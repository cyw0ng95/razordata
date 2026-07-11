package UT

import (
	"context"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// sstVerifier is the optional interface for SST-level integrity checks.
// The store (typically ls.Engine or its adapter) may implement this.
type sstVerifier interface {
	VerifySSTFiles() []string
}

// indexRefVerifier is the optional interface for index reference checks.
// When implemented by the store, IntegrityCheck verifies that every
// secondary index entry references an existing row.
// REQ001382.
type indexRefVerifier interface {
	VerifyIndexReferences() []string
}

// IntegrityCheck is the executor for PRAGMA integrity_check. REQ000261.
// It performs consistency checks on the database and returns any errors found.
// Returns empty result set if database passes all checks.
type IntegrityCheck struct {
	store Store
	done  bool
	pos   int
	rows  []Row
}

// NewIntegrityCheck creates the integrity check operator.
func NewIntegrityCheck() *IntegrityCheck {
	return &IntegrityCheck{}
}

// NewIntegrityCheckWithStore creates integrity check with engine access.
func NewIntegrityCheckWithStore(store Store) *IntegrityCheck {
	return &IntegrityCheck{store: store}
}

func (ic *IntegrityCheck) Next(ctx context.Context) (Row, error) {
	if ic.done {
		if ic.pos < len(ic.rows) {
			row := ic.rows[ic.pos]
			ic.pos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}
	ic.done = true

	// Run integrity checks
	var errors []string

	// Check 1: Catalog integrity
	if cat := DT.Catalog(); cat != nil {
		if err := ic.checkCatalog(cat); err != nil {
			errors = append(errors, err.Error())
		}
	}

	// Check 2: Store integrity (SST file checksums, etc.)
	if ic.store != nil {
		if err := ic.checkStore(); err != nil {
			errors = append(errors, err.Error())
		}
	}

	// Check 3: SST file CRC32 checksums (REQ001380)
	if sstEng, ok := ic.store.(sstVerifier); ok {
		errors = append(errors, sstEng.VerifySSTFiles()...)
	}

	// Check 4: Index reference consistency (REQ001382)
	// Use a type switch to handle both indexRefVerifier and non-verifier stores.
	switch v := ic.store.(type) {
	case indexRefVerifier:
		errors = append(errors, v.VerifyIndexReferences()...)
	}

	// Check 5: FK consistency (REQ001381)
	fkErrors := ic.checkForeignKeyConsistency()
	errors = append(errors, fkErrors...)

	// Convert errors to result rows
	// Format: (table, page, error_message)
	// Empty result = all checks passed
	for _, errMsg := range errors {
		ic.rows = append(ic.rows, Row{
			Cols: []string{"table", "page", "error"},
			Data: []Value{
				DT.NewTextValue(""),     // table (empty = global)
				DT.NewTextValue(""),     // page (empty = not applicable)
				DT.NewTextValue(errMsg), // error message
			},
		})
	}

	// Return the first error row if any, otherwise signal completion.
	if len(ic.rows) > 0 {
		row := ic.rows[0]
		ic.pos = 1
		return row, nil
	}
	return Row{}, ErrNoRows
}

func (ic *IntegrityCheck) Close() error { return nil }

func (ic *IntegrityCheck) RowsAffected() int64 { return int64(len(ic.rows)) }

func (ic *IntegrityCheck) WithParams(p []any) Operator { return ic }

// checkCatalog verifies catalog consistency. REQ000261.
func (ic *IntegrityCheck) checkCatalog(cat any) error {
	// Type assert to *ls.Catalog if possible
	catalog, ok := cat.(*ls.Catalog)
	if !ok {
		return nil
	}

	// Get all DT.Tables and verify they have valid entries
	entries := catalog.List()
	for _, entry := range entries {
		if entry == nil {
			return fmt.Errorf("catalog: nil entry detected")
		}
		if entry.Name == "" {
			return fmt.Errorf("catalog: entry with empty name (id=%d)", entry.TableID)
		}
		if entry.PrimaryKey == "" {
			// Tables without PK are allowed but unusual
			continue
		}
		// Verify column definitions
		for i, col := range entry.Columns {
			if col.Name == "" {
				return fmt.Errorf("catalog: table %q column %d has empty name", entry.Name, i)
			}
		}
	}

	return nil
}

// checkStore verifies storage-level integrity. REQ000261.
func (ic *IntegrityCheck) checkStore() error {
	if ic.store == nil {
		return nil
	}

	// Check 1: Iterate over all keys and verify CRC
	// This is a simplified check - full implementation would verify:
	// - SST file checksums
	// - Manifest consistency
	// - Index-table consistency
	// - Bloomb filter validity

	// For now, just verify we can iterate without errors
	// Full implementation in a future iteration
	prefix := []byte("")
	it := ic.store.NewIterator(prefix)
	defer it.Close()

	count := 0
	for it.Next() {
		count++
		if err := it.Err(); err != nil {
			return fmt.Errorf("store: iterator error at key %q: %v", string(it.Key()), err)
		}
	}
	if err := it.Err(); err != nil {
		return fmt.Errorf("store: iteration failed: %v", err)
	}

	// Check 2: Verify no duplicate keys
	// (handled by store implementation, but we can sanity-check)

	return nil
}

// checkForeignKeyConsistency verifies FK references for all in-memory
// tables. Returns a list of error messages; empty means all pass.
// REQ001381.
func (ic *IntegrityCheck) checkForeignKeyConsistency() []string {
	// Collect table names that have FK constraints.
	type fkEntry struct {
		childName string
		schema    *DT.StoreSchema
	}
	DT.StoreMu.Lock()
	var entries []fkEntry
	for _, ss := range DT.StoreSchemas {
		if len(ss.ForeignKeys) == 0 {
			continue
		}
		childName := ""
		for n, id := range DT.TableIDs {
			if DT.StoreSchemas[id] == ss {
				childName = n
				break
			}
		}
		if childName != "" {
			entries = append(entries, fkEntry{childName: childName, schema: ss})
		}
	}
	DT.StoreMu.Unlock()

	var errs []string
	for _, e := range entries {
		DT.TablesMu.RLock()
		rows := DT.Tables[e.childName]
		DT.TablesMu.RUnlock()
	outer:
		for _, r := range rows {
			for _, fk := range e.schema.ForeignKeys {
				localVals := make([]any, len(fk.Columns))
				allNull := true
				for i, col := range fk.Columns {
					ci, ok := e.schema.ColIndex[col]
					if !ok || ci >= len(r.Data) {
						continue
					}
					localVals[i] = r.Data[ci].ToAny()
					if localVals[i] != nil {
						allNull = false
					}
				}
				if allNull {
					continue
				}
				refSchema, ok := DT.SchemaFor(fk.RefTable)
				if !ok {
					continue
				}
				found := false
				DT.TablesMu.RLock()
				parentRows := DT.Tables[fk.RefTable]
				for _, pr := range parentRows {
					match := true
					for i, refCol := range fk.RefColumns {
						ci, ok := refSchema.ColIndex[refCol]
						if !ok || ci >= len(pr.Data) {
							match = false
							break
						}
						if !DT.EqualValueAny(pr.Data[ci], localVals[i]) {
							match = false
							break
						}
					}
					if match {
						found = true
						break
					}
				}
				DT.TablesMu.RUnlock()
				if !found {
					errs = append(errs, fmt.Sprintf("foreign key check: row in %s missing referenced row in %s",
						e.childName, fk.RefTable))
					break outer
				}
			}
		}
	}
	return errs
}
