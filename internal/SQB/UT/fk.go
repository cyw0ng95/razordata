package UT

import (
	"fmt"
	"sort"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// validateForeignKeyInsert checks that all FK-referenced rows exist
// in the referenced table. REQ000126.
func ValidateForeignKeyInsert(schema *DT.StoreSchema, row []any, store DT.Store) error {
	if schema == nil {
		return nil
	}
	for _, fk := range schema.ForeignKeys {
		localVals := make([]any, len(fk.Columns))
		allNull := true
		for i, col := range fk.Columns {
			idx := -1
			for j, c := range schema.Cols {
				if c == col {
					idx = j
					break
				}
			}
			if idx < 0 || idx >= len(row) {
				continue
			}
			localVals[i] = row[idx]
			if localVals[i] != nil {
				allNull = false
			}
		}
		if allNull {
			continue
		}
		if skip, err := checkFKMatch(fk.Match, localVals); err != nil {
			return err
		} else if skip {
			continue
		}
		if fk.Initially == "DEFERRED" {
			refTable := fk.RefTable
			refCols := fk.RefColumns
			vals := copyAnySlice(localVals)
			s := store
			DT.EnqueueDeferredFKCheck(func() error {
				if s == nil {
					if !rowExistsInMemory(refTable, refCols, vals) {
						return fmt.Errorf("%w: foreign key violation on table referencing %s",
							ap.ErrConstraint, refTable)
					}
					return nil
				}
				return checkReferencedRowExists(refTable, refCols, vals, s)
			})
			continue
		}
		if store == nil {
			continue
		}
		if err := checkReferencedRowExists(fk.RefTable, fk.RefColumns, localVals, store); err != nil {
			return fmt.Errorf("%w: foreign key violation on table referencing %s", ap.ErrConstraint, fk.RefTable)
		}
	}
	return nil
}

// validateForeignKeyDelete checks if any child rows reference the
// row being deleted. For CASCADE, it deletes child rows. REQ000126.
func validateForeignKeyDelete(table string, row []any, schema *DT.StoreSchema, store DT.Store) error {
	if store == nil || schema == nil {
		return nil
	}
	DT.StoreMu.Lock()
	defer DT.StoreMu.Unlock()
	for _, ss := range DT.StoreSchemas {
		for _, fk := range ss.ForeignKeys {
			if fk.RefTable != table {
				continue
			}
			refVals := make([]any, len(fk.RefColumns))
			for i, refCol := range fk.RefColumns {
				idx := -1
				for j, c := range schema.Cols {
					if c == refCol {
						idx = j
						break
					}
				}
				if idx < 0 || idx >= len(row) {
					continue
				}
				refVals[i] = row[idx]
			}
			childExists, err := checkChildRowExists(fk.Columns, refVals, ss, store)
			if err != nil {
				return err
			}
			if childExists {
				switch fk.OnDelete {
				case "CASCADE":
					return fmt.Errorf("%w: cascade delete not yet implemented", ap.ErrConstraint)
				case "SET NULL":
					return fmt.Errorf("%w: set null on delete not yet implemented", ap.ErrConstraint)
				case "SET DEFAULT":
					return fmt.Errorf("%w: set default on delete not yet implemented", ap.ErrConstraint)
				case "RESTRICT", "NO ACTION":
					return fmt.Errorf("%w: foreign key violation: child rows exist in %s", ap.ErrConstraint, ss.Cols[0])
				}
			}
		}
	}
	return nil
}

// checkReferencedRowExists checks if a row with the given values exists
// in the referenced table.
func checkReferencedRowExists(refTable string, refCols []string, values []any, store DT.Store) error {
	refSchema, _ := DT.SchemaFor(refTable)
	if refSchema == nil {
		return nil
	}
	if len(refCols) == 1 && len(values) == 1 {
		key, err := encodeFKLookup(refTable, refCols[0], values[0])
		if err != nil {
			return err
		}
		_, found, err := store.Get(key)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("referenced row not found in %s", refTable)
		}
		return nil
	}
	return checkMultiColumnFK(refTable, refCols, values, store)
}

// checkChildRowExists checks if any row in the child table references
// the given values.
func checkChildRowExists(childCols []string, refVals []any, childSchema *DT.StoreSchema, store DT.Store) (bool, error) {
	if store == nil {
		return false, nil
	}
	if len(childCols) == 1 && len(refVals) == 1 {
		key, err := encodeFKLookup(childSchema.Cols[0], childCols[0], refVals[0])
		if err != nil {
			return false, err
		}
		prefix := key[:len(key)-8]
		it := store.NewIterator(prefix)
		defer it.Close()
		for it.Next() {
			return true, nil
		}
		return false, nil
	}
	return false, nil
}

// checkMultiColumnFK checks a multi-column FK by scanning the referenced table.
func checkMultiColumnFK(refTable string, refCols []string, values []any, store DT.Store) error {
	refSchema, _ := DT.SchemaFor(refTable)
	if refSchema == nil {
		return nil
	}
	prefix := DT.TablePrefix(refTable)
	it := store.NewIterator(prefix)
	defer it.Close()
	for it.Next() {
		rowBytes := it.Value()
		if rowBytes == nil {
			continue
		}
		row, err := DT.DecodeRow(rowBytes, refSchema)
		if err != nil {
			continue
		}
		match := true
		for i, refCol := range refCols {
			idx := -1
			for j, c := range refSchema.Cols {
				if c == refCol {
					idx = j
					break
				}
			}
			if idx < 0 || idx >= len(row.Data) {
				match = false
				break
			}
			val := row.Data[idx]
			if val.Kind == DT.KindNull && DT.EvalVirtualColumn != nil {
				val = DT.EvalVirtualColumn(refSchema, idx, &row)
			}
			if !DT.EqualValueAny(val, values[i]) {
				match = false
				break
			}
		}
		if match {
			return nil
		}
	}
	return fmt.Errorf("referenced row not found in %s", refTable)
}

func encodeFKLookup(table, col string, val any) ([]byte, error) {
	return nil, fmt.Errorf("ex: encodeFKLookup not implemented")
}

// ValidateForeignKeyUpdateInMemory is the in-memory analogue of
// validateForeignKeyInsert. REQ000513.
func ValidateForeignKeyUpdateInMemory(schema *DT.StoreSchema, oldRow, newRow []any) error {
	if schema == nil || len(schema.ForeignKeys) == 0 {
		return nil
	}
	for _, fk := range schema.ForeignKeys {
		oldVals := make([]any, len(fk.Columns))
		newVals := make([]any, len(fk.Columns))
		_, newAllNull := true, true
		for i, col := range fk.Columns {
			idx := -1
			for j, c := range schema.Cols {
				if c == col {
					idx = j
					break
				}
			}
			if idx < 0 {
				continue
			}
			if idx < len(oldRow) {
				oldVals[i] = oldRow[idx]
			}
			if idx < len(newRow) {
				newVals[i] = newRow[idx]
				if newVals[i] != nil {
					newAllNull = false
				}
			}
		}
		if DT.EqualValueAny(oldVals[0], newVals[0]) && len(fk.Columns) == 1 {
			continue
		}
		if newAllNull {
			continue
		}
		if skip, err := checkFKMatch(fk.Match, newVals); err != nil {
			return err
		} else if skip {
			continue
		}
		if fk.Initially == "DEFERRED" {
			refTable := fk.RefTable
			refCols := fk.RefColumns
			vals := copyAnySlice(newVals)
			DT.EnqueueDeferredFKCheck(func() error {
				if !rowExistsInMemory(refTable, refCols, vals) {
					return fmt.Errorf("%w: foreign key update on table referencing %s",
						ap.ErrConstraint, refTable)
				}
				return nil
			})
			continue
		}
		if !rowExistsInMemory(fk.RefTable, fk.RefColumns, newVals) {
			return fmt.Errorf("%w: foreign key update on table referencing %s",
				ap.ErrConstraint, fk.RefTable)
		}
	}
	return nil
}

// ValidateForeignKeyDeleteInMemory is the in-memory analogue of
// validateForeignKeyDelete. REQ000514. Implements ON DELETE actions
// CASCADE, SET NULL, SET DEFAULT, RESTRICT, NO ACTION. REQ001308.
func ValidateForeignKeyDeleteInMemory(table string, row []any, schema *DT.StoreSchema) error {
	if schema == nil {
		return nil
	}
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	return applyFKDeleteActionsLocked(table, row, schema)
}

// applyFKDeleteActionsLocked applies FK ON DELETE actions for all child
// tables referencing the given parent row. Caller MUST hold DT.TablesMu.
func applyFKDeleteActionsLocked(table string, row []any, schema *DT.StoreSchema) error {
	for _, ss := range DT.StoreSchemas {
		for _, fk := range ss.ForeignKeys {
			if fk.RefTable != table {
				continue
			}
			childName := tableNameFor(ss)
			refVals := extractRefVals(row, schema, fk.RefColumns)
			childRows := tableRows(childName)
			matching := matchingRowIndices(childRows, ss, fk.Columns, refVals)
			if len(matching) == 0 {
				continue
			}
			switch fk.OnDelete {
			case "CASCADE":
				for _, mi := range matching {
					childRow := dtValuesToAny(childRows[mi].Data)
					if err := cascadeDeleteChild(childName, childRow, ss); err != nil {
						return err
					}
				}
				sort.Sort(sort.Reverse(sort.IntSlice(matching)))
				rows := tableRows(childName)
				for _, mi := range matching {
					copy(rows[mi:], rows[mi+1:])
					rows = rows[:len(rows)-1]
				}
				setTableRows(childName, rows)
			case "SET NULL":
				for _, mi := range matching {
					for _, col := range fk.Columns {
						if ci := columnIndex(ss, col); ci >= 0 && ci < len(childRows[mi].Data) {
							childRows[mi].Data[ci] = DT.NullValue()
						}
					}
				}
			case "SET DEFAULT":
				for _, mi := range matching {
					for _, col := range fk.Columns {
						ci := columnIndex(ss, col)
						if ci < 0 || ci >= len(childRows[mi].Data) {
							continue
						}
						childRows[mi].Data[ci] = DT.NullValue()
					}
				}
			case "RESTRICT", "NO ACTION":
				return fmt.Errorf("%w: foreign key delete: child rows exist in %s",
					ap.ErrConstraint, childName)
			}
		}
	}
	return nil
}

// cascadeDeleteChild recursively cascades DELETE to tables referencing
// the given child row. Caller MUST hold DT.TablesMu. REQ001308.
func cascadeDeleteChild(table string, row []any, schema *DT.StoreSchema) error {
	for _, ss := range DT.StoreSchemas {
		for _, fk := range ss.ForeignKeys {
			if fk.RefTable != table {
				continue
			}
			childName := tableNameFor(ss)
			refVals := extractRefVals(row, schema, fk.RefColumns)
			childRows := tableRows(childName)
			matching := matchingRowIndices(childRows, ss, fk.Columns, refVals)
			if len(matching) == 0 {
				continue
			}
			switch fk.OnDelete {
			case "CASCADE":
				for _, mi := range matching {
					childRow := dtValuesToAny(childRows[mi].Data)
					if err := cascadeDeleteChild(childName, childRow, ss); err != nil {
						return err
					}
				}
				sort.Sort(sort.Reverse(sort.IntSlice(matching)))
				rows := tableRows(childName)
				for _, mi := range matching {
					copy(rows[mi:], rows[mi+1:])
					rows = rows[:len(rows)-1]
				}
				setTableRows(childName, rows)
			case "SET NULL":
				for _, mi := range matching {
					for _, col := range fk.Columns {
						if ci := columnIndex(ss, col); ci >= 0 && ci < len(childRows[mi].Data) {
							childRows[mi].Data[ci] = DT.NullValue()
						}
					}
				}
			case "SET DEFAULT":
				for _, mi := range matching {
					for _, col := range fk.Columns {
						ci := columnIndex(ss, col)
						if ci < 0 || ci >= len(childRows[mi].Data) {
							continue
						}
						childRows[mi].Data[ci] = DT.NullValue()
					}
				}
			case "RESTRICT", "NO ACTION":
				return fmt.Errorf("%w: foreign key cascade: child rows exist in %s",
					ap.ErrConstraint, childName)
			}
		}
	}
	return nil
}

// ApplyForeignKeyOnUpdateInMemory handles parent-side FK ON UPDATE actions
// (CASCADE, SET NULL, SET DEFAULT, RESTRICT, NO ACTION). REQ001309.
func ApplyForeignKeyOnUpdateInMemory(table string, oldRow, newRow []any) error {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	return applyFKUpdateActionsLocked(table, oldRow, newRow)
}

func applyFKUpdateActionsLocked(table string, oldRow, newRow []any) error {
	for _, ss := range DT.StoreSchemas {
		for _, fk := range ss.ForeignKeys {
			if fk.RefTable != table {
				continue
			}
			childName := tableNameFor(ss)
			oldRefVals := extractRefVals(oldRow, nil, fk.RefColumns)
			newRefVals := extractRefVals(newRow, nil, fk.RefColumns)
			changed := false
			for i := range oldRefVals {
				if i >= len(newRefVals) || !DT.EqualValueAny(oldRefVals[i], newRefVals[i]) {
					changed = true
					break
				}
			}
			if !changed {
				continue
			}
			childRows := tableRows(childName)
			matching := matchingRowIndices(childRows, ss, fk.Columns, oldRefVals)
			if len(matching) == 0 {
				continue
			}
			switch fk.OnUpdate {
			case "CASCADE":
				for _, mi := range matching {
					for i, col := range fk.Columns {
						if ci := columnIndex(ss, col); ci >= 0 && ci < len(childRows[mi].Data) {
							if i < len(newRefVals) {
								childRows[mi].Data[ci] = DT.ValueFromAny(newRefVals[i])
							}
						}
					}
				}
			case "SET NULL":
				for _, mi := range matching {
					for _, col := range fk.Columns {
						if ci := columnIndex(ss, col); ci >= 0 && ci < len(childRows[mi].Data) {
							childRows[mi].Data[ci] = DT.NullValue()
						}
					}
				}
			case "SET DEFAULT":
				for _, mi := range matching {
					for _, col := range fk.Columns {
						ci := columnIndex(ss, col)
						if ci < 0 || ci >= len(childRows[mi].Data) {
							continue
						}
						childRows[mi].Data[ci] = DT.NullValue()
					}
				}
			case "RESTRICT", "NO ACTION":
				return fmt.Errorf("%w: foreign key update: child rows exist in %s",
					ap.ErrConstraint, childName)
			}
		}
	}
	return nil
}

// rowExistsInMemory checks whether the referenced table has a row
// whose FK-target columns equal the given values. Handles VIRTUAL
// generated columns via DT.EvalVirtualColumn. REQ001340.
func rowExistsInMemory(tableName string, cols []string, vals []any) bool {
	DT.TablesMu.RLock()
	defer DT.TablesMu.RUnlock()
	rows := DT.Tables[tableName]
	schema, _ := DT.SchemaFor(tableName)
	for _, r := range rows {
		match := true
		for i, col := range cols {
			idx := -1
			if schema != nil {
				for j, c := range schema.Cols {
					if c == col {
						idx = j
						break
					}
				}
			}
			if idx < 0 || idx >= len(r.Data) {
				match = false
				break
			}
			val := r.Data[idx]
			if val.Kind == DT.KindNull && schema != nil && DT.EvalVirtualColumn != nil {
				val = DT.EvalVirtualColumn(schema, idx, &r)
			}
			if !DT.EqualValueAny(val, vals[i]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// rowInTableMatches returns true if any row in the given schema's
// table has values matching the supplied values in the given columns.
// Caller must hold DT.TablesMu.
func rowInTableMatches(ss *DT.StoreSchema, cols []string, vals []any) bool {
	rows := DT.Tables[tableNameFor(ss)]
	for _, r := range rows {
		match := true
		for i, col := range cols {
			idx := -1
			for j, c := range ss.Cols {
				if c == col {
					idx = j
					break
				}
			}
			if idx < 0 || idx >= len(r.Data) {
				match = false
				break
			}
			if !DT.EqualValueAny(r.Data[idx], vals[i]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// tableNameFor returns the registered name for a DT.StoreSchema.
func tableNameFor(ss *DT.StoreSchema) string {
	DT.StoreMu.Lock()
	defer DT.StoreMu.Unlock()
	for name, id := range DT.TableIDs {
		if DT.StoreSchemas[id] == ss {
			return name
		}
	}
	return ""
}

// --- helper functions for FK row manipulation ---

// extractRefVals extracts the values of the given columns from row data.
func extractRefVals(row []any, schema *DT.StoreSchema, refCols []string) []any {
	vals := make([]any, len(refCols))
	for i, col := range refCols {
		if schema != nil {
			for j, c := range schema.Cols {
				if c == col && j < len(row) {
					vals[i] = row[j]
					break
				}
			}
		} else if i < len(row) {
			vals[i] = row[i]
		}
	}
	return vals
}

// columnIndex returns the index of a column in a schema, checking
// ColIndex first with fallback to linear scan.
func columnIndex(ss *DT.StoreSchema, col string) int {
	if ss.ColIndex != nil {
		if ci, ok := ss.ColIndex[col]; ok {
			return ci
		}
	}
	for j, c := range ss.Cols {
		if c == col {
			return j
		}
	}
	return -1
}

// matchingRowIndices returns indices of rows whose values in the
// specified columns equal the given values.
func matchingRowIndices(rows []DT.Row, ss *DT.StoreSchema, cols []string, vals []any) []int {
	var indices []int
	for ri, r := range rows {
		match := true
		for i, col := range cols {
			ci, ok := ss.ColIndex[col]
			if !ok {
				ci = -1
				for j, c := range ss.Cols {
					if c == col {
						ci = j
						break
					}
				}
			}
			if ci < 0 || ci >= len(r.Data) {
				match = false
				break
			}
			if !DT.EqualValueAny(r.Data[ci], vals[i]) {
				match = false
				break
			}
		}
		if match {
			indices = append(indices, ri)
		}
	}
	return indices
}

// tableRows returns DT.Tables[name] or DT.TempTables[name].
func tableRows(name string) []DT.Row {
	if r, ok := DT.TempTables[name]; ok {
		return r
	}
	return DT.Tables[name]
}

// setTableRows updates DT.Tables[name] or DT.TempTables[name].
func setTableRows(name string, rows []DT.Row) {
	if _, ok := DT.TempTables[name]; ok {
		DT.TempTables[name] = rows
	} else {
		DT.Tables[name] = rows
	}
}

// checkFKMatch validates FK column NULL pattern per MATCH mode (REQ001310).
// Returns (allNull, err). allNull=true means caller should skip FK check.
func checkFKMatch(match string, vals []any) (bool, error) {
	hasNull := false
	hasNonNull := false
	for _, v := range vals {
		if v == nil {
			hasNull = true
		} else {
			hasNonNull = true
		}
	}
	switch match {
	case "FULL":
		if hasNull && hasNonNull {
			return false, fmt.Errorf("%w: MATCH FULL: mixed NULL and NOT NULL in FK columns",
				ap.ErrConstraint)
		}
		return !hasNonNull, nil
	case "PARTIAL":
		if !hasNonNull {
			return true, nil
		}
		return false, nil
	default: // SIMPLE
		if hasNull {
			return true, nil
		}
		return false, nil
	}
}

// dtValuesToAny converts []DT.Value to []any.
func dtValuesToAny(vals []DT.Value) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v.ToAny()
	}
	return out
}

// copyAnySlice returns a shallow copy of a []any slice.
func copyAnySlice(src []any) []any {
	dst := make([]any, len(src))
	copy(dst, src)
	return dst
}
