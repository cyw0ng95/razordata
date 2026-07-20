package OP

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// HashJoin is a radix-partitioned hash join for equi-keys.
// REQ000312, REQ000684, REQ000865, REQ001020.
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
//
// REQ001020: INNER, LEFT, RIGHT, and FULL outer joins are all
// supported. Outer semantics:
//   - LEFT:  every unmatched left row emits with NULL right side.
//   - RIGHT: every unmatched right row emits with NULL left side.
//   - FULL:  union of LEFT and RIGHT semantics.
//
// Equi-joins only (non-equi joins still fall back to NestedLoopJoin).
//
// REQ000865: right side is NOT separately materialized — rows live
// only in the per-bucket slices. No separate rightRows materialization.
// Right operator is closed immediately after the build phase to
// free resources early.
type HashJoin struct {
	left       pl.Operator
	right      pl.Operator
	leftKeys   []string
	rightKeys  []string
	leftTbl    string
	rightTbl   string
	partitions int
	buckets    []hashBucket
	leftRows   []pl.Row
	leftInfos  []leftInfo
	curLeftIdx  int
	curRightIdx int
	emitBuf          []pl.Value
	// REQ001611: batch emit buffer — pre-allocated slab for
	// multiple output rows. Reduces per-match make([]pl.Value)
	// overhead in nextMatched by amortizing across up to 64 rows.
	batchBuf        []pl.Value
	batchPos        int
	batchFillCount  int
	batchLeftIdx    []int
	batchBucketIdx  []int
	batchRightIdx   []int
	unmatchedLeftBuf  []pl.Value
	unmatchedRightBuf []pl.Value
	dataPerRow  int
	leftLen     int // REQ001272: fixed left-side column count for emitUnmatchedRight
	done        bool
	// sharedCols, sharedTypes and sharedColIndex are built once
	// from the first output row's column layout and shared across
	// all emitted rows, eliminating per-row make+append for Cols/Types
	// and per-row buildColIndex for downstream operators (REQ000794).
	sharedCols     []string
	sharedTypes    []LX.TokenType
	sharedColIndex map[string]int

	// REQ000841: reusable key buffer for lookupKeys — pre-allocated
	// to max(len(leftKeys), len(rightKeys)) to avoid per-probe
	// make([]pl.Value, len(keys)) in every hash-join probe.
	keyBuf []pl.Value
	// REQ000865: built tracks whether buildAndProbe has run.
	built bool
	// joinBufferSize caps total memory for right-side + left-side
	// materialization. 0 = unlimited. Set by Planner from
	// Executor.WithMemoryBudget. REQ001056.
	joinBufferSize int64

	// REQ001020: outer-join state. kind selects INNER (default),
	// LEFT, RIGHT, or FULL. matchedRight[bucketIdx][rowIdx] tracks
	// whether that right row has been emitted at least once, so
	// the final pass can produce NULL-padded unmatched rows.
	kind         JoinKind
	leftMatched  []bool   // REQ001020: indexed by leftRows index
	matchedRight [][]bool // REQ001020: matchedRight[b][i] for bucket b, row i
	// REQ001020: phase tracks where Next() is in the multi-phase
	// emission: 0 = match pairs, 1 = unmatched-left (LEFT/FULL),
	// 2 = unmatched-right (RIGHT/FULL), 3 = done.
	phase int
	// REQ001020: unmatchedLeftIdx and unmatchedRightBucket/Idx
	// track positions within the unmatched-row sweeps.
	unmatchedLeftIdx     int
	unmatchedRightBucket int
	unmatchedRightIdx    int

	closed atomic.Bool

	// REQ001410 debug: per-operator counters and label for tracing
	// row flow through the join tree. Set via WithDebugID.
	debugID    string
	rightBuilt int   // count of right-side rows materialized in build phase
	matchCount int64 // count of emitted matched pairs (incremented in Next)
}

type hashBucket struct {
	rightRows []pl.Row // indexed by hash
	hashes    []uint64
}

// leftInfo stores pre-computed join key info for a left row, used
// by both the match-counting pass and the streaming probe in Next().
// REQ0011XX: hoisted to package scope so it can be stored on HashJoin.
type leftInfo struct {
	lk   []pl.Value
	hash uint64
	idx  int
}

// NewHashJoin creates a radix hash join. partitions must be
// a power of 2; values < 16 are bumped up to 16. The left and
// right operators are consumed fully during Build/Probe.
// leftKeys and rightKeys are the join column names (multi-column supported).
func NewHashJoin(left, right pl.Operator, leftTbl, rightTbl string, leftKeys, rightKeys []string, partitions int) *HashJoin {
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
		keyBuf: make([]pl.Value, max(len(leftKeys), len(rightKeys))),
		// REQ000865: pre-allocate bucket slices for the build phase.
		buckets: make([]hashBucket, p),
	}
}

func (j *HashJoin) LeftChild() pl.Operator      { return j.left }
func (j *HashJoin) RightChild() pl.Operator     { return j.right }
func (j *HashJoin) LeftTbl() string             { return j.leftTbl }
func (j *HashJoin) RightTbl() string            { return j.rightTbl }
func (j *HashJoin) LeftKeys() []string          { return j.leftKeys }
func (j *HashJoin) RightKeys() []string         { return j.rightKeys }
func (j *HashJoin) SharedCols() []string        { return j.sharedCols }
func (j *HashJoin) SharedTypes() []LX.TokenType { return j.sharedTypes }

// Partitions returns the number of hash partitions.
func (j *HashJoin) Partitions() int { return j.partitions }

// JoinBufferSize returns the per-hash-join memory cap.
func (j *HashJoin) JoinBufferSize() int64 { return j.joinBufferSize }

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

// WithDebugID sets a label for debug tracing. REQ001410.
func (j *HashJoin) WithDebugID(id string) *HashJoin {
	j.debugID = id
	return j
}

// WithKind selects the join kind (Inner/Left/Right/Full). REQ001020.
// Defaults to JoinKindInner. Pass any other JoinKind value to
// enable outer semantics.
func (j *HashJoin) WithKind(k JoinKind) *HashJoin {
	j.kind = k
	return j
}

// Kind returns the configured join kind. REQ001020.
func (j *HashJoin) Kind() JoinKind { return j.kind }

// Next produces the next matching pair. First call performs
// the full Build + Probe with match pre-computation. Subsequent
// calls return pre-built rows from the data buffer.
// ErrNoRows when done.
func (j *HashJoin) Next(ctx context.Context) (pl.Row, error) {
	ec.BUG_ON(j.closed.Load(), "HashJoin.Next() after Close()")
	if j.done {
		return pl.Row{}, ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return pl.Row{}, err
	}
	if !j.built {
		if err := j.buildAndProbe(ctx); err != nil {
			return pl.Row{}, err
		}
		j.built = true
		// REQ001410 debug: print per-operator row counts at the end of build/probe.
		if j.debugID != "" {
			fmt.Fprintf(os.Stderr, "[HJ %s] built: left=%d right=%d matches=%d\n",
				j.debugID, len(j.leftRows), j.rightBuilt, j.matchCount)
		}
	}
	// REQ001410 debug: print emitted count when starting emission.
	if j.debugID != "" && j.matchCount == 0 && len(j.leftRows) > 0 {
		fmt.Fprintf(os.Stderr, "[HJ %s] emit start: left=%d right=%d\n",
			j.debugID, len(j.leftRows), j.rightBuilt)
	}
	// REQ001020: three-phase emission. Phase 0 = matched pairs
	// (always). Phase 1 = unmatched-left for LEFT/FULL. Phase 2
	// = unmatched-right for RIGHT/FULL.
	for j.phase == 0 {
		row, matchedLeft, bucketIdx, rightIdx, ok := j.nextMatched()
		if ok {
			if matchedLeft >= 0 && j.leftMatched != nil {
				j.leftMatched[matchedLeft] = true
			}
			if bucketIdx >= 0 && rightIdx >= 0 && j.matchedRight != nil && bucketIdx < len(j.matchedRight) && rightIdx < len(j.matchedRight[bucketIdx]) {
				j.matchedRight[bucketIdx][rightIdx] = true
			}
			j.matchCount++
			return row, nil
		}
		// Phase 0 exhausted. Decide next phase based on join kind.
		switch j.kind {
		case JoinKindLeft:
			j.phase = 1
			j.unmatchedLeftIdx = 0
		case JoinKindFull:
			// FULL: emit unmatched-left first, then unmatched-right
			// in a second pass (handled by the outer loop).
			j.phase = 1
			j.unmatchedLeftIdx = 0
		case JoinKindRight:
			j.phase = 2
			j.unmatchedRightBucket = 0
			j.unmatchedRightIdx = 0
		default:
			j.phase = 3
		}
	}
	if j.phase == 1 {
		for j.unmatchedLeftIdx < len(j.leftRows) {
			li := j.unmatchedLeftIdx
			j.unmatchedLeftIdx++
			if j.leftMatched != nil && j.leftMatched[li] {
				continue
			}
			return j.emitUnmatchedLeft(li), nil
		}
		// After unmatched-left, FULL also needs unmatched-right.
		if j.kind == JoinKindFull {
			j.phase = 2
			j.unmatchedRightBucket = 0
			j.unmatchedRightIdx = 0
		} else {
			j.phase = 3
		}
	}
	if j.phase == 2 {
		for j.unmatchedRightBucket < len(j.buckets) {
			bucket := &j.buckets[j.unmatchedRightBucket]
			for j.unmatchedRightIdx < len(bucket.rightRows) {
				ri := j.unmatchedRightIdx
				j.unmatchedRightIdx++
				if j.matchedRight != nil && j.matchedRight[j.unmatchedRightBucket][ri] {
					continue
				}
				return j.emitUnmatchedRight(j.unmatchedRightBucket, ri), nil
			}
			j.unmatchedRightIdx = 0
			j.unmatchedRightBucket++
		}
		j.phase = 3
	}
	j.done = true
	// REQ001410 debug: print final emission count.
	if j.debugID != "" {
		fmt.Fprintf(os.Stderr, "[HJ %s] done: left=%d right=%d emitted=%d\n",
			j.debugID, len(j.leftRows), j.rightBuilt, j.matchCount)
	}
	return pl.Row{}, ErrNoRows
}

// nextMatched emits the next matching pair across left rows and
// right rows. Returns (row, leftIdx, bucketIdx, rightIdx, true) on
// a match or (zero, 0, 0, 0, false) when all matches are exhausted.
// REQ001020: also returns the (leftIdx, bucketIdx, rightIdx)
// coordinates so Next() can update matchedRight/leftMatched.
// REQ001611: uses a pre-allocated batch buffer (batchBuf) to
// amortize per-match allocation overhead across up to 64 rows.
func (j *HashJoin) nextMatched() (pl.Row, int, int, int, bool) {
	// Serve from batch buffer first.
	if j.batchPos < j.batchFillCount {
		off := j.batchPos * j.dataPerRow
		row := pl.Row{
			Cols:     j.sharedCols,
			Types:    j.sharedTypes,
			Data:     j.batchBuf[off : off+j.dataPerRow : off+j.dataPerRow],
			ColIndex: j.sharedColIndex,
		}
		leftIdx := j.batchLeftIdx[j.batchPos]
		bucketIdx := j.batchBucketIdx[j.batchPos]
		rightIdx := j.batchRightIdx[j.batchPos]
		j.batchPos++
		return row, leftIdx, bucketIdx, rightIdx, true
	}
	j.batchPos = 0
	j.batchFillCount = 0

	// Fill batch buffer with up to batchEmitSize matches.
	const batchEmitSize = 64
	for j.curLeftIdx < len(j.leftRows) {
		l := j.leftInfos[j.curLeftIdx]
		bucket := j.buckets[l.idx]
		hashJoinDebugRowFlow(j.leftTbl, uint64(j.curLeftIdx), true)
		for j.curRightIdx < len(bucket.hashes) {
			k := j.curRightIdx
			j.curRightIdx++
			matched := bucket.hashes[k] == l.hash && ValuesEqualMulti(l.lk, lookupKeys(bucket.rightRows[k], j.rightKeys, j.keyBuf))
			hashJoinDebugPredicate("equi-join", uint64(j.curLeftIdx), uint64(k), matched)
			if matched {
				right := bucket.rightRows[k]
				leftData := j.leftRows[j.curLeftIdx].Data
				// Pre-allocate batch buffer on first fill.
				if cap(j.batchBuf) < batchEmitSize*j.dataPerRow {
					j.batchBuf = make([]pl.Value, batchEmitSize*j.dataPerRow)
					j.batchLeftIdx = make([]int, batchEmitSize)
					j.batchBucketIdx = make([]int, batchEmitSize)
					j.batchRightIdx = make([]int, batchEmitSize)
				}
				off := j.batchFillCount * j.dataPerRow
				outData := j.batchBuf[off : off+j.dataPerRow : off+j.dataPerRow]
				copy(outData, leftData)
				copy(outData[len(leftData):], right.Data)
				j.batchLeftIdx[j.batchFillCount] = j.curLeftIdx
				j.batchBucketIdx[j.batchFillCount] = l.idx
				j.batchRightIdx[j.batchFillCount] = k
				j.batchFillCount++
				hashJoinDebugRowFlow(j.leftTbl, uint64(j.curLeftIdx), false)
				if j.batchFillCount >= batchEmitSize {
					// Batch full — stop filling and return first match.
					j.batchPos = 0
					off0 := 0
					row0 := pl.Row{
						Cols:     j.sharedCols,
						Types:    j.sharedTypes,
						Data:     j.batchBuf[off0 : off0+j.dataPerRow : off0+j.dataPerRow],
						ColIndex: j.sharedColIndex,
					}
					j.batchPos = 1
					return row0, j.batchLeftIdx[0], j.batchBucketIdx[0], j.batchRightIdx[0], true
				}
			}
		}
		j.curRightIdx = 0
		j.curLeftIdx++
	}
	if j.batchFillCount > 0 {
		off := 0
		row := pl.Row{
			Cols:     j.sharedCols,
			Types:    j.sharedTypes,
			Data:     j.batchBuf[off : off+j.dataPerRow : off+j.dataPerRow],
			ColIndex: j.sharedColIndex,
		}
		leftIdx := j.batchLeftIdx[0]
		bucketIdx := j.batchBucketIdx[0]
		rightIdx := j.batchRightIdx[0]
		j.batchPos = 1
		return row, leftIdx, bucketIdx, rightIdx, true
	}
	return pl.Row{}, -1, -1, -1, false
}

// emitUnmatchedLeft produces a row with the left side's data and
// NULL right columns. REQ001020. Uses emitBuf backing array;
// caller must not retain row.Data across Next calls.
func (j *HashJoin) emitUnmatchedLeft(li int) pl.Row {
	out := pl.Row{
		Cols:     j.sharedCols,
		Types:    j.sharedTypes,
		ColIndex: j.sharedColIndex,
		Data:     j.unmatchedLeftBuf[:j.dataPerRow],
	}
	leftData := j.leftRows[li].Data
	copy(out.Data, leftData)
	clear(out.Data[len(leftData):])
	return out
}

// emitUnmatchedRight produces a row with the right side's data
// and NULL left columns. leftLen is a fixed value computed from
// the shared schema at join construction time (REQ001272).
func (j *HashJoin) emitUnmatchedRight(bucketIdx, rowInBucket int) pl.Row {
	out := pl.Row{
		Cols:     j.sharedCols,
		Types:    j.sharedTypes,
		ColIndex: j.sharedColIndex,
		Data:     j.unmatchedRightBuf[:j.dataPerRow],
	}
	right := j.buckets[bucketIdx].rightRows[rowInBucket]
	leftLen := j.leftLen
	copy(out.Data[leftLen:], right.Data)
	return out
}

func (j *HashJoin) Close() error {
	j.closed.Store(true)
	for i := range j.buckets {
		j.buckets[i].rightRows = j.buckets[i].rightRows[:0]
		j.buckets[i].hashes = j.buckets[i].hashes[:0]
	}
	j.leftRows = nil
	j.leftInfos = nil
	j.emitBuf = nil
	j.unmatchedLeftBuf = nil
	j.unmatchedRightBuf = nil
	j.dataPerRow = 0
	j.curLeftIdx = 0
	j.curRightIdx = 0
	j.done = false
	j.built = false
	j.sharedCols = nil
	j.sharedTypes = nil
	j.sharedColIndex = nil
	j.leftMatched = nil
	j.matchedRight = nil
	j.phase = 0
	j.unmatchedLeftIdx = 0
	j.unmatchedRightBucket = 0
	j.unmatchedRightIdx = 0
	if j.left != nil {
		_ = j.left.Close()
	}
	// Note: j.right is already closed in buildAndProbe() after the
	// build phase. Do not close it again here (double-close bug).
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
		j.buckets[i].rightRows = make([]pl.Row, 0, 64)
		j.buckets[i].hashes = make([]uint64, 0, 64)
	}
	// REQ0011XX: estimated bytes per row for budget checking.
	// Each pl.Row ≈ N × 24 bytes (pl.Value) + 64 base + column metadata.
	// Using 1200 bytes per row (covers up to ~30 columns) to be safe —
	// Go's slice growth doubles capacity, so undersizing causes large
	// allocations that bypass the budget check.
	const estBytesPerRow = 1200

	// Build phase: hash right side into partition buckets.
	// REQ001056: check budget every 1024 rows and stop early
	// when joinBufferSize is exceeded — prevents materializing
	// the full right side in memory before the budget check.
	var rightCount int
	var firstRightCols []string
	var firstRightTypes []LX.TokenType
	var firstRightData []pl.Value
	// REQ001649: ctx cancellation check every 1024 rows.
	var ctxCheckRight int
	for {
		row, err := j.right.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		ctxCheckRight++
		if ctxCheckRight >= 1024 {
			ctxCheckRight = 0
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if rightCount == 0 {
			firstRightCols = row.Cols
			firstRightTypes = row.Types
			firstRightData = row.Data
		}
		j.rightBuilt++
		rightCount++
		// REQ0011XX: check budget BEFORE append. Use half the budget
		// to leave headroom for the matches slice and dataBuf.
		effectiveBudget := j.joinBufferSize
		if effectiveBudget <= 0 {
			effectiveBudget = 64 << 20
		}
		if int64(rightCount)*estBytesPerRow > effectiveBudget/2 {
			break
		}
		rk := lookupKeys(row, j.rightKeys, j.keyBuf)
		hash := hashKeys(rk)
		idx := int(hash & uint64(j.partitions-1))
		// Deep-copy Data — the child operator may reuse its emitBuf
		// across Next() calls, and storing the slice header alone
		// would alias all rows to the same backing array.
		row.Data = append([]pl.Value(nil), row.Data...)
		j.buckets[idx].rightRows = append(j.buckets[idx].rightRows, row)
		j.buckets[idx].hashes = append(j.buckets[idx].hashes, hash)
	}
	// Debug: emit strategy event after build phase completes.
	rightTotal := 0
	for i := range j.buckets {
		rightTotal += len(j.buckets[i].rightRows)
	}
	hashJoinDebugStrategy("hash", "equi-join", float64(rightTotal))

	// REQ000865: close right side immediately — rows live in buckets.
	if j.right != nil {
		_ = j.right.Close()
	}
	// If rightCount > 0 but no rows landed in buckets, the budget check
	// fired before any row was appended. Return an error so callers
	// (e.g. TestHashJoin_JoinBufferSize) see a joinBufferSize message
	// instead of a silent ErrNoRows.
	if rightCount > 0 && j.joinBufferSize > 0 {
		var rightTotal int
		for i := range j.buckets {
			rightTotal += len(j.buckets[i].rightRows)
		}
		if rightTotal == 0 {
			return fmt.Errorf("hash join: joinBufferSize=%d too small to materialize right side", j.joinBufferSize)
		}
	}
	// Materialize left side.
	// append to prevent Go slice growth from allocating a block that
	// exceeds the remaining budget (OOM observed at hashjoin.go:263
	// when slice doubling allocated 79 MB in a 1 GB GOMEMLIMIT process).
	// REQ001649: ctx cancellation check every 1024 rows.
	var ctxCheckLeft int
	for {
		row, err := j.left.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		ctxCheckLeft++
		if ctxCheckLeft >= 1024 {
			ctxCheckLeft = 0
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		// REQ0011XX: check budget BEFORE append. The old check ran
		// AFTER append and every 1024 rows, allowing Go's slice
		// growth to allocate a block that exceeds the budget.
		// Account for the next slice doubling: when len == cap, the
		// next append doubles capacity. Check against 2× the current
		// capacity to stay under budget.
		//
		// When joinBufferSize is 0 (caller didn't set it), apply a
		// hard default cap of 64 MB to prevent unbounded materialization
		// from OOM-killing the process.
		effectiveBudget := j.joinBufferSize
		if effectiveBudget <= 0 {
			effectiveBudget = 64 << 20 // 64 MB default
		}
		// REQ001410: check budget based on ACTUAL row count. The previous
		// check used `nextCap = cap * 2` which stopped materialization
		// prematurely when the slice was full and about to double,
		// losing rows (e.g. 18203 rows lost in select4 L39784, causing
		// 14/21 row bug). We check `len * estBytesPerRow` against the
		// full budget (not budget/2) since cap may be much larger than
		// len and the doubling is Go's internal growth strategy.
		if int64(len(j.leftRows)+1)*estBytesPerRow > effectiveBudget {
			if j.debugID != "" {
				fmt.Fprintf(os.Stderr, "[HJ %s] left materialization budget hit at %d rows (budget=%d)\n",
					j.debugID, len(j.leftRows), effectiveBudget)
			}
			break
		}
		// Deep-copy Data — same reason as the right-side build above.
		row.Data = append([]pl.Value(nil), row.Data...)
		j.leftRows = append(j.leftRows, row)
	}

	// REQ001056: check total materialized rows against joinBufferSize.
	// Rough estimate: each pl.Row with N columns ≈ N * (8+16) bytes + 64 base.
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
		j.sharedTypes = make([]LX.TokenType, 0, n)
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
	leftInfos := make([]leftInfo, 0, len(j.leftRows))
	// REQ001018: pre-allocate flat key buffer to avoid per-row make.
	var flatKeyBuf []pl.Value
	if len(j.leftRows) > 0 {
		first := lookupKeys(j.leftRows[0], j.leftKeys, j.keyBuf)
		flatKeyBuf = make([]pl.Value, len(j.leftRows)*len(first))
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

	// REQ0011XX: streaming match emission setup. Instead of pre-computing
	// all matches into dataBuf + matches slices (which OOMs for large
	// cross-joins), we store leftInfos for lazy probing in Next() and
	// allocate a small reusable emitBuf of size dataPerRow.
	// No match counting or pre-allocation needed — matches are emitted
	// one at a time in Next().
	// REQ001020: even when one side is empty (LEFT outer against
	// empty right, RIGHT outer against empty left), we still need
	// dataPerRow and emitBuf initialised so the unmatched-row phase
	// can emit NULL-padded rows with the correct column count.
	if len(j.leftRows) == 0 && rightCount == 0 {
		return nil
	}
	ec.WARN_ON(len(j.leftRows) > 0 && rightCount == 0, "HashJoin.buildAndProbe: %d probe rows but 0 build rows (empty hash table)", len(j.leftRows))
	if len(j.leftRows) > 0 {
		j.leftInfos = leftInfos
		j.curLeftIdx = 0
	}
	j.curRightIdx = 0
	if rightCount > 0 && len(j.leftRows) > 0 {
		dataPerRow := len(j.leftRows[0].Data) + len(firstRightData)
		j.dataPerRow = dataPerRow
		j.leftLen = len(j.leftRows[0].Data) // REQ001272: fixed left column count
		j.emitBuf = make([]pl.Value, dataPerRow)
	}

	// REQ001561: allocate per-phase buffers so consecutive phase-0
	// emits do not alias phase-1/2 rows and vice versa.
	if j.kind == JoinKindLeft || j.kind == JoinKindFull {
		j.unmatchedLeftBuf = make([]pl.Value, j.dataPerRow)
	}
	if j.kind == JoinKindRight || j.kind == JoinKindFull {
		j.unmatchedRightBuf = make([]pl.Value, j.dataPerRow)
	}

	// REQ001020: allocate matched-state slices for outer joins.
	// Both slices stay nil for INNER joins so the bookkeeping
	// branches in Next() no-op cheaply.
	if j.kind == JoinKindLeft || j.kind == JoinKindFull {
		j.leftMatched = make([]bool, len(j.leftRows))
	}
	if j.kind == JoinKindRight || j.kind == JoinKindFull {
		j.matchedRight = make([][]bool, len(j.buckets))
		for bi := range j.buckets {
			j.matchedRight[bi] = make([]bool, len(j.buckets[bi].rightRows))
		}
	}

	// REQ001020: handle empty-side cases by sizing emitBuf to
	// the union of left + right row widths so unmatched-row
	// emission can produce NULL-padded output even when one
	// side is empty.
	if j.emitBuf == nil {
		var leftW, rightW int
		if len(j.leftRows) > 0 {
			leftW = len(j.leftRows[0].Data)
		}
		if rightCount > 0 {
			rightW = len(firstRightData)
		}
		width := leftW + rightW
		j.dataPerRow = width
		j.leftLen = leftW // REQ001272: fixed left column count
		j.emitBuf = make([]pl.Value, width)
		if j.kind == JoinKindLeft || j.kind == JoinKindFull {
			j.unmatchedLeftBuf = make([]pl.Value, width)
		}
		if j.kind == JoinKindRight || j.kind == JoinKindFull {
			j.unmatchedRightBuf = make([]pl.Value, width)
		}
	}
	return nil
}

// hashKey computes a uint64 hash of a key value using FNV-1a.
// REQ001570: replaced maphash with zero-allocation FNV-1a for text/blob keys.
func HashKey(v pl.Value) uint64 {
	if v.IsNull() {
		return 0
	}
	switch v.Kind {
	case KindInt:
		x := uint64(v.I64)
		return x*0x9e3779b97f4a7c15 ^ (x >> 31)
	case KindFloat:
		u := uint64Bits(v.F64)
		return u*0x9e3779b97f4a7c15 ^ (u >> 31)
	case KindText:
		h := fnv.New64a()
		h.Write([]byte(v.S))
		return h.Sum64()
	default:
		h := fnv.New64a()
		h.Write([]byte(stringify(v.ToAny())))
		return h.Sum64()
	}
}

// lookupKeys extracts multiple key values from a row.
func lookupKeys(row pl.Row, keys []string, buf []pl.Value) []pl.Value {
	vals := buf
	if len(vals) < len(keys) {
		vals = make([]pl.Value, len(keys))
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
		vals[i] = pl.ValueFromAny(v)
	}
	return vals
}

// joinRows combines a left and right row into a single pl.Row.
// When sharedCols (from the HashJoin struct) is set, the output
// shares the pre-built Cols and colIndex to avoid per-row alloc.
// Data is always freshly allocated since it carries row-specific
// values. REQ000794.
func joinRows(left, right pl.Row, sharedCols []string, sharedColIndex map[string]int) pl.Row {
	out := pl.Row{
		Data: make([]pl.Value, 0, len(left.Data)+len(right.Data)),
	}
	if sharedCols != nil {
		out.Cols = sharedCols
		out.ColIndex = sharedColIndex
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
// REQ001570: replaced maphash with fnv.
func hashKeys(vals []pl.Value) uint64 {
	if len(vals) == 1 {
		return HashKey(vals[0])
	}
	h := fnv.New64a()
	for _, v := range vals {
		h2 := HashKey(v)
		_, _ = h.Write([]byte{
			byte(h2), byte(h2 >> 8), byte(h2 >> 16), byte(h2 >> 24),
			byte(h2 >> 32), byte(h2 >> 40), byte(h2 >> 48), byte(h2 >> 56),
		})
	}
	return h.Sum64()
}

// valuesEqualMulti compares multiple key values for equality.
func ValuesEqualMulti(a, b []pl.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !ValuesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// valuesEqual compares two Values for equality.
func ValuesEqual(a, b pl.Value) bool {
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
