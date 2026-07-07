package WT

import (
	"context"
	"fmt"
	"sync"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// AlterTable implements ALTER TABLE DDL operations.
// REQ000244: ALTER TABLE executor (online schema migration).
type AlterTable struct {
	Stmt *PS.AlterTableStmt
}

// catalogMu guards catalog operations. A separate mutex avoids
// deadlock with DT.StoreMu when the catalog rewrites its atomic file.
var catalogMu sync.Mutex

func NewAlterTable(stmt *PS.AlterTableStmt) *AlterTable {
	return &AlterTable{Stmt: stmt}
}

func (a *AlterTable) Next(ctx context.Context) (DT.Row, error) {
	switch a.Stmt.Action {
	case "ADD COLUMN":
		return DT.Row{}, a.execAddColumn()
	case "DROP COLUMN":
		return DT.Row{}, a.execDropColumn()
	case "RENAME":
		return DT.Row{}, a.execRename()
	case "RENAME COLUMN":
		return DT.Row{}, a.execRenameColumn()
	case "ALTER COLUMN SET DEFAULT":
		return DT.Row{}, a.execAlterColumnSetDefault()
	case "ALTER COLUMN DROP DEFAULT":
		return DT.Row{}, a.execAlterColumnDropDefault()
	default:
		return DT.Row{}, fmt.Errorf("ex: unknown ALTER TABLE action: %s", a.Stmt.Action)
	}
}

func (a *AlterTable) Close() error {
	return nil
}

func (a *AlterTable) execAddColumn() error {
	if a.Stmt.NewCol == nil {
		return fmt.Errorf("ex: ADD COLUMN requires column definition")
	}

	DT.StoreMu.Lock()
	tableID, ok := DT.TableIDs[a.Stmt.Table]
	if !ok {
		// In-memory mode: release DT.StoreMu first; the in-memory
		// helper re-acquires it (sync.Mutex is not reentrant).
		DT.StoreMu.Unlock()
		return a.execAddColumnInMemory()
	}
	defer DT.StoreMu.Unlock()

	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.Stmt.Table)
	}

	// Check duplicate column name
	for _, c := range ss.Cols {
		if c == a.Stmt.NewCol.Name {
			return fmt.Errorf("ex: column %q already exists", a.Stmt.NewCol.Name)
		}
	}

	// Re-register the complete updated schema (idempotent)
	newCols := append(append([]string(nil), ss.Cols...), a.Stmt.NewCol.Name)
	newNullable := append(append([]bool(nil), ss.Nullable...), a.Stmt.NewCol.Nullable)

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
	newDefaults = append(newDefaults, a.Stmt.NewCol.Default)

	var newTypes []LX.TokenType
	for i := range ss.Cols {
		if ss.ColTypes != nil && i < len(ss.ColTypes) {
			newTypes = append(newTypes, ss.ColTypes[i])
		} else {
			newTypes = append(newTypes, LX.TokenType(0))
		}
	}
	newTypes = append(newTypes, a.Stmt.NewCol.Type)

	var newPrecision []int
	for i := range ss.Cols {
		if ss.Precision != nil && i < len(ss.Precision) {
			newPrecision = append(newPrecision, ss.Precision[i])
		} else {
			newPrecision = append(newPrecision, 0)
		}
	}
	newPrecision = append(newPrecision, a.Stmt.NewCol.Precision)

	var newScale []int
	for i := range ss.Cols {
		if ss.Scale != nil && i < len(ss.Scale) {
			newScale = append(newScale, ss.Scale[i])
		} else {
			newScale = append(newScale, 0)
		}
	}
	newScale = append(newScale, a.Stmt.NewCol.Scale)

	var newGenerated []PS.Expr
	for i := range ss.Cols {
		if ss.Generated != nil && i < len(ss.Generated) {
			newGenerated = append(newGenerated, ss.Generated[i])
		} else {
			newGenerated = append(newGenerated, nil)
		}
	}
	newGenerated = append(newGenerated, a.Stmt.NewCol.Generated)

	// Rebuild unique keys with updated column indices
	newUnique := make([]DT.UniqueKey, len(ss.Unique))
	for i, u := range ss.Unique {
		newUnique[i] = DT.UniqueKey{Cols: append([]int(nil), u.Cols...)}
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

	DT.RegisterStoreSchemaWithFKLocked(a.Stmt.Table, newCols, newNullable, newDefaults, newUnique, ss.Pk, newFKs)

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
			Name:     a.Stmt.NewCol.Name,
			Type:     sc.TokenType(a.Stmt.NewCol.Type),
			Nullable: a.Stmt.NewCol.Nullable,
		}
		if err := catalog.Put(newEntry); err != nil {
			return fmt.Errorf("ex: catalog put %q: %w", a.Stmt.Table, err)
		}
	}

	// Also update in-memory schema (for planner consistency)
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	DT.Schemas[a.Stmt.Table] = newCols

	return nil
}

func (a *AlterTable) execAddColumnInMemory() error {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()

	cols, ok := DT.Schemas[a.Stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.Stmt.Table)
	}

	// Check duplicate
	for _, c := range cols {
		if c == a.Stmt.NewCol.Name {
			return fmt.Errorf("ex: column %q already exists", a.Stmt.NewCol.Name)
		}
	}

	// Update in-memory schema
	newCols := append(append([]string(nil), cols...), a.Stmt.NewCol.Name)
	DT.Schemas[a.Stmt.Table] = newCols

	// DT.RegisterStoreSchema takes DT.StoreMu internally; the helper
	// must not be called with DT.StoreMu already held.
	DT.RegisterStoreSchema(a.Stmt.Table, newCols, "")

	return nil
}

func (a *AlterTable) execDropColumn() error {
	DT.StoreMu.Lock()
	tableID, ok := DT.TableIDs[a.Stmt.Table]
	if !ok {
		DT.StoreMu.Unlock()
		return a.execDropColumnInMemory()
	}
	defer DT.StoreMu.Unlock()

	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.Stmt.Table)
	}

	// Find column index
	idx := -1
	for i, c := range ss.Cols {
		if c == a.Stmt.Column {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", a.Stmt.Column, a.Stmt.Table)
	}

	// Cannot drop the primary key column
	if ss.Pk == a.Stmt.Column {
		return fmt.Errorf("ex: cannot drop primary key column %q", a.Stmt.Column)
	}

	// REQ001325: cascade to generated columns whose expression references
	// the dropped column. Collect indices of all columns (the target plus
	// any generated dependents) that must be removed in this ALTER.
	toDrop := map[int]bool{idx: true}
	if ss.Generated != nil {
		for gi, gExpr := range ss.Generated {
			if gi == idx || toDrop[gi] {
				continue
			}
			if gExpr == nil {
				continue
			}
			if exprReferencesColumn(gExpr, a.Stmt.Column) {
				toDrop[gi] = true
			}
		}
	}

	// Rebuild without the dropped columns (primary + any cascaded generated).
	newCols := make([]string, 0, len(ss.Cols)-len(toDrop))
	newNullable := make([]bool, 0, len(ss.Nullable)-len(toDrop))
	var newDefaults []PS.Expr
	if ss.Defaults != nil {
		newDefaults = make([]PS.Expr, 0, len(ss.Defaults)-len(toDrop))
	}
	var newTypes []LX.TokenType
	if ss.ColTypes != nil {
		newTypes = make([]LX.TokenType, 0, len(ss.ColTypes)-len(toDrop))
	}
	var newGenerated []PS.Expr
	if ss.Generated != nil {
		newGenerated = make([]PS.Expr, 0, len(ss.Generated)-len(toDrop))
	}
	var newPrecision []int
	if ss.Precision != nil {
		newPrecision = make([]int, 0, len(ss.Precision)-len(toDrop))
	}
	var newScale []int
	if ss.Scale != nil {
		newScale = make([]int, 0, len(ss.Scale)-len(toDrop))
	}
	for i := range ss.Cols {
		if toDrop[i] {
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

	// Rebuild unique keys: remove any UNIQUE constraint that includes any
	// of the dropped columns (primary + cascaded generated). REQ001325.
	var newUnique []DT.UniqueKey
	for _, u := range ss.Unique {
		skip := false
		for _, ci := range u.Cols {
			if toDrop[ci] {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Shift indices that come after any dropped column down by the
		// count of removed columns at positions < ci.
		adj := make([]int, len(u.Cols))
		for j, ci := range u.Cols {
			drop := 0
			for d := range toDrop {
				if d < ci {
					drop++
				}
			}
			adj[j] = ci - drop
		}
		newUnique = append(newUnique, DT.UniqueKey{Cols: adj})
	}

	// Rebuild FK constraints: remove any FK that references the dropped column.
	// REQ001324 — the resulting slice is always non-nil so that even when all FKs
	// are cascaded-dropped, the helper overwrites ss.ForeignKeys with an empty
	// list rather than leaving the stale entries in place.
	newFKs := make([]DT.ForeignKeyConstraint, 0, len(ss.ForeignKeys))
	for _, fk := range ss.ForeignKeys {
		skip := false
		for _, col := range fk.Columns {
			if col == a.Stmt.Column {
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

	DT.RegisterStoreSchemaWithFKLocked(a.Stmt.Table, newCols, newNullable, newDefaults, newUnique, ss.Pk, newFKs)

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
			Unique:     NewUniqueForCatalog(newUnique, newCols),
		}
		newEntry.Columns = make([]ls.CatalogColumn, 0, len(entry.Columns)-1)
		for i, c := range entry.Columns {
			if i != idx {
				newEntry.Columns = append(newEntry.Columns, c)
			}
		}
		if err := catalog.Put(newEntry); err != nil {
			return fmt.Errorf("ex: catalog put %q: %w", a.Stmt.Table, err)
		}
	}

	// Also update in-memory schema
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	DT.Schemas[a.Stmt.Table] = newCols

	// Update existing row data to remove the dropped column
	if existing, ok := DT.Tables[a.Stmt.Table]; ok {
		updated := make([]DT.Row, len(existing))
		for i, row := range existing {
			newData := make([]DT.Value, 0, len(row.Data)-1)
			newRowCols := make([]string, 0, len(row.Cols)-1)
			for j := range row.Data {
				if j != idx {
					newData = append(newData, row.Data[j])
					if j < len(row.Cols) {
						newRowCols = append(newRowCols, row.Cols[j])
					}
				}
			}
			updated[i] = DT.Row{Cols: newRowCols, Types: row.Types, Data: newData, Outer: row.Outer}
		}
		DT.Tables[a.Stmt.Table] = updated
	}

	return nil
}

func (a *AlterTable) execDropColumnInMemory() error {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()

	cols, ok := DT.Schemas[a.Stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.Stmt.Table)
	}

	idx := -1
	for i, c := range cols {
		if c == a.Stmt.Column {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", a.Stmt.Column, a.Stmt.Table)
	}

	newCols := make([]string, 0, len(cols)-1)
	for i, c := range cols {
		if i != idx {
			newCols = append(newCols, c)
		}
	}
	DT.Schemas[a.Stmt.Table] = newCols

	// Update existing row data to remove the dropped column
	if existing, ok := DT.Tables[a.Stmt.Table]; ok {
		updated := make([]DT.Row, len(existing))
		for i, row := range existing {
			newData := make([]DT.Value, 0, len(row.Data)-1)
			newRowCols := make([]string, 0, len(row.Cols)-1)
			for j := range row.Data {
				if j != idx {
					newData = append(newData, row.Data[j])
					if j < len(row.Cols) {
						newRowCols = append(newRowCols, row.Cols[j])
					}
				}
			}
			updated[i] = DT.Row{Cols: newRowCols, Types: row.Types, Data: newData, Outer: row.Outer}
		}
		DT.Tables[a.Stmt.Table] = updated
	}

	DT.RegisterStoreSchema(a.Stmt.Table, newCols, "")

	return nil
}

func (a *AlterTable) execRename() error {
	oldName := a.Stmt.Table
	newName := a.Stmt.Column

	DT.StoreMu.Lock()
	tableID, ok := DT.TableIDs[oldName]
	if !ok {
		DT.StoreMu.Unlock()
		return a.execRenameInMemory(oldName, newName)
	}

	if _, exists := DT.TableIDs[newName]; exists {
		DT.StoreMu.Unlock()
		return fmt.Errorf("ex: table %q already exists", newName)
	}

	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		DT.StoreMu.Unlock()
		return fmt.Errorf("ex: table schema for %q not found", oldName)
	}

	// Build full schema for re-registration under new name
	newCols := append([]string(nil), ss.Cols...)
	newNullable := append([]bool(nil), ss.Nullable...)
	newDefaults := make([]PS.Expr, len(ss.Defaults))
	copy(newDefaults, ss.Defaults)
	newUnique := make([]DT.UniqueKey, len(ss.Unique))
	for i, u := range ss.Unique {
		newUnique[i] = DT.UniqueKey{Cols: append([]int(nil), u.Cols...)}
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

	// Update persistent catalog (still under StoreMu — catalogMu is independent).
	catalog := DT.Catalog()
	if catalog != nil {
		catalogMu.Lock()
		entry, err := catalog.GetByID(tableID)
		if err != nil {
			catalogMu.Unlock()
		} else {
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
			putErr := catalog.Put(newEntry)
			catalogMu.Unlock()
			if putErr != nil {
				DT.StoreMu.Unlock()
				return fmt.Errorf("ex: catalog put %q: %w", newName, putErr)
			}
		}
	}

	// Rename registered indexes
	if idxs, ok := DT.RegisteredIndexes[oldName]; ok {
		DT.RegisteredIndexes[newName] = idxs
		delete(DT.RegisteredIndexes, oldName)
	}

	// Also update in-memory DT.Schemas — hold TablesMu briefly.
	DT.TablesMu.Lock()
	if cols, ok := DT.Schemas[oldName]; ok {
		DT.Schemas[newName] = append([]string(nil), cols...)
		delete(DT.Schemas, oldName)
	}
	DT.TablesMu.Unlock()

	// Release StoreMu BEFORE calling helpers that re-acquire it.
	DT.StoreMu.Unlock()

	// REQ001321: cascade rename to FK RefTable, view FROM/JOIN, trigger OnTable.
	renameFKReferencesInSchemas(oldName, newName)
	renameViewReferences(oldName, newName)
	renameTriggerReferences(oldName, newName)

	return nil
}

// renameFKReferencesInSchemas walks every StoreSchema and rewrites FK
// RefTable entries from oldName to newName. REQ001321.
func renameFKReferencesInSchemas(oldName, newName string) {
	DT.StoreMu.Lock()
	defer DT.StoreMu.Unlock()
	for _, ss := range DT.StoreSchemas {
		if ss == nil || len(ss.ForeignKeys) == 0 {
			continue
		}
		for i, fk := range ss.ForeignKeys {
			if fk.RefTable == oldName {
				ss.ForeignKeys[i].RefTable = newName
			}
		}
	}
}

// renameViewReferences walks the view registry and rewrites any SELECT
// whose FROM or JOIN clause references the old table. REQ001321.
func renameViewReferences(oldName, newName string) {
	DT.ViewMu.Lock()
	defer DT.ViewMu.Unlock()
	for _, sel := range DT.ViewRegistry {
		if sel == nil {
			continue
		}
		if sel.From == oldName {
			sel.From = newName
		}
		for i := range sel.Joins {
			if sel.Joins[i].Right == oldName {
				sel.Joins[i].Right = newName
			}
		}
	}
}

// renameTriggerRewrites the trigger registry: any TriggerStmt whose
// OnTable equals the old name is rewritten to point at the new name.
// REQ001321.
func renameTriggerReferences(oldName, newName string) {
	triggerMu.Lock()
	defer triggerMu.Unlock()
	for _, t := range triggerReg {
		if t != nil && t.OnTable == oldName {
			t.OnTable = newName
		}
	}
	if list, ok := tableTriggers[oldName]; ok {
		delete(tableTriggers, oldName)
		tableTriggers[newName] = list
	}
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

	// REQ001321: cascade rename into other in-memory tables' FK RefTable,
	// views FROM/JOIN, and trigger OnTable.
	renameFKReferencesInSchemas(oldName, newName)
	renameViewReferences(oldName, newName)
	renameTriggerReferences(oldName, newName)

	return nil
}

// newUniqueForCatalog converts EX-layer DT.UniqueKey indices back
// to CatalogUnique column-name format.
func NewUniqueForCatalog(unique []DT.UniqueKey, cols []string) []ls.CatalogUnique {
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
	oldCol := a.Stmt.Column
	newCol := a.Stmt.NewName
	if oldCol == "" || newCol == "" {
		return fmt.Errorf("ex: RENAME COLUMN requires old and new column names")
	}

	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()

	cols, ok := DT.Schemas[a.Stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.Stmt.Table)
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
		return fmt.Errorf("ex: column %q not found in table %q", oldCol, a.Stmt.Table)
	}

	// Check new name doesn't already exist
	for _, c := range cols {
		if c == newCol {
			return fmt.Errorf("ex: column %q already exists in table %q", newCol, a.Stmt.Table)
		}
	}

	// Rename in schema
	newCols := make([]string, len(cols))
	copy(newCols, cols)
	newCols[idx] = newCol
	DT.Schemas[a.Stmt.Table] = newCols

	// Also update store DT.Schemas
	DT.StoreMu.Lock()
	if id, ok := DT.TableIDs[a.Stmt.Table]; ok {
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

// execAlterColumnSetDefault updates the default expression for an existing
// column without rewriting the table. REQ001322.
func (a *AlterTable) execAlterColumnSetDefault() error {
	if a.Stmt.NewExpr == nil {
		return fmt.Errorf("ex: ALTER COLUMN SET DEFAULT requires an expression")
	}

	// REQ001322: semantics check — refuse if expr references a column that
	// does not exist on this table. We walk the Expr tree and compare
	// IdentExpr names against the table's column set.
	if err := a.checkDefaultExprSemantics(); err != nil {
		return err
	}

	DT.StoreMu.Lock()
	defer DT.StoreMu.Unlock()

	tableID, ok := DT.TableIDs[a.Stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.Stmt.Table)
	}
	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.Stmt.Table)
	}

	idx := -1
	for i, c := range ss.Cols {
		if c == a.Stmt.Column {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", a.Stmt.Column, a.Stmt.Table)
	}

	// Defaults is parallel to Cols; align length then write at idx.
	if ss.Defaults == nil {
		ss.Defaults = make([]PS.Expr, len(ss.Cols))
	} else if len(ss.Defaults) < len(ss.Cols) {
		grown := make([]PS.Expr, len(ss.Cols))
		copy(grown, ss.Defaults)
		ss.Defaults = grown
	}
	ss.Defaults[idx] = a.Stmt.NewExpr

	return nil
}

// execAlterColumnDropDefault clears the default expression for an existing
// column. REQ001323.
func (a *AlterTable) execAlterColumnDropDefault() error {
	DT.StoreMu.Lock()
	defer DT.StoreMu.Unlock()

	tableID, ok := DT.TableIDs[a.Stmt.Table]
	if !ok {
		return fmt.Errorf("ex: table %q not found", a.Stmt.Table)
	}
	ss, ok := DT.StoreSchemas[tableID]
	if !ok {
		return fmt.Errorf("ex: table schema for %q not found", a.Stmt.Table)
	}

	idx := -1
	for i, c := range ss.Cols {
		if c == a.Stmt.Column {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("ex: column %q not found in table %q", a.Stmt.Column, a.Stmt.Table)
	}

	if ss.Defaults == nil || idx >= len(ss.Defaults) {
		return nil // already absent
	}
	ss.Defaults[idx] = nil
	return nil
}

// checkDefaultExprSemantics rejects a default expression that references
// column names not present on the target table. REQ001322 extra-check.
func (a *AlterTable) checkDefaultExprSemantics() error {
	colSet := map[string]bool{}
	if id, ok := DT.TableIDs[a.Stmt.Table]; ok {
		if ss, ok := DT.StoreSchemas[id]; ok {
			for _, c := range ss.Cols {
				colSet[c] = true
			}
		}
	}
	bad := defaultExprUnknownColumns(a.Stmt.NewExpr, colSet)
	if bad != "" {
		return fmt.Errorf("ex: default expression references unknown column %q", bad)
	}
	return nil
}

// defaultExprUnknownColumns walks the Expr and returns the first unknown
// column name referenced via Ident or QualifiedName, or "" if all references
// resolve. REQ001322.
func defaultExprUnknownColumns(e PS.Expr, known map[string]bool) string {
	switch v := e.(type) {
	case *PS.Ident:
		if v.Name == "" {
			return ""
		}
		if !known[v.Name] {
			return v.Name
		}
		return ""
	case *PS.QualifiedName:
		if v.Name == "" {
			return ""
		}
		if !known[v.Name] {
			return v.Name
		}
		return ""
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return ""
	case *PS.BinaryExpr:
		if bad := defaultExprUnknownColumns(v.Left, known); bad != "" {
			return bad
		}
		return defaultExprUnknownColumns(v.Right, known)
	case *PS.UnaryExpr:
		return defaultExprUnknownColumns(v.Operand, known)
	case *PS.CastExpr:
		return defaultExprUnknownColumns(v.Expr, known)
	case *PS.CaseExpr:
		for _, w := range v.WhenList {
			if bad := defaultExprUnknownColumns(w.Cond, known); bad != "" {
				return bad
			}
			if bad := defaultExprUnknownColumns(w.Then, known); bad != "" {
				return bad
			}
		}
		if v.Else != nil {
			return defaultExprUnknownColumns(v.Else, known)
		}
		return ""
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			if bad := defaultExprUnknownColumns(arg, known); bad != "" {
				return bad
			}
		}
		return ""
	case *PS.ListExpr:
		for _, x := range v.Items {
			if bad := defaultExprUnknownColumns(x, known); bad != "" {
				return bad
			}
		}
		return ""
	case *PS.SubqueryExpr:
		return "" // subqueries not allowed in default exprs
	default:
		return ""
	}
}

// exprReferencesColumn reports whether e (a generated-column expression)
// references the given column name. REQ001325.
func exprReferencesColumn(e PS.Expr, colName string) bool {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name == colName
	case *PS.QualifiedName:
		return v.Name == colName
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return false
	case *PS.BinaryExpr:
		return exprReferencesColumn(v.Left, colName) || exprReferencesColumn(v.Right, colName)
	case *PS.UnaryExpr:
		return exprReferencesColumn(v.Operand, colName)
	case *PS.CastExpr:
		return exprReferencesColumn(v.Expr, colName)
	case *PS.CaseExpr:
		for _, w := range v.WhenList {
			if exprReferencesColumn(w.Cond, colName) || exprReferencesColumn(w.Then, colName) {
				return true
			}
		}
		if v.Else != nil {
			return exprReferencesColumn(v.Else, colName)
		}
		return false
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			if exprReferencesColumn(arg, colName) {
				return true
			}
		}
		return false
	case *PS.ListExpr:
		for _, x := range v.Items {
			if exprReferencesColumn(x, colName) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
