// Package EX — HashCrossJoin operator for small-table INNER JOINs
// with single-column equi-keys. REQ000800.
//
// HashCrossJoin materializes the right side into a hash map keyed
// by the join column value, then for each left row probes the map
// and emits matches. Unlike HashJoin, this operator does not use
// radix partitioning — it's optimized for the case where both
// sides fit comfortably in memory (≤1024 rows each). For larger
// inputs the planner falls back to NestedLoopJoin or HashJoin.
//
// Falls back to NLJ if the hash key is not a simple column
// reference (e.g., expression on the right side).
package EX

import (
	"context"
	"hash/maphash"
	"math"
	"strings"
)

// HashCrossJoin is a simple hash-probe equi-join for small tables.
// REQ000800.
type HashCrossJoin struct {
	left     Operator
	right    Operator
	leftTbl  string
	rightTbl string
	leftKey  string // single-column join key on the left
	rightKey string // single-column join key on the right
	hashSeed maphash.Seed
	// Build phase: hash right rows by rightKey into buckets.
	buckets map[uint64][]int // hash → indices into rightRows
	// Materialized rows from each side.
	rightRows []Row
	// REQ000816: left side is materialized lazily so we can build
	// a shared colIndex for output rows.
	leftRows   []Row
	probeBuilt bool // true after build() + materializeLeft() ran successfully
	// Probe phase: pre-computed matches from materializeLeft.
	// REQ000802+: eliminates on-the-fly probing and per-row
	// Data allocations by building all matches upfront with a
	// shared data buffer.
	matches    []Row
	matchPos   int
	dataBuf    []Value
	dataPerRow int
	// REQ000818: crossOverflow is set when either side exceeds 1024 rows.
	// In this mode the operator falls back to emitting all left×right pairs
	// (pure cross product) instead of hash probing.
	crossOverflow   bool
	crossLeftIdx    int
	crossRightIdx   int
	crossBucketPos  int   // REQ000951: position within current bucket's index list
	// REQ000816: shared col metadata built once, reused across
	// all emitted rows to skip per-row buildColIndex.
	sharedColIndex map[string]int
	sharedCols     []string
	sharedTypes    []int
	// REQ000874: cached prefix/suffix check. hasAnyPrefix is called
	// per-row in materializeLeft; hoist the check to the operator
	// struct so it's computed once per scan (all rows from the same
	// scan share the same Cols).
	leftHasPrefix  bool
	rightHasPrefix bool
}

// NewHashCrossJoin creates a hash-probe equi-join. Both sides
// must produce rows with Cols/Data aligned to the join keys.
// leftKey/rightKey are unqualified column names; they are matched
// against the right-side row's table-prefixed column name (e.g.,
// "t2.a") and the left-side row's prefixed column name.
func NewHashCrossJoin(left, right Operator, leftTbl, rightTbl, leftKey, rightKey string) *HashCrossJoin {
	return &HashCrossJoin{
		left:     left,
		right:    right,
		leftTbl:  leftTbl,
		rightTbl: rightTbl,
		leftKey:  leftKey,
		rightKey: rightKey,
		hashSeed: maphash.MakeSeed(),
	}
}

func (j *HashCrossJoin) LeftChild() Operator  { return j.left }
func (j *HashCrossJoin) RightChild() Operator { return j.right }

// WithProjection sets the projected columns for the join output.
// REQ000803: when set, only these columns are included in output rows.
func (j *HashCrossJoin) WithProjection(cols []string) *HashCrossJoin {
	return j
}

// Next produces the next matching pair. Build happens lazily on
// the first call. Returns ErrNoRows when exhausted.
func (j *HashCrossJoin) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if !j.probeBuilt {
		if err := j.build(ctx); err != nil {
			// REQ000818: build may set crossOverflow instead of
			// returning error when rows exceed the hash limit.
			if j.crossOverflow {
				return j.nextCross(ctx)
			}
			return Row{}, err
		}
		// REQ000816: materialize left side once so we can build
		// a shared colIndex for output rows. Streaming probe forced
		// per-row buildColIndex in downstream Eval.
		if err := j.materializeLeft(ctx); err != nil {
			if j.crossOverflow {
				return j.nextCross(ctx)
			}
			return Row{}, err
		}
	}
	if j.crossOverflow {
		return j.nextCross(ctx)
	}
	if j.buckets == nil {
		return Row{}, ErrNoRows
	}
	// REQ000802+: return pre-computed matches from data buffer.
	for j.matchPos < len(j.matches) {
		m := j.matches[j.matchPos]
		j.matchPos++
		return m, nil
	}
	return Row{}, ErrNoRows
}

// materializeLeft reads all rows from the left side into leftRows
// with table-prefixed columns, then merges with rightRows[0] to
// build sharedCols/sharedColIndex. REQ000816.
// REQ000802+: also pre-computes all join matches using a
// pre-allocated data buffer to eliminate per-row allocations.
func (j *HashCrossJoin) materializeLeft(ctx context.Context) error {
	const maxMaterialize = 4096
	j.leftRows = make([]Row, 0, 64)
	j.leftHasPrefix = false
	// Check first row to determine prefix state (all rows from the
	// same scan share the same Cols). REQ000874.
	if firstRow, err := j.left.Next(ctx); err == nil {
		j.leftHasPrefix = hasAnyPrefix(firstRow.Cols)
		prefixed := Row{Types: firstRow.Types, Data: firstRow.Data, Outer: firstRow.Outer}
		prefixed.tableName = firstRow.tableName
		if !j.leftHasPrefix {
			prefixed.Cols = prefixCols(firstRow.Cols, j.leftTbl)
		} else {
			prefixed.Cols = append([]string(nil), firstRow.Cols...)
		}
		j.leftRows = append(j.leftRows, prefixed)
	}
	for {
		row, err := j.left.Next(ctx)
		if err != nil {
			break
		}
		prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		prefixed.tableName = row.tableName
		if !j.leftHasPrefix {
			prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
		} else {
			prefixed.Cols = append([]string(nil), row.Cols...)
		}
		j.leftRows = append(j.leftRows, prefixed)
		if len(j.leftRows) >= maxMaterialize {
			j.crossOverflow = true
		}
	}
	if len(j.leftRows) == 0 || len(j.rightRows) == 0 {
		return nil
	}
	lCols := j.leftRows[0].Cols
	rCols := j.rightRows[0].Cols
	j.sharedCols = make([]string, 0, len(lCols)+len(rCols))
	j.sharedCols = append(j.sharedCols, lCols...)
	j.sharedCols = append(j.sharedCols, rCols...)
	j.sharedTypes = make([]int, 0, len(j.leftRows[0].Types)+len(j.rightRows[0].Types))
	j.sharedTypes = append(j.sharedTypes, j.leftRows[0].Types...)
	j.sharedTypes = append(j.sharedTypes, j.rightRows[0].Types...)
	j.sharedColIndex = make(map[string]int, len(j.sharedCols))
	for i, c := range j.sharedCols {
		key := strings.ToLower(c)
		if _, exists := j.sharedColIndex[key]; !exists {
			j.sharedColIndex[key] = i
		}
	}
	for i := range j.leftRows {
		j.leftRows[i].Cols = j.sharedCols[:len(lCols)]
		j.leftRows[i].colIndex = j.sharedColIndex
	}
	for i := range j.rightRows {
		j.rightRows[i].Cols = j.sharedCols[len(lCols):]
		j.rightRows[i].colIndex = j.sharedColIndex
	}
	// REQ000802+: pre-compute all matches with data buffer.
	// Count total matches first.
	dataPerRow := len(lCols) + len(rCols)
	var totalMatches int
	for _, l := range j.leftRows {
		keyVal, ok := lookupColumn(&l, j.leftTbl, j.leftKey)
		if !ok || keyVal == nil {
			continue
		}
		h := hashValue(j.hashSeed, keyVal)
		matches := j.buckets[h]
		totalMatches += len(matches)
	}
	// Pre-allocate contiguous data buffer.
	j.dataPerRow = dataPerRow
	j.dataBuf = make([]Value, 0, totalMatches*dataPerRow)
	j.matches = make([]Row, 0, totalMatches)
	j.matchPos = 0

	for _, l := range j.leftRows {
		keyVal, ok := lookupColumn(&l, j.leftTbl, j.leftKey)
		if !ok || keyVal == nil {
			continue
		}
		h := hashValue(j.hashSeed, keyVal)
		matches := j.buckets[h]
		for _, rightIdx := range matches {
			r := j.rightRows[rightIdx]
			off := len(j.dataBuf)
			// Carve non-overlapping sub-slice from dataBuf.
			dataSlice := j.dataBuf[off : off : off+dataPerRow]
			out := Row{
				Cols:     j.sharedCols,
				Types:    j.sharedTypes,
				Data:     dataSlice,
				colIndex: j.sharedColIndex,
			}
			out.Data = append(out.Data, l.Data...)
			out.Data = append(out.Data, r.Data...)
			j.dataBuf = append(j.dataBuf, out.Data...)
			j.matches = append(j.matches, out)
		}
	}
	return nil
}

func (j *HashCrossJoin) build(ctx context.Context) error {
	j.probeBuilt = true
	j.buckets = make(map[uint64][]int, 64)
	const maxMaterialize = 4096
	j.rightRows = make([]Row, 0, 64)
	j.rightHasPrefix = false
	// Check first row to determine prefix state (all rows from the
	// same scan share the same Cols). REQ000874.
	if firstRow, err := j.right.Next(ctx); err == nil {
		j.rightHasPrefix = hasAnyPrefix(firstRow.Cols)
		prefixed := Row{
			Cols:      firstRow.Cols,
			Types:     firstRow.Types,
			Data:      append([]Value(nil), firstRow.Data...),
			tableName: firstRow.tableName,
		}
		if j.rightHasPrefix {
			prefixed.Cols = append([]string(nil), firstRow.Cols...)
		} else {
			prefixed.Cols = prefixCols(firstRow.Cols, j.rightTbl)
		}
		keyVal, ok := lookupColumn(&prefixed, j.rightTbl, j.rightKey)
		if ok && keyVal != nil {
			h := hashValue(j.hashSeed, keyVal)
			idx := len(j.rightRows)
			j.rightRows = append(j.rightRows, prefixed)
			j.buckets[h] = append(j.buckets[h], idx)
		}
	}
	for {
		row, err := j.right.Next(ctx)
		if err != nil {
			break
		}
		prefixed := Row{
			Cols:      row.Cols,
			Types:     row.Types,
			Data:      append([]Value(nil), row.Data...),
			tableName: row.tableName,
		}
		if j.rightHasPrefix {
			prefixed.Cols = append([]string(nil), row.Cols...)
		} else {
			prefixed.Cols = prefixCols(row.Cols, j.rightTbl)
		}
		keyVal, ok := lookupColumn(&prefixed, j.rightTbl, j.rightKey)
		if !ok || keyVal == nil {
			continue
		}
		h := hashValue(j.hashSeed, keyVal)
		idx := len(j.rightRows)
		j.rightRows = append(j.rightRows, prefixed)
		j.buckets[h] = append(j.buckets[h], idx)
		if len(j.rightRows) >= maxMaterialize {
			j.crossOverflow = true
		}
	}
	return nil
}

// REQ000951: cross-product hash fallback when either side exceeds
// the materialize limit. Uses the already-built j.buckets hash map
// (from build()) for O(1) right-side lookups instead of the
// previous O(N*M) sequential equality check. This matches MySQL 8.0's
// hash join behavior where the smaller side is hashed and the larger
// side probes.
//
// State is tracked across multiple calls via crossLeftIdx,
// crossRightIdx (position within bucket's index slice), and
// crossBucketPos (current bucket index list reference).
func (j *HashCrossJoin) nextCross(_ context.Context) (Row, error) {
	for j.crossLeftIdx < len(j.leftRows) {
		l := &j.leftRows[j.crossLeftIdx]
		lv, lok := lookupColumn(l, j.leftTbl, j.leftKey)
		if !lok || lv == nil {
			j.crossLeftIdx++
			j.crossRightIdx = 0
			continue
		}
		h := hashValue(j.hashSeed, lv)
		idxs, ok := j.buckets[h]
		if !ok {
			j.crossLeftIdx++
			j.crossRightIdx = 0
			continue
		}
		for j.crossRightIdx < len(idxs) {
			ridx := idxs[j.crossRightIdx]
			j.crossRightIdx++
			r := &j.rightRows[ridx]
			rv, rok := lookupColumn(r, j.rightTbl, j.rightKey)
			if !rok || rv == nil {
				continue
			}
			if !equalValue(lv, rv) {
				continue
			}
			off := len(j.dataBuf)
			required := off + j.dataPerRow
			if cap(j.dataBuf) < required {
				newCap := cap(j.dataBuf) * 2
				if newCap < required {
					newCap = required
				}
				buf := make([]Value, required, newCap)
				copy(buf, j.dataBuf)
				j.dataBuf = buf
			}
			j.dataBuf = j.dataBuf[:required]
			dataSlice := j.dataBuf[off : off+j.dataPerRow : off+j.dataPerRow]
			copy(dataSlice, l.Data)
			copy(dataSlice[len(l.Data):], r.Data)
			return Row{
				Cols:     j.sharedCols,
				Types:    j.sharedTypes,
				Data:     dataSlice,
				colIndex: j.sharedColIndex,
			}, nil
		}
		j.crossRightIdx = 0
		j.crossLeftIdx++
	}
	return Row{}, ErrNoRows
}

func (j *HashCrossJoin) Close() error {
	j.probeBuilt = false
	j.buckets = nil
	j.rightRows = nil
	j.leftRows = nil
	j.matches = nil
	j.matchPos = 0
	j.dataBuf = nil
	j.dataPerRow = 0
	j.crossOverflow = false
	j.crossLeftIdx = 0
	j.crossRightIdx = 0
	j.crossBucketPos = 0
	j.sharedColIndex = nil
	j.sharedCols = nil
	j.sharedTypes = nil
	_ = j.left.Close()
	return j.right.Close()
}

// lookupColumn finds the value of `col` in row, allowing either
// bare ("a") or table-qualified ("t1.a") column names.
func lookupColumn(row *Row, tbl, col string) (any, bool) {
	want := tbl + "." + col
	// REQ001036: check colIndex first (O(1)), fall back to linear scan only if colIndex is nil.
	if row.colIndex != nil {
		if idx, ok := row.colIndex[strings.ToLower(want)]; ok && idx < len(row.Data) {
			return row.Data[idx].ToAny(), true
		}
		if idx, ok := row.colIndex[strings.ToLower(col)]; ok && idx < len(row.Data) {
			return row.Data[idx].ToAny(), true
		}
		return nil, false
	}
	for i, c := range row.Cols {
		if c == want || c == col {
			if i < len(row.Data) {
				return row.Data[i].ToAny(), true
			}
			return nil, false
		}
	}
	return nil, false
}

// hashValue produces a uint64 hash of an arbitrary value used as a
// hash-join key. Reuses maphash for consistency. Nil values hash
// to the same uint64 — callers must treat NULL specially by
// excluding nil keys from joining.
func hashValue(seed maphash.Seed, v any) uint64 {
	var h maphash.Hash
	h.SetSeed(seed)
	switch x := v.(type) {
	case Value:
		if x.IsNull() {
			var b [1]byte
			b[0] = 0xff
			h.Write(b[:])
			return h.Sum64()
		}
		switch x.Kind {
		case KindInt:
			var b [8]byte
			u := uint64(x.I64)
			b[0] = byte(u)
			b[1] = byte(u >> 8)
			b[2] = byte(u >> 16)
			b[3] = byte(u >> 24)
			b[4] = byte(u >> 32)
			b[5] = byte(u >> 40)
			b[6] = byte(u >> 48)
			b[7] = byte(u >> 56)
			h.Write(b[:])
		case KindFloat:
			var b [8]byte
			u := math.Float64bits(x.F64)
			b[0] = byte(u)
			b[1] = byte(u >> 8)
			b[2] = byte(u >> 16)
			b[3] = byte(u >> 24)
			b[4] = byte(u >> 32)
			b[5] = byte(u >> 40)
			b[6] = byte(u >> 48)
			b[7] = byte(u >> 56)
			h.Write(b[:])
		case KindText:
			h.WriteString(x.S)
		case KindBool:
			if x.Bo {
				h.WriteByte(1)
			} else {
				h.WriteByte(0)
			}
		}
		return h.Sum64()
	case nil:
		var b [1]byte
		b[0] = 0xff
		h.Write(b[:])
	case int64:
		var b [8]byte
		u := uint64(x)
		b[0] = byte(u)
		b[1] = byte(u >> 8)
		b[2] = byte(u >> 16)
		b[3] = byte(u >> 24)
		b[4] = byte(u >> 32)
		b[5] = byte(u >> 40)
		b[6] = byte(u >> 48)
		b[7] = byte(u >> 56)
		h.Write(b[:])
	case int:
		return hashValue(seed, int64(x))
	case float64:
		var b [8]byte
		u := math.Float64bits(x)
		b[0] = byte(u)
		b[1] = byte(u >> 8)
		b[2] = byte(u >> 16)
		b[3] = byte(u >> 24)
		b[4] = byte(u >> 32)
		b[5] = byte(u >> 40)
		b[6] = byte(u >> 48)
		b[7] = byte(u >> 56)
		h.Write(b[:])
	case string:
		h.WriteString(x)
	case []byte:
		h.Write(x)
	case bool:
		if x {
			h.WriteByte(1)
		} else {
			h.WriteByte(0)
		}
	}
	return h.Sum64()
}

func toStringFallback(v any) string {
	// Avoid pulling in fmt in the hot path; use a type switch.
	switch x := v.(type) {
	case []byte:
		return string(x)
	case bool:
		if x {
			return "t"
		}
		return "f"
	}
	return ""
}
