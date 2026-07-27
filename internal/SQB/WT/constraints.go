package WT

import (
	"encoding/binary"
	"fmt"
	"math"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
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
func FillDefaults(schema *DT.StoreSchema, row DT.Row) (DT.Row, error) {
	if schema.Defaults == nil {
		return row, nil
	}
	for i, def := range schema.Defaults {
		if def == nil {
			continue
		}
		if row.Data[i].IsNull() {
			v, err := EV.EvalValue(def, nil, nil)
			if err != nil {
				return row, fmt.Errorf("%w: default for column %q: %v",
					ErrConstraint, schema.Cols[i], err)
			}
			// REQ000515: coerce the default value to the column's
			// declared type. Without this, a DEFAULT 1 for a TEXT
			// column stays int64 instead of becoming "1".
			if schema.ColTypes != nil && i < len(schema.ColTypes) {
				v = coerceDefault(v, schema.ColTypes[i])
			}
			row.Data[i] = v
		}
	}
	// REQ000249: materialize STORED generated columns. The
	// expression is evaluated against the row so it can reference
	// any earlier column. Virtual columns are skipped (deferred).
	if schema.Generated != nil {
		for i, gen := range schema.Generated {
			if gen == nil {
				continue
			}
			v, err := EV.EvalValue(gen, &row, nil)
			if err != nil {
				return row, fmt.Errorf("%w: generated column %q: %v",
					ErrConstraint, schema.Cols[i], err)
			}
			row.Data[i] = v
		}
	}
	return row, nil
}

// coerceDefault coerces v to the Value type matching the column's
// token type. Unrecognised types pass through unchanged. REQ000515.
func coerceDefault(v DT.Value, colType LX.TokenType) DT.Value {
	if v.IsNull() {
		return v
	}
	raw := v.ToAny()
	switch LX.TokenType(colType) {
	case LX.T_INT_KW, LX.T_BIGINT, LX.T_NUMERIC, LX.T_DATE, LX.T_TIME:
		switch n := raw.(type) {
		case int64:
			return DT.NewIntValue(n)
		case int:
			return DT.NewIntValue(int64(n))
		case float64:
			return DT.NewIntValue(int64(n))
		case string:
			return DT.NewIntValue(0)
		case bool:
			if n {
				return DT.NewIntValue(1)
			}
			return DT.NewIntValue(0)
		}
	case LX.T_FLOAT_KW:
		switch n := raw.(type) {
		case float64:
			return DT.NewFloatValue(n)
		case int64:
			return DT.NewFloatValue(float64(n))
		case int:
			return DT.NewFloatValue(float64(n))
		case string:
			return DT.NewFloatValue(0)
		case bool:
			if n {
				return DT.NewFloatValue(1)
			}
			return DT.NewFloatValue(0)
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_TIMESTAMP, LX.T_JSON:
		return DT.NewTextValue(fmt.Sprintf("%v", raw))
	case LX.T_BOOL:
		switch b := raw.(type) {
		case bool:
			return DT.NewBoolValue(b)
		case int64:
			return DT.NewBoolValue(b != 0)
		case int:
			return DT.NewBoolValue(b != 0)
		case float64:
			return DT.NewBoolValue(b != 0)
		case string:
			return DT.NewBoolValue(b == "true" || b == "1" || b == "yes")
		}
	}
	return v
}

// validateRow checks that every non-nullable column has a non-nil value
// in row.Data. Columns with a DEFAULT are allowed to be nil at this
// stage (fillDefaults runs first). Returns a wrapped ErrConstraint on
// violation.
func ValidateRow(schema *DT.StoreSchema, row DT.Row) error {
	for i, col := range schema.Cols {
		if row.Data[i].IsNull() && !schema.Nullable[i] {
			// REQ000713: INTEGER PRIMARY KEY allows NULL —
			// SQLite treats it as a rowid alias and auto-assigns.
			if schema.Pk == col && isIntegerType(schema.ColTypes, i) {
				continue
			}
			// Note: PK implies NOT NULL; primary-key columns always have
			// schema.Nullable[i] == false from CREATE TABLE parsing.
			return fmt.Errorf("%w: column %q is NOT NULL", ErrConstraint, col)
		}
		// REQ001369: STRICT table-type enforcement — values must
		// match the declared column affinity (NULL is allowed for
		// nullable columns).
		if schema.Strict && !row.Data[i].IsNull() {
			if !kindMatchesAffinity(row.Data[i].Kind, schema.ColTypes, i) {
				return fmt.Errorf("%w: column %q: STRICT type mismatch (got %s, expected %s)",
					ErrConstraint, col, row.Data[i].Kind, affinityName(schema.ColTypes, i))
			}
		}
	}
	return nil
}

// kindMatchesAffinity returns true when the value Kind satisfies the
// declared column affinity. REQ001369.
func kindMatchesAffinity(k DT.ValueKind, colTypes []LX.TokenType, i int) bool {
	if i >= len(colTypes) {
		return true // no declared type — accept any
	}
	aff := sqliteAffinity(colTypes[i])
	switch aff {
	case "INT":
		return k == DT.KindInt
	case "REAL":
		return k == DT.KindInt || k == DT.KindFloat
	case "TEXT":
		return k == DT.KindText
	case "BLOB":
		return k == DT.KindBlob
	case "ANY":
		return true
	default:
		return true // unknown affinity — accept
	}
}

// affinityName returns the affinity name for error reporting.
func affinityName(colTypes []LX.TokenType, i int) string {
	if i >= len(colTypes) {
		return "ANY"
	}
	return sqliteAffinity(colTypes[i])
}

// sqliteAffinity computes SQLite's 5-affinity classification.
func sqliteAffinity(t LX.TokenType) string {
	switch t {
	case LX.T_INT_KW, LX.T_BIGINT:
		return "INT"
	case LX.T_FLOAT_KW, LX.T_DECIMAL, LX.T_NUMERIC:
		return "REAL"
	case LX.T_TEXT, LX.T_VARCHAR:
		return "TEXT"
	case LX.T_BLOB:
		return "BLOB"
	default:
		return "ANY"
	}
}

// isIntegerType returns true if the column type at index i is an
// integer type (INTEGER, INT, BIGINT).
func isIntegerType(colTypes []LX.TokenType, i int) bool {
	if i >= len(colTypes) {
		return false
	}
	return colTypes[i] == LX.T_INT_KW || colTypes[i] == LX.T_BIGINT
}

// validateDecimal checks that values in DECIMAL/NUMERIC columns respect
// the column's precision and scale. REQ000568.
func ValidateDecimal(schema *DT.StoreSchema, row DT.Row) error {
	if schema.Precision == nil || schema.Scale == nil {
		return nil
	}
	for i, typ := range schema.ColTypes {
		if typ != LX.T_DECIMAL && typ != LX.T_NUMERIC {
			continue
		}
		v := row.Data[i]
		if v.IsNull() {
			continue
		}
		prec := schema.Precision[i]
		sc := schema.Scale[i]
		if prec == 0 && sc == 0 {
			continue
		}
		if _, err := UT.FormatDecimal(v.ToAny(), prec, sc); err != nil {
			return fmt.Errorf("%w: column %q: %v", ErrConstraint, schema.Cols[i], err)
		}
	}
	return nil
}

// UniqueLookup returns (true, nil) if the (cols, vals) combination
// already exists in another row of the table, (false, nil) if no
// match, or an error. Implementations may scan an in-memory map or an
// LSM key range iterator.
type UniqueLookup func(cols []int, vals []any) (bool, error)

// UniqueLookupWithApply is an extended lookup that exposes the matching
// row so callers can mutate it in place. Implementations are
// responsible for any locking. REQ000511.
type UniqueLookupWithApply interface {
	Lookup(cols []int, vals []any) (bool, error)
	FindAndLock(cols []int, vals []any) (int, bool, error)
	Mutate(idx int, fn func(DT.Row) DT.Row) error
}

// validateCheck checks that row satisfies all CHECK constraints
// defined on the table. Returns a wrapped ErrConstraint on violation.
// REQ000986: CHECK expressions are pre-compiled on first use and
// cached in schema.CompiledChecks to avoid per-row AST re-evaluation.
func ValidateCheck(schema *DT.StoreSchema, row DT.Row) error {
	// Lazy-compile CHECK expressions on first call.
	if schema.CompiledChecks == nil && len(schema.Checks) > 0 {
		schema.CompiledChecks = make([]func(*DT.Row) (bool, error), len(schema.Checks))
		for i, check := range schema.Checks {
			if check == nil {
				continue
			}
			i2, c2 := i, check
			schema.CompiledChecks[i2] = func(r *DT.Row) (bool, error) {
				val, err := EV.EvalValue(c2, r, nil)
				if err != nil {
					return false, fmt.Errorf("%w: CHECK constraint %d: %v",
						ErrConstraint, i2, err)
				}
				return DT.IsValueTruthy(val), nil
			}
		}
	}
	if schema.CompiledChecks != nil {
		for i, fn := range schema.CompiledChecks {
			if fn == nil {
				continue
			}
			ok, err := fn(&row)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: CHECK constraint %d failed",
					ErrConstraint, i)
			}
		}
		return nil
	}
	// Fallback: evaluate from AST (no caching possible).
	for i, check := range schema.Checks {
		if check == nil {
			continue
		}
		val, err := EV.EvalValue(check, &row, nil)
		if err != nil {
			return fmt.Errorf("%w: CHECK constraint %d: %v",
				ErrConstraint, i, err)
		}
		if !DT.IsValueTruthy(val) {
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
//
// REQ002100 (attempted, rolled back): sync.Pool of []any / []byte
// scratch buffers showed 0% improvement on BenchmarkRazordata_Update
// because each Put/Get boxes the slice into `any`, which itself
// allocates the eface header on the heap — for small slices (~32B)
// the pool overhead cancels the savings. The encodeUniqueKeyInto
// helper is kept for future per-call reuse paths that do not go
// through sync.Pool.
func CheckUnique(schema *DT.StoreSchema, row DT.Row, pending map[string]struct{}, snapshot DT.Row, lookup UniqueLookup) error {
	if lookup == nil {
		return nil
	}
	keys := schema.Unique
	// Implicit UNIQUE on the PK: add a synthetic UniqueKey.
	if schema.Pk != "" {
		pkIdx := -1
		for i, c := range schema.Cols {
			if c == schema.Pk {
				pkIdx = i
				break
			}
		}
		if pkIdx >= 0 {
			keys = append([]DT.UniqueKey(nil), keys...)
			keys = append(keys, DT.UniqueKey{Cols: []int{pkIdx}})
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
		// REQ002100 (round 2): only encode the pending key when the
		// pending map is non-nil. The bench UPDATE hot path passes
		// pending=nil (writers_dml.go:1080) and only uses the
		// lookup(uk.Cols, vals) result below; the previous code
		// always allocated a []byte via EncodeUniqueKey + copied it
		// to a string for an unused keyStr, costing ~31 MB flat
		// alloc_space in BenchmarkRazordata_Update.
		var keyStr string
		if pending != nil {
			key := EncodeUniqueKey(uk.Cols, vals)
			keyStr = string(key)
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
				if idx >= len(snapshot.Data) || !DT.EqualValueAny(snapshot.Data[idx], vals[i]) {
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
				colNames[i] = schema.Cols[idx]
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
func EncodeUniqueKey(cols []int, vals []any) []byte {
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

// encodeUniqueKeyInto writes the encoded unique-key bytes into dst
// starting at offset 0 and returns the new slice. Used by CheckUnique
// to avoid per-row make([]byte,0,size) allocations on the unique-constraint
// hot path; the returned slice may share storage with dst. REQ002100.
func encodeUniqueKeyInto(dst []byte, cols []int, vals []any) []byte {
	out := dst[:0]
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

// inMemoryLookup returns a UniqueLookup that scans the in-memory
// DT.Tables map for matching values. Caller MUST hold tablesMu
// (write or read); the lookup does not take the lock itself.
func InMemoryLookup(tableName string) UniqueLookupWithApply {
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

// memLookupAdapter adapts a UniqueLookupWithApply back to a
// UniqueLookup for callers (like checkUnique) that only need
// the boolean result. REQ000511.
type memLookupAdapter struct{ inner UniqueLookupWithApply }

func (a memLookupAdapter) lookup(cols []int, vals []any) (bool, error) {
	return a.inner.Lookup(cols, vals)
}

// asUniqueLookup downgrades a UniqueLookupWithApply to a
// UniqueLookup for callers that don't need the apply path. REQ000511.
func AsUniqueLookup(apply UniqueLookupWithApply) UniqueLookup {
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
	rows := DT.Tables[m.table]
	for i, existing := range rows {
		if rowMatchesUnique(existing.Data, cols, vals) {
			return i, true, nil
		}
	}
	return -1, false, nil
}

// Mutate replaces the row at idx using fn. Callers MUST already
// hold tablesMu.
func (m *memLookup) Mutate(idx int, fn func(DT.Row) DT.Row) error {
	rows := DT.Tables[m.table]
	if idx < 0 || idx >= len(rows) {
		return fmt.Errorf("ex: mutate out of range %d", idx)
	}
	rows[idx] = fn(rows[idx])
	DT.Tables[m.table] = rows
	return nil
}

func rowMatchesUnique(data []DT.Value, cols []int, vals []any) bool {
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
func RemoveConflicting(existing []DT.Row, schema *DT.StoreSchema, out DT.Row) ([]DT.Row, int) {
	keys := schema.Unique
	if schema.Pk != "" {
		pkIdx := -1
		for i, c := range schema.Cols {
			if c == schema.Pk {
				pkIdx = i
				break
			}
		}
		if pkIdx >= 0 {
			keys = append([]DT.UniqueKey(nil), keys...)
			keys = append(keys, DT.UniqueKey{Cols: []int{pkIdx}})
		}
	}
	filtered := make([]DT.Row, 0, len(existing))
	removed := 0
	for _, row := range existing {
		conflict := false
		for _, uk := range keys {
			match := true
			for _, idx := range uk.Cols {
				if idx >= len(row.Data) || idx >= len(out.Data) {
					match = false
					break
				}
				if !DT.EqualValueAny(row.Data[idx], out.Data[idx]) {
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
		} else {
			removed++
		}
	}
	return filtered, removed
}

// removeConflictingInMemory removes rows from existing that match out on
// the PK column (in-memory path without StoreSchema). Returns filtered slice
// and count of removed rows. pkName is the table's primary key column name
// (empty = no PK conflict detection).
func RemoveConflictingInMemory(existing []DT.Row, schema []string, pkName string, out DT.Row) ([]DT.Row, int) {
	if pkName == "" {
		return existing, 0
	}
	pkIdx := -1
	for i, c := range schema {
		if c == pkName {
			pkIdx = i
			break
		}
	}
	if pkIdx < 0 || pkIdx >= len(out.Data) {
		return existing, 0
	}
	filtered := make([]DT.Row, 0, len(existing))
	removed := 0
	for _, row := range existing {
		if pkIdx < len(row.Data) && DT.EqualValueAny(row.Data[pkIdx], out.Data[pkIdx]) {
			removed++
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered, removed
}
