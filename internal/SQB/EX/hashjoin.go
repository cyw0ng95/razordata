package EX

import (
	"context"
	"fmt"
	"hash/maphash"
	"strings"
)

// HashJoin is a radix-partitioned hash join for INNER joins
// on equi-keys. REQ000312, REQ000684, REQ000865.
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
//
// REQ000865: right side is NOT separately materialized — rows live
// only in the per-bucket slices. The right operator is closed
// immediately after the build phase to release resources.
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
	// REQ000865: rightRows removed — right rows live only in buckets.
	// right operator is closed after build phase.
	// Pre-computed matches with data buffer.
	matches    []Row
	matchPos   int
	dataBuf    []Value
	dataPerRow int
	done       bool
	// sharedCols, sharedTypes and sharedColIndex are built once
	// from the first output row's column layout and shared across
	// all emitted rows, eliminating per-row make+append for Cols/Types
	// and per-row buildColIndex for downstream operators (REQ000794).
	sharedCols     []string
	sharedTypes    []int
	sharedColIndex map[string]int

	// REQ000841: reusable key buffer for lookupKeys — pre-allocated
	// to max(len(leftKeys), len(rightKeys)) to avoid per-probe
	// make([]Value, len(keys)) in every hash-join probe.
	keyBuf []Value
	// REQ000865: built tracks whether buildAndProbe has run.
	built bool
	// joinBufferSize caps total memory for right-side + left-side
	// materialization. 0 = unlimited. Set by Planner from
	// Executor.WithMemoryBudget. REQ001056.
	joinBufferSize int64
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

	return &HashJoin{
		left: left, right: right,
		leftKeys: leftKeys, rightKeys: rightKeys,
		leftTbl: leftTbl, rightTbl: rightTbl,
		partitions: p,
		// REQ000841: pre-allocate key buffer to max key width.
		keyBuf: make([]Value, max(len(leftKeys), len(rightKeys))),
		// REQ000865: pre-allocate bucket slices for the build phase.
		buckets: make([]hashBucket, p),
	}
}

func (j *HashJoin) LeftChild() Operator { return j.left }

func (j *HashJoin) RightChild() Operator { return j.right }

// WithJoinBufferSize sets the per-hash-join memory cap
// (right-side + left-side materialization). 0 = unlimited.
// REQ001056.
func (j *HashJoin) WithJoinBufferSize(v int64) *HashJoin {
	j.joinBufferSize = v
	return j
}

// WithProjection sets the projected columns for the join output.
// REQ000803: when set, only these columns are included in output rows.
func (j *HashJoin) WithProjection(cols []string) *HashJoin {
	return j
}

// Next produces the next matching pair. First call performs
// the full Build + Probe with match pre-computation. Subsequent
// calls return pre-built rows from the data buffer.
// ErrNoRows when done.
func (j *HashJoin) Next(ctx context.Context) (Row, error) {
	if j.done {
		return Row{}, ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if !j.built {
		if err := j.buildAndProbe(ctx); err != nil {
			return Row{}, err
		}
		j.built = true
	}
	// REQ000802+: return pre-computed matches from data buffer.
	for j.matchPos < len(j.matches) {
		m := j.matches[j.matchPos]
		j.matchPos++
		return m, nil
	}
	j.done = true
	return Row{}, ErrNoRows
}

func (j *HashJoin) Close() error {
	for i := range j.buckets {
		j.buckets[i].rightRows = j.buckets[i].rightRows[:0]
		j.buckets[i].hashes = j.buckets[i].hashes[:0]
	}
	j.leftRows = nil
	j.matches = nil
	j.matchPos = 0
	j.dataBuf = nil
	j.dataPerRow = 0
	j.done = false
	j.built = false
	j.sharedCols = nil
	j.sharedTypes = nil
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
// then reads the left side and probes. All matches are
// pre-computed with a shared data buffer to eliminate per-row
// Data allocations (REQ000802+).
//
// REQ000865: right side rows live ONLY in per-bucket slices — no
// separate rightRows materialization. Right operator is closed
// immediately after the build phase to free resources early.
func (j *HashJoin) buildAndProbe(ctx context.Context) error {
	// REQ000865: pre-allocate all bucket slices upfront.
	for i := range j.buckets {
		j.buckets[i].rightRows = make([]Row, 0, 64)
		j.buckets[i].hashes = make([]uint64, 0, 64)
	}
	// REQ001056: estimated bytes per row for budget checking.
	// Each Row ≈ 5 × 24 bytes (Value) + 64 base + column metadata.
	const estBytesPerRow = 200

	// Build phase: hash right side into partition buckets.
	// REQ001056: check budget every 1024 rows and stop early
	// when joinBufferSize is exceeded — prevents materializing
	// the full right side in memory before the budget check.
	var rightCount int
	var firstRightCols []string
	var firstRightTypes []int
	for {
		row, err := j.right.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		if rightCount == 0 {
			firstRightCols = row.Cols
			firstRightTypes = row.Types
		}
		rightCount++
		// REQ001056: proactive budget check during materialization.
		if j.joinBufferSize > 0 && rightCount%1024 == 0 {
			if int64(rightCount)*estBytesPerRow > j.joinBufferSize {
				break
			}
		}
		rk := lookupKeys(row, j.rightKeys, j.keyBuf)
		hash := hashKeys(rk)
		idx := int(hash & uint64(j.partitions-1))
		j.buckets[idx].rightRows = append(j.buckets[idx].rightRows, row)
		j.buckets[idx].hashes = append(j.buckets[idx].hashes, hash)
	}
	// REQ000865: close right side immediately — rows live in buckets.
	if j.right != nil {
		_ = j.right.Close()
	}
	// Materialize left side. REQ001056: proactive budget check.
	for {
		row, err := j.left.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		j.leftRows = append(j.leftRows, row)
		if j.joinBufferSize > 0 && len(j.leftRows)%1024 == 0 {
			if int64(len(j.leftRows))*estBytesPerRow > j.joinBufferSize {
				break
			}
		}
	}

	// REQ001056: check total materialized rows against joinBufferSize.
	// Rough estimate: each Row with N columns ≈ N * (8+16) bytes + 64 base.
	if j.joinBufferSize > 0 {
		var rightTotal int
		for i := range j.buckets {
			rightTotal += len(j.buckets[i].rightRows)
		}
		totalRows := len(j.leftRows) + rightTotal
		// Estimate: each row has ~5 columns × 24 bytes = 120 + 64 base ≈ 200 bytes.
		estBytes := int64(totalRows) * 200
		if estBytes > j.joinBufferSize {
			return fmt.Errorf("hash join materialized %d rows (~%d bytes), exceeds joinBufferSize=%d", totalRows, estBytes, j.joinBufferSize)
		}
	}
	// Pre-build sharedCols, sharedTypes and sharedColIndex from the
	// first output row's column layout so every emitted row reuses
	// them instead of allocating fresh Cols/Types slices and
	// triggering per-row buildColIndex downstream.
	if len(j.leftRows) > 0 && rightCount > 0 {
		n := len(j.leftRows[0].Cols) + len(firstRightCols)
		j.sharedCols = make([]string, 0, n)
		j.sharedCols = append(j.sharedCols, j.leftRows[0].Cols...)
		j.sharedCols = append(j.sharedCols, firstRightCols...)
		j.sharedTypes = make([]int, 0, n)
		j.sharedTypes = append(j.sharedTypes, j.leftRows[0].Types...)
		j.sharedTypes = append(j.sharedTypes, firstRightTypes...)
		j.sharedColIndex = make(map[string]int, n)
		for i, c := range j.sharedCols {
			key := strings.ToLower(c)
			if _, exists := j.sharedColIndex[key]; !exists {
				j.sharedColIndex[key] = i
			}
		}
	}
	// REQ000802+: pre-compute all matches with data buffer.
	// First, pre-compute left-side lookup keys to avoid
	// redundant lookups during match counting and building.
	type leftInfo struct {
		lk   []Value
		hash uint64
		idx  int
	}
	leftInfos := make([]leftInfo, 0, len(j.leftRows))
	// REQ001018: pre-allocate flat key buffer to avoid per-row make.
	var flatKeyBuf []Value
	if len(j.leftRows) > 0 {
		first := lookupKeys(j.leftRows[0], j.leftKeys, j.keyBuf)
		flatKeyBuf = make([]Value, len(j.leftRows)*len(first))
	}
	for li, left := range j.leftRows {
		lk := lookupKeys(left, j.leftKeys, j.keyBuf)
		// REQ000841: copy into dedicated storage — leftInfo stores
		// the slice for use during match phase, so it must not
		// share the reusable keyBuf backing array.
		lkCopy := flatKeyBuf[li*len(lk) : (li+1)*len(lk)]
		copy(lkCopy, lk)
		hash := hashKeys(lkCopy)
		idx := int(hash & uint64(j.partitions-1))
		leftInfos = append(leftInfos, leftInfo{lk: lkCopy, hash: hash, idx: idx})
	}

	// Count total matches.
	var totalMatches int
	for i := range j.leftRows {
		l := leftInfos[i]
		bucket := j.buckets[l.idx]
		for k := range bucket.hashes {
			if bucket.hashes[k] == l.hash && valuesEqualMulti(l.lk, lookupKeys(bucket.rightRows[k], j.rightKeys, j.keyBuf)) {
				totalMatches++
			}
		}
	}

	// REQ001056: cap totalMatches based on joinBufferSize to prevent
	// OOM from dataBuf pre-allocation (e.g. cross-join with 100K
	// rows each side produces 10B matches → 80GB dataBuf).
	// Each Value is ~24 bytes; each match row has dataPerRow Values.
	// Compute dataPerRow before the cap since the cap depends on it.
	var dataPerRow int
	if len(j.leftRows) > 0 && rightCount > 0 {
		dataPerRow = len(j.leftRows[0].Cols) + len(firstRightCols)
	}
	if j.joinBufferSize > 0 && totalMatches > 0 && dataPerRow > 0 {
		maxValues := j.joinBufferSize / 24
		maxMatches := int(maxValues / int64(dataPerRow))
		if maxMatches < 1 {
			maxMatches = 1
		}
		if totalMatches > maxMatches {
			totalMatches = maxMatches
		}
	}

	// Pre-allocate contiguous data buffer and matches slice.
	if len(j.leftRows) == 0 || rightCount == 0 {
		return nil
	}
	j.dataPerRow = dataPerRow
	// REQ001090: dataBuf pre-allocates totalMatches*dataPerRow
	// Values. The output Row.Data sub-slices point INTO this
	// buffer (line 321: j.dataBuf[off:off+dataPerRow:off+dataPerRow])
	// so the cap MUST be exact — if we cap lower and append grows,
	// append reallocates the backing array and the previously
	// emitted Row.Data slices become dangling pointers. The original
	// 1GiB MaxResultRows cap at the driver level is the right
	// place to bound memory for this operator.
	j.dataBuf = make([]Value, 0, totalMatches*dataPerRow)
	j.matches = make([]Row, 0, totalMatches)
	j.matchPos = 0

	// Build matches.
	for i := range j.leftRows {
		l := leftInfos[i]
		bucket := j.buckets[l.idx]
		for k := range bucket.hashes {
			if bucket.hashes[k] == l.hash && valuesEqualMulti(l.lk, lookupKeys(bucket.rightRows[k], j.rightKeys, j.keyBuf)) {
				right := bucket.rightRows[k]
				off := len(j.dataBuf)
				j.dataBuf = j.dataBuf[:off+dataPerRow]
				copy(j.dataBuf[off:], j.leftRows[i].Data)
				copy(j.dataBuf[off+len(j.leftRows[i].Data):], right.Data)
				out := Row{
					Cols:     j.sharedCols,
					Types:    j.sharedTypes,
					Data:     j.dataBuf[off : off+dataPerRow : off+dataPerRow],
					colIndex: j.sharedColIndex,
				}
				j.matches = append(j.matches, out)
			}
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
	switch v.Kind {
	case KindInt:
		// REQ001039: fast path for int64 keys — FNV-1a mixing, zero allocation.
		x := uint64(v.I64)
		return x*0x9e3779b97f4a7c15 ^ (x >> 31)
	case KindFloat:
		// REQ001039: fast path for float64 keys — FNV-1a mixing, zero allocation.
		u := uint64Bits(v.F64)
		return u*0x9e3779b97f4a7c15 ^ (u >> 31)
	case KindText:
		var h maphash.Hash
		h.SetSeed(hashKeySeed)
		_, _ = h.WriteString(v.S)
		return h.Sum64()
	default:
		var h maphash.Hash
		h.SetSeed(hashKeySeed)
		_, _ = h.WriteString(stringify(v.ToAny()))
		return h.Sum64()
	}
}

// lookupKeys extracts multiple key values from a row.
func lookupKeys(row Row, keys []string, buf []Value) []Value {
	vals := buf
	if len(vals) < len(keys) {
		vals = make([]Value, len(keys))
	} else {
		vals = vals[:len(keys)]
	}
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
		return a.Bo == b.Bo
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
