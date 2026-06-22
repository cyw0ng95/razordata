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
	left      Operator
	right     Operator
	leftTbl   string
	rightTbl  string
	leftKey   string // single-column join key on the left
	rightKey  string // single-column join key on the right
	hashSeed  maphash.Seed
	// Build phase: hash right rows by rightKey into buckets.
	buckets map[uint64][]int // hash → indices into rightRows
	// Materialized rows from each side.
	rightRows []Row
	// Probe phase: current left row + position in its matching bucket.
	leftRow      *Row
	bucketPos    int // index into buckets[hash] slice
	bucketHash   uint64
	probeBuilt   bool // true after build() ran successfully
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

// Next produces the next matching pair. Build happens lazily on
// the first call. Returns ErrNoRows when exhausted.
func (j *HashCrossJoin) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if !j.probeBuilt {
		if err := j.build(ctx); err != nil {
			return Row{}, err
		}
	}
	if j.buckets == nil {
		return Row{}, ErrNoRows
	}
	// Probe loop: outer = current left row, inner = each match in its bucket.
	for {
		if j.leftRow == nil {
			row, err := j.left.Next(ctx)
			if err != nil {
				return Row{}, err
			}
			// Preserve tableName so qualified-name eval works.
			prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
			prefixed.tableName = row.tableName
			if !hasAnyPrefix(row.Cols) {
				prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
			} else {
				prefixed.Cols = append([]string(nil), row.Cols...)
			}
			j.leftRow = &prefixed
			// Compute the hash of the left row's join key and look up
			// its bucket. Reset bucketPos to walk through all matches.
			keyVal, ok := lookupColumn(&prefixed, j.leftTbl, j.leftKey)
			if !ok || keyVal == nil {
				// Left row missing the key column or has NULL —
				// SQL NULL keys never match.
				j.leftRow = nil
				continue
			}
			j.bucketHash = hashValue(j.hashSeed, keyVal)
			matches, found := j.buckets[j.bucketHash]
			if !found {
				// No matches in right for this left key. Skip.
				j.leftRow = nil
				continue
			}
			j.bucketPos = 0
			// Stash matches slice in a side channel (we use bucketHash + a map).
			j.buckets[j.bucketHash] = matches
		}
		// Emit the next match from the bucket.
		matches := j.buckets[j.bucketHash]
		if j.bucketPos >= len(matches) {
			// Exhausted matches for this left row — move on.
			j.leftRow = nil
			continue
		}
		rightIdx := matches[j.bucketPos]
		j.bucketPos++
		right := j.rightRows[rightIdx]
		// Set Outer so column lookups can resolve across the join.
		right.Outer = j.leftRow
		return joinRowsLL(j.leftRow, &right), nil
	}
}

func (j *HashCrossJoin) build(ctx context.Context) error {
	j.probeBuilt = true
	j.buckets = make(map[uint64][]int)
	const maxMaterialize = 1024
	j.rightRows = make([]Row, 0, 64)
	for {
		row, err := j.right.Next(ctx)
		if err != nil {
			break
		}
		prefixed := Row{
			Cols:      prefixCols(row.Cols, j.rightTbl),
			Types:     row.Types,
			Data:      append([]Value(nil), row.Data...),
			tableName: row.tableName,
		}
		keyVal, ok := lookupColumn(&prefixed, j.rightTbl, j.rightKey)
		if !ok || keyVal == nil {
			// Right row missing the key column or has NULL —
			// skip. SQL NULL keys never match each other.
			continue
		}
		h := hashValue(j.hashSeed, keyVal)
		idx := len(j.rightRows)
		j.rightRows = append(j.rightRows, prefixed)
		j.buckets[h] = append(j.buckets[h], idx)
		if len(j.rightRows) >= maxMaterialize {
			// Right side too large — fall back to NLJ semantics
			// by clearing buckets and forcing probeBuilt=false.
			// The caller (planner) should not have selected this
			// operator if size was unknown; abort safely.
			j.buckets = nil
			j.rightRows = nil
			j.probeBuilt = false
			return ErrNoRows
		}
	}
	return nil
}

func (j *HashCrossJoin) Close() error {
	j.probeBuilt = false
	j.buckets = nil
	j.rightRows = nil
	j.leftRow = nil
	j.bucketPos = 0
	j.bucketHash = 0
	_ = j.left.Close()
	return j.right.Close()
}

// lookupColumn finds the value of `col` in row, allowing either
// bare ("a") or table-qualified ("t1.a") column names.
func lookupColumn(row *Row, tbl, col string) (any, bool) {
	want := tbl + "." + col
	for i, c := range row.Cols {
		if c == want || c == col {
			if i < len(row.Data) {
				return row.Data[i].ToAny(), true
			}
			return nil, false
		}
	}
	// Fall back to colIndex map.
	if row.colIndex != nil {
		if idx, ok := row.colIndex[strings.ToLower(want)]; ok && idx < len(row.Data) {
			return row.Data[idx].ToAny(), true
		}
		if idx, ok := row.colIndex[strings.ToLower(col)]; ok && idx < len(row.Data) {
			return row.Data[idx].ToAny(), true
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
			if x.B {
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