package EX

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
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
		if row.Data[i].IsNull() {
			v, err := EvalValue(def, nil, nil)
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
			v, err := EvalValue(gen, &row, nil)
			if err != nil {
				return row, fmt.Errorf("%w: generated column %q: %v",
					ErrConstraint, schema.cols[i], err)
			}
			row.Data[i] = v
		}
	}
	return row, nil
}

// coerceDefault coerces v to the Value type matching the column's
// token type. Unrecognised types pass through unchanged. REQ000515.
func coerceDefault(v Value, colType int) Value {
	if v.IsNull() {
		return v
	}
	raw := v.ToAny()
	switch LX.TokenType(colType) {
	case LX.T_INT_KW, LX.T_BIGINT, LX.T_NUMERIC, LX.T_DATE, LX.T_TIME:
		switch n := raw.(type) {
		case int64:
			return NewIntValue(n)
		case int:
			return NewIntValue(int64(n))
		case float64:
			return NewIntValue(int64(n))
		case string:
			return NewIntValue(0)
		case bool:
			if n {
				return NewIntValue(1)
			}
			return NewIntValue(0)
		}
	case LX.T_FLOAT_KW:
		switch n := raw.(type) {
		case float64:
			return NewFloatValue(n)
		case int64:
			return NewFloatValue(float64(n))
		case int:
			return NewFloatValue(float64(n))
		case string:
			return NewFloatValue(0)
		case bool:
			if n {
				return NewFloatValue(1)
			}
			return NewFloatValue(0)
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_TIMESTAMP, LX.T_JSON:
		return NewTextValue(fmt.Sprintf("%v", raw))
	case LX.T_BOOL:
		switch b := raw.(type) {
		case bool:
			return NewBoolValue(b)
		case int64:
			return NewBoolValue(b != 0)
		case int:
			return NewBoolValue(b != 0)
		case float64:
			return NewBoolValue(b != 0)
		case string:
			return NewBoolValue(b == "true" || b == "1" || b == "yes")
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
		if row.Data[i].IsNull() && !schema.nullable[i] {
			// REQ000713: INTEGER PRIMARY KEY allows NULL —
			// SQLite treats it as a rowid alias and auto-assigns.
			if schema.pk == col && isIntegerType(schema.colTypes, i) {
				continue
			}
			// Note: PK implies NOT NULL; primary-key columns always have
			// schema.nullable[i] == false from CREATE TABLE parsing.
			return fmt.Errorf("%w: column %q is NOT NULL", ErrConstraint, col)
		}
	}
	return nil
}

// isIntegerType returns true if the column type at index i is an
// integer type (INTEGER, INT, BIGINT).
func isIntegerType(colTypes []int, i int) bool {
	if i >= len(colTypes) {
		return false
	}
	return colTypes[i] == int(LX.T_INT_KW) || colTypes[i] == int(LX.T_BIGINT)
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
		if v.IsNull() {
			continue
		}
		prec := schema.precision[i]
		sc := schema.scale[i]
		if prec == 0 && sc == 0 {
			continue
		}
		if _, err := FormatDecimal(v.ToAny(), prec, sc); err != nil {
			return fmt.Errorf("%w: column %q: %v", ErrConstraint, schema.cols[i], err)
		}
	}
	return nil
}

// uniqueLookup returns (true, nil) if the (cols, vals) combination
// already exists in another row of the table, (false, nil) if no
// match, or an error. Implementations may scan an in-memory map or an
// LSM key range iterator.
type uniqueLookup func(cols []int, vals []any) (bool, error)

// uniqueLookupWithApply is an extended lookup that exposes the matching
// row so callers can mutate it in place. Implementations are
// responsible for any locking. REQ000511.
type uniqueLookupWithApply interface {
	Lookup(cols []int, vals []any) (bool, error)
	FindAndLock(cols []int, vals []any) (int, bool, error)
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
		val, err := EvalValue(check, &row, nil)
		if err != nil {
			return fmt.Errorf("%w: CHECK constraint %d: %v",
				ErrConstraint, i, err)
		}
		// CHECK must evaluate to TRUE (not FALSE or NULL)
		if !isValueTruthy(val) {
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
// The primary key is implicitly unique — if the table has a PK, it
// is also checked via the same lookup.
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
		vals := make([]any, len(uk.Cols))
		anyNil := false
		for i, idx := range uk.Cols {
			vals[i] = row.Data[idx].ToAny()
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

// encodeUniqueKey produces a deterministic binary key from vals for
// use in the pending set. Unlike CRC32, this encoding is:
//   - collision-free (full value preserved)
//   - type-safe (each value carries a type tag)
//   - canonical (same input always produces same output)
func encodeUniqueKey(cols []int, vals []any) []byte {
	size := 0
	for _, v := range vals {
		switch x := v.(type) {
		case int64:
			size += 9
		case float64:
			size += 9
		case string:
			size += 5 + len(x)
		case bool:
			size += 2
		case []byte:
			size += 5 + len(x)
		default:
			s := fmt.Sprintf("%v", v)
			size += 5 + len(s)
		}
	}
	out := make([]byte, 0, size)
	for _, v := range vals {
		switch x := v.(type) {
		case int64:
			out = append(out, 0)
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(x))
			out = append(out, buf[:]...)
		case float64:
			out = append(out, 1)
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(x))
			out = append(out, buf[:]...)
		case string:
			out = append(out, 2)
			var blen [4]byte
			binary.LittleEndian.PutUint32(blen[:], uint32(len(x)))
			out = append(out, blen[:]...)
			out = append(out, x...)
		case bool:
			out = append(out, 3)
			if x {
				out = append(out, 1)
			} else {
				out = append(out, 0)
			}
		case []byte:
			out = append(out, 4)
			var blen [4]byte
			binary.LittleEndian.PutUint32(blen[:], uint32(len(x)))
			out = append(out, blen[:]...)
			out = append(out, x...)
		default:
			out = append(out, 5)
			s := fmt.Sprintf("%v", v)
			var blen [4]byte
			binary.LittleEndian.PutUint32(blen[:], uint32(len(s)))
			out = append(out, blen[:]...)
			out = append(out, s...)
		}
	}
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

func (m *memLookup) Lookup(cols []int, vals []any) (bool, error) {
	_, ok, err := m.FindAndLock(cols, vals)
	return ok, err
}

// memLookupAdapter adapts a uniqueLookupWithApply back to a
// uniqueLookup for callers (like checkUnique) that only need
// the boolean result. REQ000511.
type memLookupAdapter struct{ inner uniqueLookupWithApply }

func (a memLookupAdapter) lookup(cols []int, vals []any) (bool, error) {
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
func (m *memLookup) FindAndLock(cols []int, vals []any) (int, bool, error) {
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

func rowMatchesUnique(data []Value, cols []int, vals []any) bool {
	for i, idx := range cols {
		if idx >= len(data) {
			return false
		}
		if !valueEqual(data[idx].ToAny(), vals[i]) {
			return false
		}
	}
	return true
}

func valueEqual(a, b any) bool {
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
