package EX

import (
	"fmt"
	"hash/crc32"

	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// ErrConstraint is the package alias for AP.ErrConstraint. Constraint
// violations (NOT NULL, UNIQUE, DEFAULT evaluation) wrap this sentinel.
var ErrConstraint = ap.ErrConstraint

// fillDefaults replaces nil entries in row.Data with the evaluated
// DEFAULT expression for that column. Columns without a DEFAULT keep
// their nil. The row is returned with the same Data slice length.
// Returns a wrapped ErrConstraint on DEFAULT evaluation failure.
func fillDefaults(schema *storeSchema, row Row) (Row, error) {
	if schema.defaults == nil {
		return row, nil
	}
	for i, def := range schema.defaults {
		if def == nil {
			continue
		}
		if row.Data[i] == nil {
			v, err := Eval(def, nil, nil)
			if err != nil {
				return row, fmt.Errorf("%w: default for column %q: %v",
					ErrConstraint, schema.cols[i], err)
			}
			row.Data[i] = v
		}
	}
	// REQ000249: materialize STORED generated columns. The
	// expression is evaluated against the row so it can reference
	// any earlier column. Virtual columns are skipped (deferred).
	if schema.generated != nil {
		for i, gen := range schema.generated {
			if gen == nil {
				continue
			}
			v, err := Eval(gen, &row, nil)
			if err != nil {
				return row, fmt.Errorf("%w: generated column %q: %v",
					ErrConstraint, schema.cols[i], err)
			}
			row.Data[i] = v
		}
	}
	return row, nil
}

// validateRow checks that every non-nullable column has a non-nil value
// in row.Data. Columns with a DEFAULT are allowed to be nil at this
// stage (fillDefaults runs first). Returns a wrapped ErrConstraint on
// violation.
func validateRow(schema *storeSchema, row Row) error {
	for i, col := range schema.cols {
		if row.Data[i] == nil && !schema.nullable[i] {
			// Note: PK implies NOT NULL; primary-key columns always have
			// schema.nullable[i] == false from CREATE TABLE parsing.
			return fmt.Errorf("%w: column %q is NOT NULL", ErrConstraint, col)
		}
	}
	return nil
}

// uniqueLookup returns (true, nil) if the (cols, vals) combination
// already exists in another row of the table, (false, nil) if no
// match, or an error. Implementations may scan an in-memory map or an
// LSM key range iterator.
type uniqueLookup func(cols []int, vals []interface{}) (bool, error)

// validateCheck checks that row satisfies all CHECK constraints
// defined on the table. Returns a wrapped ErrConstraint on violation.
func validateCheck(schema *storeSchema, row Row) error {
	for i, check := range schema.checks {
		if check == nil {
			continue
		}
		// Evaluate the CHECK expression against the row
		val, err := Eval(check, &row, nil)
		if err != nil {
			return fmt.Errorf("%w: CHECK constraint %d: %v",
				ErrConstraint, i, err)
		}
		// CHECK must evaluate to TRUE (not FALSE or NULL)
		if !truthy(val) {
			return fmt.Errorf("%w: CHECK constraint %d failed",
				ErrConstraint, i)
		}
	}
	return nil
}

// checkUnique verifies that row's values for each UNIQUE key do not
// collide with existing rows. The pending set carries encoded unique
// keys from earlier rows in the same statement (multi-row INSERT
// support). Returns a wrapped ErrConstraint on duplicate.
//
// The primary key is implicitly unique — if the table has a PK, it
// is also checked via the same lookup.
//
// selfKey is the encoded unique key of the row being updated (only
// relevant for UPDATE; pass nil for INSERT). When non-nil, an exact
// match against selfKey is treated as a no-op (no violation) so
// `UPDATE t SET a = a` does not self-conflict.
func checkUnique(schema *storeSchema, row Row, pending map[string]struct{}, selfKey []byte, lookup uniqueLookup) error {
	if lookup == nil {
		return nil
	}
	keys := schema.unique
	// Implicit UNIQUE on the PK: add a synthetic UniqueKey.
	if schema.pk != "" {
		pkIdx := -1
		for i, c := range schema.cols {
			if c == schema.pk {
				pkIdx = i
				break
			}
		}
		if pkIdx >= 0 {
			keys = append([]UniqueKey(nil), keys...)
			keys = append(keys, UniqueKey{Cols: []int{pkIdx}})
		}
	}
	for _, uk := range keys {
		vals := make([]interface{}, len(uk.Cols))
		anyNil := false
		for i, idx := range uk.Cols {
			vals[i] = row.Data[idx]
			if vals[i] == nil {
				anyNil = true
				break
			}
		}
		// NULL semantics: skip columns with NULL — SQL standard allows
		// multiple NULLs in a UNIQUE column. v1 behavior: skip the
		// check entirely for this key (consistent with PG/SQLite).
		if anyNil {
			continue
		}
		key := encodeUniqueKey(uk.Cols, vals)
		keyStr := string(key)
		// Pending-batch check.
		if pending != nil {
			if _, dup := pending[keyStr]; dup {
				return fmt.Errorf("%w: duplicate of (%v) within statement", ErrConstraint, vals)
			}
		}
		// Self-match for UPDATE no-ops.
		if selfKey != nil && string(selfKey) == keyStr {
			continue
		}
		exists, err := lookup(uk.Cols, vals)
		if err != nil {
			return err
		}
		if exists {
			colNames := make([]string, len(uk.Cols))
			for i, idx := range uk.Cols {
				colNames[i] = schema.cols[idx]
			}
			return fmt.Errorf("%w: duplicate of (%v) on column(s) %v", ErrConstraint, vals, colNames)
		}
		// Track this key in the pending set if provided.
		if pending != nil {
			pending[keyStr] = struct{}{}
		}
	}
	return nil
}

// encodeUniqueKey produces a stable byte key from (cols, vals) for
// use in the pending set or composite comparison. Columns are
// CRC32-hashed individually (8 bytes each), then concatenated. Order
// matters — (a, b) and (b, a) are different keys.
func encodeUniqueKey(cols []int, vals []interface{}) []byte {
	h := crc32.NewIEEE()
	for _, v := range vals {
		switch x := v.(type) {
		case int64:
			var buf [8]byte
			for i := 0; i < 8; i++ {
				buf[i] = byte(x >> (8 * i))
			}
			h.Write(buf[:])
		case float64:
			bits := fmt.Sprintf("%f", x)
			h.Write([]byte(bits))
		case string:
			h.Write([]byte(x))
		case bool:
			if x {
				h.Write([]byte{1})
			} else {
				h.Write([]byte{0})
			}
		case []byte:
			h.Write(x)
		default:
			h.Write([]byte(fmt.Sprintf("%v", v)))
		}
	}
	sum := h.Sum32()
	out := make([]byte, 4)
	out[0] = byte(sum)
	out[1] = byte(sum >> 8)
	out[2] = byte(sum >> 16)
	out[3] = byte(sum >> 24)
	return out
}

// inMemoryLookup returns a uniqueLookup that scans the in-memory
// tables map for matching values. Caller MUST hold tablesMu
// (write or read); the lookup does not take the lock itself.
func inMemoryLookup(tableName string) uniqueLookup {
	return func(cols []int, vals []interface{}) (bool, error) {
		for _, existing := range tables[tableName] {
			if rowMatchesUnique(existing.Data, cols, vals) {
				return true, nil
			}
		}
		return false, nil
	}
}

func rowMatchesUnique(data []interface{}, cols []int, vals []interface{}) bool {
	for i, idx := range cols {
		if idx >= len(data) {
			return false
		}
		if !valueEqual(data[idx], vals[i]) {
			return false
		}
	}
	return true
}

func valueEqual(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch ax := a.(type) {
	case int64:
		if bx, ok := b.(int64); ok {
			return ax == bx
		}
	case string:
		if bx, ok := b.(string); ok {
			return ax == bx
		}
	case bool:
		if bx, ok := b.(bool); ok {
			return ax == bx
		}
	case float64:
		if bx, ok := b.(float64); ok {
			return ax == bx
		}
	case []byte:
		if bx, ok := b.([]byte); ok {
			return string(ax) == string(bx)
		}
	}
	return false
}
