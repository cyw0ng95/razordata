package EX

import (
	"context"
	"fmt"
	"sync"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// AlterTable implements ALTER TABLE DDL operations.
// REQ000244: ALTER TABLE executor (online schema migration).
type AlterTable struct {
	stmt *PS.AlterTableStmt
}

// catalogMu guards catalog operations. A separate mutex avoids
// deadlock with storeMu when the catalog rewrites its atomic file.
var catalogMu sync.Mutex

func NewAlterTable(stmt *PS.AlterTableStmt) *AlterTable {
	return &AlterTable{stmt: stmt}
}

func (a *AlterTable) Next(ctx context.Context) (Row, error) {
	switch a.stmt.Action {
	case "ADD COLUMN":
		return Row{}, a.execAddColumn()
	case "DROP COLUMN":
		return Row{}, a.execDropColumn()
	case "RENAME":
		return Row{}, a.execRename()
	default:
		return Row{}, fmt.Errorf("ex: unknown ALTER TABLE action: %s", a.stmt.Action)
	}
}

func (a *AlterTable) Close() error {
	return nil
}

func (a *AlterTable) execAddColumn() error {
	if a.stmt.NewCol == nil {
		return fmt.Errorf("ex: ADD COLUMN requires column definition")
	}

	storeMu.Lock()
	defer storeMu.Unlock()

	tableID, ok := tableIDs[a.stmt.Table]
	if !ok {
		// In-memory mode: update source.go tables/schemas
		return a.execAddColumnInMemory()
	}

	ss, ok := storeSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.stmt.Table)
	}

	// Check duplicate column name
	for _, c := range ss.cols {
		if c == a.stmt.NewCol.Name {
			return fmt.Errorf("ex: column %q already exists", a.stmt.NewCol.Name)
		}
	}

	// Re-register the complete updated schema (idempotent)
	newCols := append(append([]string(nil), ss.cols...), a.stmt.NewCol.Name)
	newNullable := append(append([]bool(nil), ss.nullable...), a.stmt.NewCol.Nullable)
	newDefaults := append(append([]PS.Expr(nil), ss.defaults...), a.stmt.NewCol.Default)
	newTypes := append(append([]int(nil), ss.colTypes...), a.stmt.NewCol.Type)
	newGenerated := append(append([]PS.Expr(nil), ss.generated...), a.stmt.NewCol.Generated)

	// Rebuild unique keys with updated column indices
	newUnique := make([]UniqueKey, len(ss.unique))
	for i, u := range ss.unique {
		newUnique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
	}

	// Copy FK constraints
	var newFKs []ForeignKeyConstraint
	if ss.foreignKeys != nil {
		newFKs = make([]ForeignKeyConstraint, len(ss.foreignKeys))
		for i, fk := range ss.foreignKeys {
			newFKs[i] = ForeignKeyConstraint{
				Columns:    append([]string(nil), fk.Columns...),
				RefTable:   fk.RefTable,
				RefColumns: append([]string(nil), fk.RefColumns...),
				OnDelete:   fk.OnDelete,
				OnUpdate:   fk.OnUpdate,
			}
		}
	}

	registerStoreSchemaWithFK(a.stmt.Table, newCols, newNullable, newDefaults, newUnique, ss.pk, newFKs)

	// Update colTypes and generated inline (registerStoreSchemaWithFK doesn't store these)
	ss.colTypes = newTypes
	ss.generated = newGenerated

	// Update persistent catalog
	catalog := Catalog()
	if catalog != nil {
		catalogMu.Lock()
		defer catalogMu.Unlock()
		entry, err := catalog.GetByID(tableID)
		if err != nil {
			return nil // in-memory table, not an error
		}
		newEntry := ls.CatalogEntry{
			Version:    entry.Version,
			TableID:    entry.TableID,
			Name:       entry.Name,
			PrimaryKey: entry.PrimaryKey,
			CreateSQL:  entry.CreateSQL,
			Unique:     entry.Unique,
		}
		newEntry.Columns = make([]ls.CatalogColumn, len(entry.Columns)+1)
		for i, c := range entry.Columns {
			newEntry.Columns[i] = c
		}
		newEntry.Columns[len(entry.Columns)] = ls.CatalogColumn{
			Name:     a.stmt.NewCol.Name,
			Type:     a.stmt.NewCol.Type,
			Nullable: a.stmt.NewCol.Nullable,
		}
		if err := catalog.Put(newEntry); err != nil {
			return fmt.Errorf("ex: catalog put %q: %w", a.stmt.Table, err)
		}
	}

	// Also update in-memory schema (for planner consistency)
	tablesMu.Lock()
	defer tablesMu.Unlock()
	schemas[a.stmt.Table] = newCols

	return nil
}

func (a *AlterTable) execAddColumnInMemory() error {
	tablesMu.Lock()
	defer tablesMu.Unlock()

	cols, ok := schemas[a.stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.stmt.Table)
	}

	// Check duplicate
	for _, c := range cols {
		if c == a.stmt.NewCol.Name {
			return fmt.Errorf("ex: column %q already exists", a.stmt.NewCol.Name)
		}
	}

	// Update in-memory schema
	newCols := append(append([]string(nil), cols...), a.stmt.NewCol.Name)
	schemas[a.stmt.Table] = newCols

	// Also register in store schemas if not already present
	storeMu.Lock()
	defer storeMu.Unlock()
	registerStoreSchema(a.stmt.Table, newCols, "")

	return nil
}

func (a *AlterTable) execDropColumn() error {
	storeMu.Lock()
	defer storeMu.Unlock()

	tableID, ok := tableIDs[a.stmt.Table]
	if !ok {
		return a.execDropColumnInMemory()
	}

	ss, ok := storeSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.stmt.Table)
	}

	// Find column index
	idx := -1
	for i, c := range ss.cols {
		if c == a.stmt.Column {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", a.stmt.Column, a.stmt.Table)
	}

	// Cannot drop the primary key column
	if ss.pk == a.stmt.Column {
		return fmt.Errorf("ex: cannot drop primary key column %q", a.stmt.Column)
	}

	// Rebuild without the dropped column
	newCols := make([]string, 0, len(ss.cols)-1)
	newNullable := make([]bool, 0, len(ss.nullable)-1)
	newDefaults := make([]PS.Expr, 0, len(ss.defaults)-1)
	newTypes := make([]int, 0, len(ss.colTypes)-1)
	newGenerated := make([]PS.Expr, 0, len(ss.generated)-1)
	for i := range ss.cols {
		if i == idx {
			continue
		}
		newCols = append(newCols, ss.cols[i])
		newNullable = append(newNullable, ss.nullable[i])
		newDefaults = append(newDefaults, ss.defaults[i])
		newTypes = append(newTypes, ss.colTypes[i])
		newGenerated = append(newGenerated, ss.generated[i])
	}

	// Rebuild unique keys: remove any UNIQUE constraint that includes the dropped column
	var newUnique []UniqueKey
	for _, u := range ss.unique {
		skip := false
		for _, ci := range u.Cols {
			if ci == idx {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Shift indices > idx down by 1
		adj := make([]int, len(u.Cols))
		for j, ci := range u.Cols {
			if ci > idx {
				adj[j] = ci - 1
			} else {
				adj[j] = ci
			}
		}
		newUnique = append(newUnique, UniqueKey{Cols: adj})
	}

	// Rebuild FK constraints: remove any FK that references the dropped column
	var newFKs []ForeignKeyConstraint
	for _, fk := range ss.foreignKeys {
		skip := false
		for _, col := range fk.Columns {
			if col == a.stmt.Column {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		newFKs = append(newFKs, ForeignKeyConstraint{
			Columns:    append([]string(nil), fk.Columns...),
			RefTable:   fk.RefTable,
			RefColumns: append([]string(nil), fk.RefColumns...),
			OnDelete:   fk.OnDelete,
			OnUpdate:   fk.OnUpdate,
		})
	}

	registerStoreSchemaWithFK(a.stmt.Table, newCols, newNullable, newDefaults, newUnique, ss.pk, newFKs)

	// Update colTypes and generated inline
	ss.colTypes = newTypes
	ss.generated = newGenerated

	// Update persistent catalog
	catalog := Catalog()
	if catalog != nil {
		catalogMu.Lock()
		defer catalogMu.Unlock()
		entry, err := catalog.GetByID(tableID)
		if err != nil {
			return nil // in-memory table
		}
		newEntry := ls.CatalogEntry{
			Version:    entry.Version,
			TableID:    entry.TableID,
			Name:       entry.Name,
			PrimaryKey: entry.PrimaryKey,
			CreateSQL:  entry.CreateSQL,
			Unique:     newUniqueForCatalog(newUnique, newCols),
		}
		newEntry.Columns = make([]ls.CatalogColumn, 0, len(entry.Columns)-1)
		for i, c := range entry.Columns {
			if i != idx {
				newEntry.Columns = append(newEntry.Columns, c)
			}
		}
		if err := catalog.Put(newEntry); err != nil {
			return fmt.Errorf("ex: catalog put %q: %w", a.stmt.Table, err)
		}
	}

	// Also update in-memory schema
	tablesMu.Lock()
	defer tablesMu.Unlock()
	schemas[a.stmt.Table] = newCols

	return nil
}

func (a *AlterTable) execDropColumnInMemory() error {
	tablesMu.Lock()
	defer tablesMu.Unlock()

	cols, ok := schemas[a.stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.stmt.Table)
	}

	idx := -1
	for i, c := range cols {
		if c == a.stmt.Column {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", a.stmt.Column, a.stmt.Table)
	}

	// Cannot drop PK column
	// (in in-memory mode we don't know which is PK, so allow it)

	newCols := make([]string, 0, len(cols)-1)
	for i, c := range cols {
		if i != idx {
			newCols = append(newCols, c)
		}
	}
	schemas[a.stmt.Table] = newCols

	// Also update store schemas
	storeMu.Lock()
	defer storeMu.Unlock()
	registerStoreSchema(a.stmt.Table, newCols, "")

	return nil
}

func (a *AlterTable) execRename() error {
	oldName := a.stmt.Table
	newName := a.stmt.Column

	storeMu.Lock()
	defer storeMu.Unlock()

	tableID, ok := tableIDs[oldName]
	if !ok {
		return a.execRenameInMemory(oldName, newName)
	}

	if _, exists := tableIDs[newName]; exists {
		return fmt.Errorf("ex: table %q already exists", newName)
	}

	ss, ok := storeSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", oldName)
	}

	// Build full schema for re-registration under new name
	newCols := append([]string(nil), ss.cols...)
	newNullable := append([]bool(nil), ss.nullable...)
	newDefaults := make([]PS.Expr, len(ss.defaults))
	copy(newDefaults, ss.defaults)
	newUnique := make([]UniqueKey, len(ss.unique))
	for i, u := range ss.unique {
		newUnique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
	}
	var newFKs []ForeignKeyConstraint
	if ss.foreignKeys != nil {
		newFKs = make([]ForeignKeyConstraint, len(ss.foreignKeys))
		for i, fk := range ss.foreignKeys {
			newFKs[i] = ForeignKeyConstraint{
				Columns:    append([]string(nil), fk.Columns...),
				RefTable:   fk.RefTable,
				RefColumns: append([]string(nil), fk.RefColumns...),
				OnDelete:   fk.OnDelete,
				OnUpdate:   fk.OnUpdate,
			}
		}
	}

	// Remove old name and register new name
	delete(tableIDs, oldName)
	delete(storeSchemas, tableID)

	registerStoreSchemaWithFK(newName, newCols, newNullable, newDefaults, newUnique, ss.pk, newFKs)

	// Copy colTypes and generated to the new schema entry
	newTableID, ok := tableIDs[newName]
	if ok {
		if newSS, exists := storeSchemas[newTableID]; exists {
			newSS.colTypes = append([]int(nil), ss.colTypes...)
			newSS.generated = append([]PS.Expr(nil), ss.generated...)
		}
	}

	// Update persistent catalog
	catalog := Catalog()
	if catalog != nil {
		catalogMu.Lock()
		defer catalogMu.Unlock()
		entry, err := catalog.GetByID(tableID)
		if err != nil {
			return nil // in-memory table
		}
		newEntry := ls.CatalogEntry{
			Version:    entry.Version,
			TableID:    entry.TableID,
			Name:       newName,
			Columns:    entry.Columns,
			PrimaryKey: entry.PrimaryKey,
			CreateSQL:  entry.CreateSQL,
			Unique:     entry.Unique,
		}
		catalog.Delete(entry.TableID)
		if err := catalog.Put(newEntry); err != nil {
			return fmt.Errorf("ex: catalog put %q: %w", newName, err)
		}
	}

	// Rename registered indexes
	if idxs, ok := registeredIndexes[oldName]; ok {
		registeredIndexes[newName] = idxs
		delete(registeredIndexes, oldName)
	}

	// Also update in-memory schemas
	tablesMu.Lock()
	defer tablesMu.Unlock()
	if cols, ok := schemas[oldName]; ok {
		schemas[newName] = append([]string(nil), cols...)
		delete(schemas, oldName)
	}

	return nil
}

func (a *AlterTable) execRenameInMemory(oldName, newName string) error {
	tablesMu.Lock()
	defer tablesMu.Unlock()

	if _, ok := schemas[newName]; ok {
		return fmt.Errorf("ex: table %q already exists", newName)
	}

	cols, ok := schemas[oldName]
	if !ok {
		return fmt.Errorf("ex: table %q not found", oldName)
	}

	schemas[newName] = append([]string(nil), cols...)
	delete(schemas, oldName)

	// Also update store schemas
	storeMu.Lock()
	defer storeMu.Unlock()
	registerStoreSchema(newName, cols, "")
	if _, ok := tableIDs[oldName]; ok {
		delete(tableIDs, oldName)
	}

	return nil
}

// newUniqueForCatalog converts EX-layer UniqueKey indices back
// to CatalogUnique column-name format.
func newUniqueForCatalog(unique []UniqueKey, cols []string) []ls.CatalogUnique {
	result := make([]ls.CatalogUnique, 0, len(unique))
	for _, u := range unique {
		cu := ls.CatalogUnique{Cols: make([]int, len(u.Cols))}
		for i, ci := range u.Cols {
			cu.Cols[i] = ci
		}
		result = append(result, cu)
	}
	return result
}

