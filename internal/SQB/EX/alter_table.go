package EX

import (
	"context"
	"fmt"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// AlterTable implements ALTER TABLE DDL operations.
// REQ000244: ALTER TABLE executor (online schema migration).
type AlterTable struct {
	stmt *PS.AlterTableStmt
}

// catalogMu guards catalog operations. A separate mutex avoids
// deadlock with DT.StoreMu when the catalog rewrites its atomic file.
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
	case "RENAME COLUMN":
		return Row{}, a.execRenameColumn()
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

	DT.StoreMu.Lock()
	tableID, ok := DT.TableIDs[a.stmt.Table]
	if !ok {
		// In-memory mode: release DT.StoreMu first; the in-memory
		// helper re-acquires it (sync.Mutex is not reentrant).
		DT.StoreMu.Unlock()
		return a.execAddColumnInMemory()
	}
	defer DT.StoreMu.Unlock()

	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.stmt.Table)
	}

	// Check duplicate column name
	for _, c := range ss.Cols {
		if c == a.stmt.NewCol.Name {
			return fmt.Errorf("ex: column %q already exists", a.stmt.NewCol.Name)
		}
	}

	// Re-register the complete updated schema (idempotent)
	newCols := append(append([]string(nil), ss.Cols...), a.stmt.NewCol.Name)
	newNullable := append(append([]bool(nil), ss.Nullable...), a.stmt.NewCol.Nullable)

	// Pad parallel slices that may be nil or shorter than ss.Cols.
	// The loop produces slices aligned to the new schema length.
	var newDefaults []PS.Expr
	for i := range ss.Cols {
		if ss.Defaults != nil && i < len(ss.Defaults) {
			newDefaults = append(newDefaults, ss.Defaults[i])
		} else {
			newDefaults = append(newDefaults, nil)
		}
	}
	newDefaults = append(newDefaults, a.stmt.NewCol.Default)

	var newTypes []LX.TokenType
	for i := range ss.Cols {
		if ss.ColTypes != nil && i < len(ss.ColTypes) {
			newTypes = append(newTypes, ss.ColTypes[i])
		} else {
			newTypes = append(newTypes, LX.TokenType(0))
		}
	}
	newTypes = append(newTypes, a.stmt.NewCol.Type)

	var newPrecision []int
	for i := range ss.Cols {
		if ss.Precision != nil && i < len(ss.Precision) {
			newPrecision = append(newPrecision, ss.Precision[i])
		} else {
			newPrecision = append(newPrecision, 0)
		}
	}
	newPrecision = append(newPrecision, a.stmt.NewCol.Precision)

	var newScale []int
	for i := range ss.Cols {
		if ss.Scale != nil && i < len(ss.Scale) {
			newScale = append(newScale, ss.Scale[i])
		} else {
			newScale = append(newScale, 0)
		}
	}
	newScale = append(newScale, a.stmt.NewCol.Scale)

	var newGenerated []PS.Expr
	for i := range ss.Cols {
		if ss.Generated != nil && i < len(ss.Generated) {
			newGenerated = append(newGenerated, ss.Generated[i])
		} else {
			newGenerated = append(newGenerated, nil)
		}
	}
	newGenerated = append(newGenerated, a.stmt.NewCol.Generated)

	// Rebuild unique keys with updated column indices
	newUnique := make([]UniqueKey, len(ss.Unique))
	for i, u := range ss.Unique {
		newUnique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
	}

	// Copy FK constraints
	var newFKs []DT.ForeignKeyConstraint
	if ss.ForeignKeys != nil {
		newFKs = make([]DT.ForeignKeyConstraint, len(ss.ForeignKeys))
		for i, fk := range ss.ForeignKeys {
			newFKs[i] = DT.ForeignKeyConstraint{
				Columns:    append([]string(nil), fk.Columns...),
				RefTable:   fk.RefTable,
				RefColumns: append([]string(nil), fk.RefColumns...),
				OnDelete:   fk.OnDelete,
				OnUpdate:   fk.OnUpdate,
			}
		}
	}

	DT.RegisterStoreSchemaWithFKLocked(a.stmt.Table, newCols, newNullable, newDefaults, newUnique, ss.Pk, newFKs)

	// Update colTypes, precision, scale and generated inline
	ss.ColTypes = newTypes
	ss.Precision = newPrecision
	ss.Scale = newScale
	ss.Generated = newGenerated

	// Update persistent catalog
	catalog := DT.Catalog()
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
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	DT.Schemas[a.stmt.Table] = newCols

	return nil
}

func (a *AlterTable) execAddColumnInMemory() error {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()

	cols, ok := DT.Schemas[a.stmt.Table]
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
	DT.Schemas[a.stmt.Table] = newCols

	// DT.RegisterStoreSchema takes DT.StoreMu internally; the helper
	// must not be called with DT.StoreMu already held.
	DT.RegisterStoreSchema(a.stmt.Table, newCols, "")

	return nil
}

func (a *AlterTable) execDropColumn() error {
	DT.StoreMu.Lock()
	tableID, ok := DT.TableIDs[a.stmt.Table]
	if !ok {
		DT.StoreMu.Unlock()
		return a.execDropColumnInMemory()
	}
	defer DT.StoreMu.Unlock()

	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.stmt.Table)
	}

	// Find column index
	idx := -1
	for i, c := range ss.Cols {
		if c == a.stmt.Column {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", a.stmt.Column, a.stmt.Table)
	}

	// Cannot drop the primary key column
	if ss.Pk == a.stmt.Column {
		return fmt.Errorf("ex: cannot drop primary key column %q", a.stmt.Column)
	}

	// Rebuild without the dropped column
	newCols := make([]string, 0, len(ss.Cols)-1)
	newNullable := make([]bool, 0, len(ss.Nullable)-1)
	var newDefaults []PS.Expr
	if ss.Defaults != nil {
		newDefaults = make([]PS.Expr, 0, len(ss.Defaults)-1)
	}
	var newTypes []LX.TokenType
	if ss.ColTypes != nil {
		newTypes = make([]LX.TokenType, 0, len(ss.ColTypes)-1)
	}
	var newGenerated []PS.Expr
	if ss.Generated != nil {
		newGenerated = make([]PS.Expr, 0, len(ss.Generated)-1)
	}
	var newPrecision []int
	if ss.Precision != nil {
		newPrecision = make([]int, 0, len(ss.Precision)-1)
	}
	var newScale []int
	if ss.Scale != nil {
		newScale = make([]int, 0, len(ss.Scale)-1)
	}
	for i := range ss.Cols {
		if i == idx {
			continue
		}
		newCols = append(newCols, ss.Cols[i])
		newNullable = append(newNullable, ss.Nullable[i])
		if ss.Defaults != nil {
			newDefaults = append(newDefaults, ss.Defaults[i])
		}
		if ss.ColTypes != nil {
			newTypes = append(newTypes, ss.ColTypes[i])
		}
		if ss.Generated != nil {
			newGenerated = append(newGenerated, ss.Generated[i])
		}
		if ss.Precision != nil {
			newPrecision = append(newPrecision, ss.Precision[i])
		}
		if ss.Scale != nil {
			newScale = append(newScale, ss.Scale[i])
		}
	}

	// Rebuild unique keys: remove any UNIQUE constraint that includes the dropped column
	var newUnique []UniqueKey
	for _, u := range ss.Unique {
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
	var newFKs []DT.ForeignKeyConstraint
	for _, fk := range ss.ForeignKeys {
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
		newFKs = append(newFKs, DT.ForeignKeyConstraint{
			Columns:    append([]string(nil), fk.Columns...),
			RefTable:   fk.RefTable,
			RefColumns: append([]string(nil), fk.RefColumns...),
			OnDelete:   fk.OnDelete,
			OnUpdate:   fk.OnUpdate,
		})
	}

	DT.RegisterStoreSchemaWithFKLocked(a.stmt.Table, newCols, newNullable, newDefaults, newUnique, ss.Pk, newFKs)

	// Update colTypes, precision, scale and generated inline
	ss.ColTypes = newTypes
	ss.Precision = newPrecision
	ss.Scale = newScale
	ss.Generated = newGenerated

	// Update persistent catalog
	catalog := DT.Catalog()
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
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	DT.Schemas[a.stmt.Table] = newCols

	// Update existing row data to remove the dropped column
	if existing, ok := DT.Tables[a.stmt.Table]; ok {
		updated := make([]Row, len(existing))
		for i, row := range existing {
			newData := make([]Value, 0, len(row.Data)-1)
			newRowCols := make([]string, 0, len(row.Cols)-1)
			for j := range row.Data {
				if j != idx {
					newData = append(newData, row.Data[j])
					if j < len(row.Cols) {
						newRowCols = append(newRowCols, row.Cols[j])
					}
				}
			}
			updated[i] = Row{Cols: newRowCols, Types: row.Types, Data: newData, Outer: row.Outer}
		}
		DT.Tables[a.stmt.Table] = updated
	}

	return nil
}

func (a *AlterTable) execDropColumnInMemory() error {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()

	cols, ok := DT.Schemas[a.stmt.Table]
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

	newCols := make([]string, 0, len(cols)-1)
	for i, c := range cols {
		if i != idx {
			newCols = append(newCols, c)
		}
	}
	DT.Schemas[a.stmt.Table] = newCols

	// Update existing row data to remove the dropped column
	if existing, ok := DT.Tables[a.stmt.Table]; ok {
		updated := make([]Row, len(existing))
		for i, row := range existing {
			newData := make([]Value, 0, len(row.Data)-1)
			newRowCols := make([]string, 0, len(row.Cols)-1)
			for j := range row.Data {
				if j != idx {
					newData = append(newData, row.Data[j])
					if j < len(row.Cols) {
						newRowCols = append(newRowCols, row.Cols[j])
					}
				}
			}
			updated[i] = Row{Cols: newRowCols, Types: row.Types, Data: newData, Outer: row.Outer}
		}
		DT.Tables[a.stmt.Table] = updated
	}

	DT.RegisterStoreSchema(a.stmt.Table, newCols, "")

	return nil
}

func (a *AlterTable) execRename() error {
	oldName := a.stmt.Table
	newName := a.stmt.Column

	DT.StoreMu.Lock()
	tableID, ok := DT.TableIDs[oldName]
	if !ok {
		DT.StoreMu.Unlock()
		return a.execRenameInMemory(oldName, newName)
	}
	defer DT.StoreMu.Unlock()

	if _, exists := DT.TableIDs[newName]; exists {
		return fmt.Errorf("ex: table %q already exists", newName)
	}

	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", oldName)
	}

	// Build full schema for re-registration under new name
	newCols := append([]string(nil), ss.Cols...)
	newNullable := append([]bool(nil), ss.Nullable...)
	newDefaults := make([]PS.Expr, len(ss.Defaults))
	copy(newDefaults, ss.Defaults)
	newUnique := make([]UniqueKey, len(ss.Unique))
	for i, u := range ss.Unique {
		newUnique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
	}
	var newFKs []DT.ForeignKeyConstraint
	if ss.ForeignKeys != nil {
		newFKs = make([]DT.ForeignKeyConstraint, len(ss.ForeignKeys))
		for i, fk := range ss.ForeignKeys {
			newFKs[i] = DT.ForeignKeyConstraint{
				Columns:    append([]string(nil), fk.Columns...),
				RefTable:   fk.RefTable,
				RefColumns: append([]string(nil), fk.RefColumns...),
				OnDelete:   fk.OnDelete,
				OnUpdate:   fk.OnUpdate,
			}
		}
	}

	// Remove old name and register new name
	delete(DT.TableIDs, oldName)
	delete(DT.StoreSchemas, tableID)

	DT.RegisterStoreSchemaWithFKLocked(newName, newCols, newNullable, newDefaults, newUnique, ss.Pk, newFKs)

	// Copy colTypes and generated to the new schema entry
	newTableID, ok := DT.TableIDs[newName]
	if ok {
		if newSS, exists := DT.StoreSchemas[newTableID]; exists {
			newSS.ColTypes = append([]LX.TokenType(nil), ss.ColTypes...)
			newSS.Precision = append([]int(nil), ss.Precision...)
			newSS.Scale = append([]int(nil), ss.Scale...)
			newSS.Generated = append([]PS.Expr(nil), ss.Generated...)
		}
	}

	// Update persistent catalog
	catalog := DT.Catalog()
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
	if idxs, ok := DT.RegisteredIndexes[oldName]; ok {
		DT.RegisteredIndexes[newName] = idxs
		delete(DT.RegisteredIndexes, oldName)
	}

	// Also update in-memory DT.Schemas
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	if cols, ok := DT.Schemas[oldName]; ok {
		DT.Schemas[newName] = append([]string(nil), cols...)
		delete(DT.Schemas, oldName)
	}

	return nil
}

func (a *AlterTable) execRenameInMemory(oldName, newName string) error {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()

	if _, ok := DT.Schemas[newName]; ok {
		return fmt.Errorf("ex: table %q already exists", newName)
	}

	cols, ok := DT.Schemas[oldName]
	if !ok {
		return fmt.Errorf("ex: table %q not found", oldName)
	}

	DT.Schemas[newName] = append([]string(nil), cols...)
	delete(DT.Schemas, oldName)

	// DT.RegisterStoreSchema takes DT.StoreMu internally; the helper
	// must not be called with DT.StoreMu already held.
	DT.RegisterStoreSchema(newName, cols, "")
	if _, ok := DT.TableIDs[oldName]; ok {
		delete(DT.TableIDs, oldName)
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

// execRenameColumn handles ALTER TABLE t RENAME COLUMN old TO new.
// REQ000498: renames a column in the in-memory schema and store schema.
func (a *AlterTable) execRenameColumn() error {
	oldCol := a.stmt.Column
	newCol := a.stmt.NewName
	if oldCol == "" || newCol == "" {
		return fmt.Errorf("ex: RENAME COLUMN requires old and new column names")
	}

	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()

	cols, ok := DT.Schemas[a.stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.stmt.Table)
	}

	// Find the column
	idx := -1
	for i, c := range cols {
		if c == oldCol {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", oldCol, a.stmt.Table)
	}

	// Check new name doesn't already exist
	for _, c := range cols {
		if c == newCol {
			return fmt.Errorf("ex: column %q already exists in table %q", newCol, a.stmt.Table)
		}
	}

	// Rename in schema
	newCols := make([]string, len(cols))
	copy(newCols, cols)
	newCols[idx] = newCol
	DT.Schemas[a.stmt.Table] = newCols

	// Also update store DT.Schemas
	DT.StoreMu.Lock()
	if id, ok := DT.TableIDs[a.stmt.Table]; ok {
		if ss, ok := DT.StoreSchemas[id]; ok {
			newStoreCols := make([]string, len(ss.Cols))
			copy(newStoreCols, ss.Cols)
			newStoreCols[idx] = newCol
			ss.Cols = newStoreCols
			ss.BuildColIndex()
		}
	}
	DT.StoreMu.Unlock()

	return nil
}
