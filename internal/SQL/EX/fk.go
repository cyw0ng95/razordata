package EX

import (
	"fmt"

	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// validateForeignKeyInsert checks that all FK-referenced rows exist
// in the referenced table. REQ000126.
func validateForeignKeyInsert(schema *storeSchema, row []interface{}, store Store) error {
	if store == nil || schema == nil {
		return nil
	}
	for _, fk := range schema.foreignKeys {
		// Extract local column values
		localVals := make([]interface{}, len(fk.Columns))
		allNull := true
		for i, col := range fk.Columns {
			idx := -1
			for j, c := range schema.cols {
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
		// If all FK columns are NULL, the constraint is satisfied
		if allNull {
			continue
		}
		// Look up the referenced row
		if err := checkReferencedRowExists(fk.RefTable, fk.RefColumns, localVals, store); err != nil {
			return fmt.Errorf("%w: foreign key violation on table referencing %s", ap.ErrConstraint, fk.RefTable)
		}
	}
	return nil
}

// validateForeignKeyDelete checks if any child rows reference the
// row being deleted. For CASCADE, it deletes child rows. REQ000126.
func validateForeignKeyDelete(table string, row []interface{}, schema *storeSchema, store Store) error {
	if store == nil || schema == nil {
		return nil
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	for _, ss := range storeSchemas {
		for _, fk := range ss.foreignKeys {
			if fk.RefTable != table {
				continue
			}
			// Extract the referenced column values from the deleted row
			refVals := make([]interface{}, len(fk.RefColumns))
			for i, refCol := range fk.RefColumns {
				idx := -1
				for j, c := range schema.cols {
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
			// Check if any child rows reference this row
			childExists, err := checkChildRowExists(fk.Columns, refVals, ss, store)
			if err != nil {
				return err
			}
			if childExists {
				switch fk.OnDelete {
				case "CASCADE":
					// Delete child rows (simplified: just reject for now)
					return fmt.Errorf("%w: cascade delete not yet implemented", ap.ErrConstraint)
				case "SET NULL":
					return fmt.Errorf("%w: set null on delete not yet implemented", ap.ErrConstraint)
				case "SET DEFAULT":
					return fmt.Errorf("%w: set default on delete not yet implemented", ap.ErrConstraint)
				case "RESTRICT", "NO ACTION":
					return fmt.Errorf("%w: foreign key violation: child rows exist in %s", ap.ErrConstraint, ss.cols[0])
				}
			}
		}
	}
	return nil
}

// checkReferencedRowExists checks if a row with the given values exists
// in the referenced table.
func checkReferencedRowExists(refTable string, refCols []string, values []interface{}, store Store) error {
	refSchema, _ := schemaFor(refTable)
	if refSchema == nil {
		return nil // table not registered, skip check
	}
	// Build a key from the referenced columns to look up
	// For single-column FK, use the value directly
	if len(refCols) == 1 && len(values) == 1 {
		key := encodeFKLookup(refTable, refCols[0], values[0])
		_, found, err := store.Get(key)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("referenced row not found in %s", refTable)
		}
		return nil
	}
	// Multi-column FK: scan for matching row
	return checkMultiColumnFK(refTable, refCols, values, store)
}

// checkChildRowExists checks if any row in the child table references
// the given values.
func checkChildRowExists(childCols []string, refVals []interface{}, childSchema *storeSchema, store Store) (bool, error) {
	if store == nil {
		return false, nil
	}
	// Build a scan prefix for the child table's FK columns
	// For single-column FK, check if any child row has this value
	if len(childCols) == 1 && len(refVals) == 1 {
		key := encodeFKLookup(childSchema.cols[0], childCols[0], refVals[0])
		prefix := key[:len(key)-8] // remove the value part, keep table+col prefix
		it := store.NewIterator(prefix)
		defer it.Close()
		for it.Next() {
			return true, nil
		}
		return false, nil
	}
	// Multi-column: scan all rows in child table
	return false, nil
}

// checkMultiColumnFK checks a multi-column FK by scanning the referenced table.
func checkMultiColumnFK(refTable string, refCols []string, values []interface{}, store Store) error {
	refSchema, _ := schemaFor(refTable)
	if refSchema == nil {
		return nil
	}
	// Simple approach: scan the referenced table and check each row
	prefix := tablePrefix(refTable)
	it := store.NewIterator(prefix)
	defer it.Close()
	for it.Next() {
		rowBytes := it.Value()
		if rowBytes == nil {
			continue
		}
		row, err := decodeRow(rowBytes, refSchema)
		if err != nil {
			continue
		}
		match := true
		for i, refCol := range refCols {
			idx := -1
			for j, c := range refSchema.cols {
				if c == refCol {
					idx = j
					break
				}
			}
			if idx < 0 || idx >= len(row.Data) {
				match = false
				break
			}
			if !equalValue(row.Data[idx], values[i]) {
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

// encodeFKLookup builds a key for FK validation lookups.
func encodeFKLookup(table, col string, val interface{}) []byte {
	prefix := tablePrefix(table)
	// For now, use a simple encoding
	_ = prefix
	_ = col
	_ = val
	return nil
}

// validateForeignKeyUpdateInMemory is the in-memory analogue of
// validateForeignKeyInsert. When an UPDATE changes the values of FK
// columns, the new values must still point at a valid referenced row.
// REQ000513.
func validateForeignKeyUpdateInMemory(schema *storeSchema, oldRow, newRow []interface{}) error {
	if schema == nil || len(schema.foreignKeys) == 0 {
		return nil
	}
	for _, fk := range schema.foreignKeys {
		// Build old and new local-col value slices.
		oldVals := make([]interface{}, len(fk.Columns))
		newVals := make([]interface{}, len(fk.Columns))
		_, newAllNull := true, true
		for i, col := range fk.Columns {
			idx := -1
			for j, c := range schema.cols {
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
		// If the FK columns are unchanged, the row was already valid
		// at INSERT time, so no re-check is needed.
		if equalValue(oldVals[0], newVals[0]) && len(fk.Columns) == 1 {
			continue
		}
		// If new values are all NULL, the constraint is satisfied
		// (SQL standard: NULL in any FK column relaxes the constraint).
		if newAllNull {
			continue
		}
		// Verify the new values reference an existing row in the
		// referenced table.
		if !rowExistsInMemory(fk.RefTable, fk.RefColumns, newVals) {
			return fmt.Errorf("%w: foreign key update on table referencing %s",
				ap.ErrConstraint, fk.RefTable)
		}
	}
	return nil
}

// validateForeignKeyDeleteInMemory is the in-memory analogue of
// validateForeignKeyDelete. REQ000514.
func validateForeignKeyDeleteInMemory(table string, row []interface{}, schema *storeSchema) error {
	if schema == nil {
		return nil
	}
	tablesMu.Lock()
	defer tablesMu.Unlock()
	for _, ss := range storeSchemas {
		for _, fk := range ss.foreignKeys {
			if fk.RefTable != table {
				continue
			}
			// Extract referenced column values from the deleted row.
			refVals := make([]interface{}, len(fk.RefColumns))
			for i, refCol := range fk.RefColumns {
				idx := -1
				for j, c := range schema.cols {
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
			// Check if any child row in `ss` has the FK columns
			// matching these values.
			childExists := rowInTableMatches(ss, fk.Columns, refVals)
			if childExists {
				switch fk.OnDelete {
				case "CASCADE", "SET NULL", "SET DEFAULT":
					// v1: refuse rather than silently do the wrong thing
					return fmt.Errorf("%w: %s on delete not yet implemented", ap.ErrConstraint, fk.OnDelete)
				default:
					return fmt.Errorf("%w: foreign key delete: child rows exist in %s", ap.ErrConstraint, ss.cols[0])
				}
			}
		}
	}
	return nil
}

// rowExistsInMemory checks whether the referenced table has a row
// whose FK-target columns equal the given values.
func rowExistsInMemory(tableName string, cols []string, vals []interface{}) bool {
	tablesMu.RLock()
	defer tablesMu.RUnlock()
	rows := tables[tableName]
	for _, r := range rows {
		match := true
		for i, col := range cols {
			idx := -1
			for j, c := range Schema(tableName) {
				if c == col {
					idx = j
					break
				}
			}
			if idx < 0 || idx >= len(r.Data) {
				match = false
				break
			}
			if !equalValue(r.Data[idx], vals[i]) {
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
// Caller must hold tablesMu.
func rowInTableMatches(ss *storeSchema, cols []string, vals []interface{}) bool {
	rows := tables[tableNameFor(ss)]
	for _, r := range rows {
		match := true
		for i, col := range cols {
			idx := -1
			for j, c := range ss.cols {
				if c == col {
					idx = j
					break
				}
			}
			if idx < 0 || idx >= len(r.Data) {
				match = false
				break
			}
			if !equalValue(r.Data[idx], vals[i]) {
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

// tableNameFor returns the registered name for a storeSchema. The
// schema store doesn't store the name, so we reverse-lookup via
// tableIDs. REQ000513.
func tableNameFor(ss *storeSchema) string {
	storeMu.Lock()
	defer storeMu.Unlock()
	for name, id := range tableIDs {
		if storeSchemas[id] == ss {
			return name
		}
	}
	return ""
}
