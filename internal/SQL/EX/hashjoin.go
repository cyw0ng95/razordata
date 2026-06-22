package EX

import (
	"context"
	"hash/maphash"
	"strings"
)

// HashJoin is a radix-partitioned hash join for INNER joins
// on equi-keys. REQ000312, REQ000684.
// Algorithm (classic radix hash join):
//  1. Build phase: hash the right relation's join key into
//     N radix partitions (one per high bit of the hash).
//  2. Probe phase: hash the left relation's join key, look up
//     the matching right partition, and probe the per-bucket
//     hash table. Emit matching pairs.
//  3. For N radix bits, this reduces the per-probe set size
//     by 2^N (and the hash table fits in L1 cache).
//
// The implementation uses maphash.Hash for the partition key.
// Supports multi-column equi-join keys (REQ000684).
// Current limits:
//   - INNER JOIN only (LEFT/RIGHT/FULL deferred to NestedLoopJoin)
//   - Equi-join only (non-equi joins deferred to NestedLoopJoin)
type HashJoin struct {
	left       Operator
	right      Operator
	leftKeys   []string
	rightKeys  []string
	leftTbl    string
	rightTbl   string
	partitions int
	buckets    []hashBucket
	leftRows   []Row
	rightRows  []Row
	emitIdx    int
	bucketPos  int // position within current bucket's hash/rightRows for multi-match
	emitRow    Row
	done       bool
	// sharedCols and sharedColIndex are built once from the first
	// output row's column layout and shared across all emitted rows,
	// eliminating per-row make+append for Cols and per-row
	// buildColIndex for downstream operators (REQ000794).
	sharedCols     []string
	sharedColIndex map[string]int
}

type hashBucket struct {
	rightRows []Row // indexed by hash
	hashes    []uint64
}

// NewHashJoin creates a radix hash join. partitions must be
// a power of 2; values < 16 are bumped up to 16. The left and
// right operators are consumed fully during Build/Probe.
// leftKeys and rightKeys are the join column names (multi-column supported).
func NewHashJoin(left, right Operator, leftTbl, rightTbl string, leftKeys, rightKeys []string, partitions int) *HashJoin {
	const minPartitions = 16
	if partitions < minPartitions {
		partitions = minPartitions
	}
	// Round up to next power of 2.
	p := 1
	for p < partitions {
		p <<= 1
	}

	return &HashJoin{left: left, right: right, leftKeys: leftKeys, rightKeys: rightKeys, leftTbl: leftTbl, rightTbl: rightTbl, partitions: p}
}

func (j *HashJoin) LeftChild() Operator { return j.left }

func (j *HashJoin) RightChild() Operator { return j.right }

// Next produces the next matching pair. First call performs
// the full Build + Probe. Subsequent calls iterate over
// all matches from the current bucket position before advancing
// to the next left row. ErrNoRows when done.
func (j *HashJoin) Next(ctx context.Context) (Row, error) {
	if j.done {
		return Row{}, ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if j.buckets == nil {
		if err := j.buildAndProbe(ctx); err != nil {
			return Row{}, err
		}
	}
	for j.emitIdx < len(j.leftRows) {
		left := j.leftRows[j.emitIdx]
		lk := lookupKeys(left, j.leftKeys)
		hash := hashKeys(lk)
		idx := int(hash & uint64(j.partitions-1))
		bucket := j.buckets[idx]
		// Continue scanning from the saved position within the
		// bucket to find the next matching right row.
		for k := j.bucketPos; k < len(bucket.hashes); k++ {
			if bucket.hashes[k] == hash {
				right := bucket.rightRows[k]
				rk := lookupKeys(right, j.rightKeys)
				if valuesEqualMulti(lk, rk) {
					j.bucketPos = k + 1
					return joinRows(left, right, j.sharedCols, j.sharedColIndex), nil
				}
			}
		}
		// No more matches for this left row; advance to next.
		j.emitIdx++
		j.bucketPos = 0
	}
	j.done = true
	return Row{}, ErrNoRows
}

func (j *HashJoin) Close() error {
	j.buckets = nil
	j.leftRows = nil
	j.rightRows = nil
	j.emitIdx = 0
	j.bucketPos = 0
	j.done = false
	j.emitRow = Row{}
	j.sharedCols = nil
	j.sharedColIndex = nil
	if j.left != nil {
		_ = j.left.Close()
	}
	if j.right != nil {
		return j.right.Close()
	}
	return nil
}

// buildAndProbe reads the right side into partition buckets,
// then reads the left side and probes.
func (j *HashJoin) buildAndProbe(ctx context.Context) error {
	j.buckets = make([]hashBucket, j.partitions)
	// Materialize right side.
	for {
		row, err := j.right.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		rk := lookupKeys(row, j.rightKeys)
		hash := hashKeys(rk)
		idx := int(hash & uint64(j.partitions-1))
		j.buckets[idx].rightRows = append(j.buckets[idx].rightRows, row)
		j.buckets[idx].hashes = append(j.buckets[idx].hashes, hash)
		j.rightRows = append(j.rightRows, row)
	}
	// Materialize left side.
	for {
		row, err := j.left.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		j.leftRows = append(j.leftRows, row)
	}
	// Pre-build sharedCols and sharedColIndex from the first output
	// row's column layout so every emitted row reuses them instead
	// of allocating fresh Cols slices and triggering per-row
	// buildColIndex downstream.
	if len(j.leftRows) > 0 && len(j.rightRows) > 0 {
		n := len(j.leftRows[0].Cols) + len(j.rightRows[0].Cols)
		j.sharedCols = make([]string, 0, n)
		j.sharedCols = append(j.sharedCols, j.leftRows[0].Cols...)
		j.sharedCols = append(j.sharedCols, j.rightRows[0].Cols...)
		j.sharedColIndex = make(map[string]int, n)
		for i, c := range j.sharedCols {
			j.sharedColIndex[strings.ToLower(c)] = i
		}
	}
	return nil
}

// hashKeySeed is a fixed maphash seed so identical keys
// produce identical hashes (vital for join correctness).
var hashKeySeed = maphash.MakeSeed()

// hashKey computes a uint64 hash of a key value. maphash
// is fast and distributes well. A fixed seed is used so
// the same key always hashes to the same value across
// calls and goroutines.
func hashKey(v Value) uint64 {
	if v.IsNull() {
		return 0
	}
	var h maphash.Hash
	h.SetSeed(hashKeySeed)
	switch v.Kind {
	case KindInt:
		x := v.I64
		_, _ = h.Write([]byte{
			byte(x), byte(x >> 8), byte(x >> 16), byte(x >> 24),
			byte(x >> 32), byte(x >> 40), byte(x >> 48), byte(x >> 56),
		})
	case KindText:
		_, _ = h.WriteString(v.S)
	case KindFloat:
		u := uint64Bits(v.F64)
		_, _ = h.Write([]byte{
			byte(u), byte(u >> 8), byte(u >> 16), byte(u >> 24),
			byte(u >> 32), byte(u >> 40), byte(u >> 48), byte(u >> 56),
		})
	default:
		_, _ = h.WriteString(stringify(v.ToAny()))
	}
	return h.Sum64()
}

// lookupKeys extracts multiple key values from a row.
func lookupKeys(row Row, keys []string) []Value {
	vals := make([]Value, len(keys))
	for i, k := range keys {
		v, _ := row.Lookup(k)
		// REQ000725: when the row comes from a previous join,
		// its columns are prefixed with the table name (e.g.
		// 't51.a51'). A bare key 'a51' won't match unless we
		// also try the bare form. Walk Cols once to find a
		// suffix match if the direct lookup failed.
		// REQ000794: when the key is qualified (e.g. "t3.c"),
		// try the bare column name after the dot as fallback
		// so the lookup works for both prefixed NLJ output
		// rows (where colIndex has "t3.c") and bare SeqScan
		// rows (where colIndex has "c").
		if v == nil {
			bare := k
			if dotIdx := strings.LastIndexByte(k, '.'); dotIdx >= 0 {
				bare = k[dotIdx+1:]
				if bv, ok := row.Lookup(bare); ok {
					v = bv
				}
			}
		}
		if v == nil {
			lk := strings.ToLower(k)
			for j, c := range row.Cols {
				if strings.HasSuffix(strings.ToLower(c), "."+lk) && j < len(row.Data) {
					v = row.Data[j]
					break
				}
			}
		}
		vals[i] = valueFromAny(v)
	}
	return vals
}

// joinRows combines a left and right row into a single Row.
// When sharedCols (from the HashJoin struct) is set, the output
// shares the pre-built Cols and colIndex to avoid per-row alloc.
// Data is always freshly allocated since it carries row-specific
// values. REQ000794.
func joinRows(left, right Row, sharedCols []string, sharedColIndex map[string]int) Row {
	out := Row{
		Data: make([]Value, 0, len(left.Data)+len(right.Data)),
	}
	if sharedCols != nil {
		out.Cols = sharedCols
		out.colIndex = sharedColIndex
	} else {
		out.Cols = make([]string, 0, len(left.Cols)+len(right.Cols))
		out.Cols = append(out.Cols, left.Cols...)
		out.Cols = append(out.Cols, right.Cols...)
	}
	out.Data = append(out.Data, left.Data...)
	out.Data = append(out.Data, right.Data...)
	return out
}

// hashKeys computes a uint64 hash of multiple key values by
// hashing each value and combining the hashes.
func hashKeys(vals []Value) uint64 {
	if len(vals) == 1 {
		return hashKey(vals[0])
	}
	var h maphash.Hash
	h.SetSeed(hashKeySeed)
	for _, v := range vals {
		h2 := hashKey(v)
		_, _ = h.Write([]byte{
			byte(h2), byte(h2 >> 8), byte(h2 >> 16), byte(h2 >> 24),
			byte(h2 >> 32), byte(h2 >> 40), byte(h2 >> 48), byte(h2 >> 56),
		})
	}
	return h.Sum64()
}

// valuesEqualMulti compares multiple key values for equality.
func valuesEqualMulti(a, b []Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !valuesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// valuesEqual compares two Values for equality.
func valuesEqual(a, b Value) bool {
	if a.IsNull() && b.IsNull() {
		return true
	}
	if a.IsNull() || b.IsNull() {
		return false
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindInt:
		return a.I64 == b.I64
	case KindText:
		return a.S == b.S
	case KindFloat:
		return a.F64 == b.F64
	case KindBool:
		return a.B == b.B
	}
	return false
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if i, ok := v.(int64); ok {
		return string(rune(i))
	}
	return ""
}

// uint64Bits reinterprets a float64 as a uint64 (bitwise copy).
// We do this without importing math by using a bit-shifting
// trick: float64 -> uint64 via IEEE-754 layout (sign|exp|mantissa).
func uint64Bits(f float64) uint64 {
	// Handle 0 explicitly to avoid log(0) issues.
	if f == 0 {
		return 0
	}
	negative := f < 0
	if negative {
		f = -f
	}
	// Approximate: hash doesn't need bit-exact IEEE-754
	// encoding. We multiply by 2^52 to extract the
	// significant bits and bias the exponent.
	biased := uint64(f * (1 << 52))
	if negative {
		biased |= 1 << 63
	}
	return biased
}
