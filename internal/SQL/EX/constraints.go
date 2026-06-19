package EX

import (
	"fmt"
	"hash/crc32"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// ErrConstraint is the package alias for AP.ErrConstraint. Constraint
// violations (NOT NULL, UNIQUE, DEFAULT evaluation) wrap this sentinel.
var ErrConstraint = ap.ErrConstraint

// fillDefaults replaces nil entries in row.Data with the evaluated
// DEFAULT expression for that column. Columns without a DEFAULT keep
// their nil. The row is returned with the same Data slice length.
// Returns a wrapped ErrConstraint on DEFAULT evaluation failure.
// REQ000515: type coercion applied to match the column's declared type.
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
			// REQ000515: coerce the default value to the column's
			// declared type. Without this, a DEFAULT 1 for a TEXT
			// column stays int64 instead of becoming "1".
			if schema.colTypes != nil && i < len(schema.colTypes) {
				v = coerceDefault(v, schema.colTypes[i])
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

// coerceDefault coerces v to the Go type matching the column's
// token type. Unrecognised types pass through unchanged. REQ000515.
func coerceDefault(v interface{}, colType int) interface{} {
	if v == nil {
		return nil
	}
	switch LX.TokenType(colType) {
	case LX.T_INT_KW, LX.T_BIGINT, LX.T_NUMERIC, LX.T_DATE, LX.T_TIME:
		switch n := v.(type) {
		case int64:
			return n
		case int:
			return int64(n)
		case float64:
			return int64(n)
		case string:
			return int64(0)
		case bool:
			if n {
				return int64(1)
			}
			return int64(0)
		}
	case LX.T_FLOAT_KW:
		switch n := v.(type) {
		case float64:
			return n
		case int64:
			return float64(n)
		case int:
			return float64(n)
		case string:
			return float64(0)
		case bool:
			if n {
				return float64(1)
			}
			return float64(0)
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_TIMESTAMP, LX.T_JSON:
		return fmt.Sprintf("%v", v)
	case LX.T_BOOL:
		switch b := v.(type) {
		case bool:
			return b
		case int64:
			return b != 0
		case int:
			return b != 0
		case float64:
			return b != 0
		case string:
			return b == "true" || b == "1" || b == "yes"
		}
	}
	return v
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

// validateDecimal checks that values in DECIMAL/NUMERIC columns respect
// the column's precision and scale. REQ000568.
func validateDecimal(schema *storeSchema, row Row) error {
	if schema.precision == nil || schema.scale == nil {
		return nil
	}
	for i, typ := range schema.colTypes {
		if typ != int(LX.T_DECIMAL) && typ != int(LX.T_NUMERIC) {
			continue
		}
		v := row.Data[i]
		if v == nil {
			continue
		}
		prec := schema.precision[i]
		sc := schema.scale[i]
		if prec == 0 && sc == 0 {
			continue
		}
		if _, err := FormatDecimal(v, prec, sc); err != nil {
			return fmt.Errorf("%w: column %q: %v", ErrConstraint, schema.cols[i], err)
		}
	}
	return nil
}

// uniqueLookup returns (true, nil) if the (cols, vals) combination
// already exists in another row of the table, (false, nil) if no
// match, or an error. Implementations may scan an in-memory map or an
// LSM key range iterator.
type uniqueLookup func(cols []int, vals []interface{}) (bool, error)

// uniqueLookupWithApply is an extended lookup that exposes the matching
// row so callers can mutate it in place. Implementations are
// responsible for any locking. REQ000511.
type uniqueLookupWithApply interface {
	Lookup(cols []int, vals []interface{}) (bool, error)
	FindAndLock(cols []int, vals []interface{}) (int, bool, error)
	Mutate(idx int, fn func(Row) Row) error
}

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
// snapshot is the pre-update row for UPDATE (nil for INSERT). When
// non-nil, each unique key's old value is compared: if the old value
// equals the new value, the check is skipped (no-op self-match) so
// `UPDATE t SET a = a` does not self-conflict. REQ000516.
func checkUnique(schema *storeSchema, row Row, pending map[string]struct{}, snapshot Row, lookup uniqueLookup) error {
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
		// Self-match for UPDATE no-ops: if the old value (from
		// snapshot) equals the new value for every column in this
		// unique key, skip the lookup.
		if len(snapshot.Data) > 0 {
			same := true
			for i, idx := range uk.Cols {
				if idx >= len(snapshot.Data) || !equalValue(snapshot.Data[idx], vals[i]) {
					same = false
					break
				}
			}
			if same {
				continue
			}
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
func inMemoryLookup(tableName string) uniqueLookupWithApply {
	return &memLookup{table: tableName}
}

// memLookup is the in-memory unique-lookup implementation that
// supports the apply interface for ON CONFLICT DO UPDATE (REQ000511).
type memLookup struct {
	table string
}

func (m *memLookup) Lookup(cols []int, vals []interface{}) (bool, error) {
	_, ok, err := m.FindAndLock(cols, vals)
	return ok, err
}

// memLookupAdapter adapts a uniqueLookupWithApply back to a
// uniqueLookup for callers (like checkUnique) that only need
// the boolean result. REQ000511.
type memLookupAdapter struct{ inner uniqueLookupWithApply }

func (a memLookupAdapter) lookup(cols []int, vals []interface{}) (bool, error) {
	return a.inner.Lookup(cols, vals)
}

// asUniqueLookup downgrades a uniqueLookupWithApply to a
// uniqueLookup for callers that don't need the apply path. REQ000511.
func asUniqueLookup(apply uniqueLookupWithApply) uniqueLookup {
	if apply == nil {
		return nil
	}
	return apply.Lookup
}

// FindAndLock returns the index of the first matching row in the
// in-memory table, or -1 if none. Callers MUST already hold
// tablesMu (typically because they're inside Insert.Next /
// checkUnique which are called under tablesMu).
func (m *memLookup) FindAndLock(cols []int, vals []interface{}) (int, bool, error) {
	rows := tables[m.table]
	for i, existing := range rows {
		if rowMatchesUnique(existing.Data, cols, vals) {
			return i, true, nil
		}
	}
	return -1, false, nil
}

// Mutate replaces the row at idx using fn. Callers MUST already
// hold tablesMu.
func (m *memLookup) Mutate(idx int, fn func(Row) Row) error {
	rows := tables[m.table]
	if idx < 0 || idx >= len(rows) {
		return fmt.Errorf("ex: mutate out of range %d", idx)
	}
	rows[idx] = fn(rows[idx])
	tables[m.table] = rows
	return nil
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

// removeConflicting removes rows from existing that conflict with out
// on any unique key (including implicit PK). Returns the filtered slice.
func removeConflicting(existing []Row, schema *storeSchema, out Row) []Row {
	keys := schema.unique
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
	filtered := make([]Row, 0, len(existing))
	for _, row := range existing {
		conflict := false
		for _, uk := range keys {
			match := true
			for _, idx := range uk.Cols {
				if idx >= len(row.Data) || idx >= len(out.Data) {
					match = false
					break
				}
				if !equalValue(row.Data[idx], out.Data[idx]) {
					match = false
					break
				}
			}
			if match {
				conflict = true
				break
			}
		}
		if !conflict {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

// removeConflictingInMemory removes rows from existing that match out on
// the PK column (in-memory path without storeSchema). Returns filtered slice.
// pkName is the table's primary key column name (empty = no PK conflict detection).
func removeConflictingInMemory(existing []Row, schema []string, pkName string, out Row) []Row {
	if pkName == "" {
		return existing
	}
	pkIdx := -1
	for i, c := range schema {
		if c == pkName {
			pkIdx = i
			break
		}
	}
	if pkIdx < 0 || pkIdx >= len(out.Data) {
		return existing
	}
	filtered := make([]Row, 0, len(existing))
	for _, row := range existing {
		if pkIdx < len(row.Data) && equalValue(row.Data[pkIdx], out.Data[pkIdx]) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}
