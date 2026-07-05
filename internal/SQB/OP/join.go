// Package EX's join.go hosts NestedLoopJoin. Joins are not part of the
// v1 MVP scope per docs/design/ARCH.md; the operator is retained in v1.1 for
// upcoming releases.
package OP

import (
	"context"
	"errors"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// JoinKind specifies the type of join.
type JoinKind string

const (
	JoinKindInner JoinKind = "INNER"
	JoinKindLeft  JoinKind = "LEFT"
	JoinKindRight JoinKind = "RIGHT"
	JoinKindFull  JoinKind = "FULL"
	JoinKindCross JoinKind = "CROSS"
	JoinKindSemi  JoinKind = "SEMI"
)

type NestedLoopJoin struct {
	left      Operator
	right     Operator
	leftTbl   string
	rightTbl  string
	on        func(outer, inner *Row) (bool, error)
	kind      JoinKind
	leftOuter bool // REQ000685: true for LEFT/LEFT OUTER JOIN
	// REQ000743: true for RIGHT/RIGHT OUTER JOIN
	rightOuter bool
	// rightMode indicates the right side has been materialized for
	// RIGHT/FULL OUTER JOIN processing. When true, j.rightRows holds
	// the materialized rows and the main loop iterates over them
	// instead of re-scanning the right operator per left row.
	rightMode    bool
	rightMatched []bool // tracks which materialized right rows matched (for RIGHT/FULL)
	rightEmitted bool   // true after unmatched right rows have been emitted
	leftRow      *Row
	rightPos     int
	rightRows    []Row
	matched      bool // for OUTER JOIN: track if left row found a match
	// REQ000803: column projection pushdown — only these columns
	// (if non-nil) are included in output rows. Columns are stored
	// as "table.col" pairs (e.g., ["t1.id", "t2.a", "t3.b"]).
	projectedCols []string
	// projectedLayout maps each projected column to (leftIdx, rightIdx)
	// where leftIdx ≥ 0 means the column comes from the left side,
	// rightIdx ≥ 0 from the right side. Built once in tryHashCrossJoin
	// or on first NLJ row.
	projectedLayout [][2]int // [][leftIdx, rightIdx], -1 if not on that side
	// REQ000800: hash-based cross join for small DT.Tables.
	// When both sides fit in memory, materialize both and
	// do a hash-based cross product instead of NLJ.
	leftRows      []Row
	hashMode      bool
	hashAttempted bool             // set true after tryHashCrossJoin fails; prevents O(N²) re-entry
	hashBuckets   map[uint64][]int // hash → indices into rightRows
	leftIdx       int
	rightIdx      int
	leftMatched   []bool // for LEFT JOIN
	// sharedColIndex is built lazily from a sample left+right row's
	// column layouts and reused across all emitted rows so the
	// downstream Filter/Eval doesn't have to call BuildColIndex
	// per row. j3 perf: ~2.7GB of allocations eliminated per
	// 1M-row benchmark run.
	sharedColIndex map[string]int
	// sharedCols and sharedTypes are pre-built from materialized
	// left+right column layouts and shared across all hash-mode
	// output rows instead of allocating fresh slices per row.
	sharedCols  []string
	sharedTypes []LX.TokenType
	// dataBuf is a single pre-allocated []any covering all output
	// rows' Data slices. Each row gets a non-overlapping sub-slice
	// [off:off:off+dataPerRow], eliminating per-row make([]Value, ...)
	// allocations. j3 perf: 39% of alloc_space was here.
	dataBuf    []Value
	dataPerRow int
	dataOffset int // running write offset into dataBuf
	// REQ000798: Block NLJ mode — batch left rows and re-scan right
	// per batch. Used when hash mode is unavailable (left > 4096
	// or ON clause exists). Reduces right-side scans from N to N/64.
	blockMode    bool
	blkLeftBatch []Row
	blkLeftPos   int   // position within left batch
	blkRightRows []Row // materialized right side (recreated per batch)
	blkRightPos  int
	blkResultBuf []Row // buffered matches for current batch
	blkResultPos int   // cursor into blkResultBuf
	// REQ000816: blkSharedCols/ColIndex built lazily per batch,
	// reused across all emit rows in the batch to skip per-row
	// BuildColIndex in downstream Lookup.
	// REQ000802+: blkDataBuf/blkDataPerRow/blkDataOffset provide
	// a shared data buffer for block-mode output rows, eliminating
	// per-row make([]Value) allocations.
	blkSharedCols     []string
	blkSharedTypes    []LX.TokenType
	blkSharedColIndex map[string]int
	// REQ000877: sharedBuilt guards blkSharedCols/Types/colIndex
	// — computed once on first batch, reused across all subsequent.
	sharedBuilt   bool
	blkDataBuf    []Value
	blkDataPerRow int
	blkDataOffset int
	// REQ000847: limitRemaining tells this NLJ to stop early when a
	// LIMIT is present above in the plan tree. Set by SetLimit from
	// the planner. Prevents the join from producing more rows than
	// necessary when only the first few are needed (e.g. LIMIT 10
	// on a 5-table cross producing 10^10 rows).
	limitRemaining int64
	// totalEmitted counts how many rows this NLJ has emitted so far.
	// Used in conjunction with limitRemaining to stop batch loops
	// early. REQ000847.
	totalEmitted int64
	// REQ000863: pre-computed prefixed column lists. Computed once
	// on first nextBlock/Next call and reused across all batches,
	// eliminating the per-row/ per-batch hasAnyPrefix + prefixCols
	// overhead. For a 5-table cross (3125 batches), this avoids
	// 6250 calls to prefixCols (5 allocs each = 31250 saved allocs).
	leftPrefixedCols  []string
	rightPrefixedCols []string
	// REQ000873: cachedRightRows holds the materialized right side
	// when it is small (< 64 rows). Reused across batches to avoid
	// re-scanning the right operator per batch. Only populated when
	// the right side fits entirely in memory.
	cachedRightRows  []Row
	cachedRightCols  []string // pre-built prefixed cols for cached rows
	cachedRightTypes []int    // pre-built types for cached rows
	rightCached      bool     // true once the cache is populated
	// rowID counter for debug tracing — incremented for each row
	// processed by the join operator.
	rowID uint64
	// REQ000870: pre-computed shared Cols/Types/colIndex for outer-join
	// emit paths. Computed lazily from the first left+right row pair.
	// Used by joinRowsLLWithCols to avoid per-row make([]string) and
	// make([]int) allocations in right-outer and left-outer paths.
	outerSharedCols     []string
	outerSharedTypes    []LX.TokenType
	outerSharedColIndex map[string]int
	// REQ000870: cached null-row templates to avoid rebuilding Cols/Types
	// per call in nullRightRow/nullLeftRow.
	nullRightRowCache *Row
	nullLeftRowCache  *Row
}

// emitLimitCheck is called before returning a row from Next().
// It increments the totalEmitted counter and returns the row
// unmodified. REQ000847.
func (j *NestedLoopJoin) emitLimitCheck(row Row) Row {
	if j.limitRemaining > 0 {
		j.totalEmitted++
	}
	return row
}

// outerJoinRows is a convenience wrapper that calls ensureOuterShared
// with the given row pair (to populate the shared Cols/Types template)
// and then delegates to joinRowsLLWithCols. REQ000870: eliminates
// per-row make([]string) and make([]int) in outer-join emit paths.
func (j *NestedLoopJoin) outerJoinRows(a, b *Row) Row {
	if j.outerSharedCols == nil {
		j.ensureOuterShared(a, b)
	}
	return joinRowsLLWithCols(a, b, j.outerSharedCols, j.outerSharedTypes, j.outerSharedColIndex)
}

// ensureOuterShared builds pre-computed shared Cols/Types/colIndex
// for outer-join emit paths from the first left row and right row
// (or nullRow fallback). REQ000870: used by outerJoinRows to avoid
// per-row make([]string) and make([]int) allocations.
// REQ001097: skip the build when WithSharedSchema has pre-populated
// the schema fields.
func (j *NestedLoopJoin) ensureOuterShared(leftFirst, rightFirst *Row) {
	if j.outerSharedCols != nil || j.sharedBuilt {
		return
	}
	lc, rc := leftFirst.Cols, rightFirst.Cols
	j.outerSharedCols = make([]string, 0, len(lc)+len(rc))
	j.outerSharedCols = append(j.outerSharedCols, lc...)
	j.outerSharedCols = append(j.outerSharedCols, rc...)
	lt, rt := leftFirst.Types, rightFirst.Types
	j.outerSharedTypes = make([]LX.TokenType, 0, len(lt)+len(rt))
	j.outerSharedTypes = append(j.outerSharedTypes, lt...)
	j.outerSharedTypes = append(j.outerSharedTypes, rt...)
	j.outerSharedColIndex = make(map[string]int, len(j.outerSharedCols))
	for i, c := range j.outerSharedCols {
		key := strings.ToLower(c)
		if _, exists := j.outerSharedColIndex[key]; !exists {
			j.outerSharedColIndex[key] = i
		}
	}
}

// WithProjection sets the projected columns for the join output.
// REQ000803: when set, only these columns are included in output rows,
// reducing per-row memory and CPU for downstream operators.
func (j *NestedLoopJoin) WithProjection(projectedCols []string) *NestedLoopJoin {
	j.projectedCols = projectedCols
	return j
}

// WithSharedSchema pre-computes the output schema (cols, types, colIndex)
// for this join. REQ001097: when set, the runtime paths (tryHashCrossJoin,
// nextBlock, outerJoinRows) skip their per-operator schema build phase,
// avoiding redundant allocation across an NLJ chain.
func (j *NestedLoopJoin) WithSharedSchema(cols []string, types []LX.TokenType, colIndex map[string]int) *NestedLoopJoin {
	colsCopy := append([]string(nil), cols...)
	typesCopy := append([]LX.TokenType(nil), types...)
	colIdx := make(map[string]int, len(colIndex))
	for k, v := range colIndex {
		colIdx[k] = v
	}
	j.sharedCols = colsCopy
	j.sharedTypes = typesCopy
	j.sharedColIndex = colIdx
	// Same triplet for block-mode and outer-join emit paths.
	j.blkSharedCols = colsCopy
	j.blkSharedTypes = typesCopy
	j.blkSharedColIndex = colIdx
	j.outerSharedCols = colsCopy
	j.outerSharedTypes = typesCopy
	j.outerSharedColIndex = colIdx
	j.sharedBuilt = true
	return j
}

func NewNestedLoopJoin(left, right Operator, leftTable, rightTable string, on func(outer, inner *Row) (bool, error), kind JoinKind) *NestedLoopJoin {
	const batchSize = 32
	return &NestedLoopJoin{
		left:       left,
		right:      right,
		leftTbl:    leftTable,
		rightTbl:   rightTable,
		on:         on,
		kind:       kind,
		leftOuter:  kind == JoinKindLeft || kind == JoinKindFull,
		rightOuter: kind == JoinKindRight || kind == JoinKindFull,
		// REQ000881: pre-allocate batch and result buffers to avoid
		// first-call heap escape of Row literals in the batch-fill loop
		// and repeated append growth in the matching loop.
		blkLeftBatch: make([]Row, 0, batchSize),
		blkRightRows: make([]Row, 0, batchSize),
		blkResultBuf: make([]Row, 0, batchSize),
		blkDataBuf:   make([]Value, 0, 64),
	}
}

// SetLimit sets a row limit on this join. After producing `n` rows,
// Next() returns ErrNoRows. REQ000847.
func (j *NestedLoopJoin) SetLimit(n int64) { j.limitRemaining = n }

func (j *NestedLoopJoin) LeftChild() Operator { return j.left }
func (j *NestedLoopJoin) SetLeft(c Operator)  { j.left = c }

func (j *NestedLoopJoin) RightChild() Operator { return j.right }
func (j *NestedLoopJoin) SetRight(c Operator)  { j.right = c }

func (j *NestedLoopJoin) LeftTbl() string { return j.leftTbl }

func (j *NestedLoopJoin) RightTbl() string { return j.rightTbl }

// Kind returns the join kind (INNER, LEFT, RIGHT, FULL, CROSS, SEMI).
func (j *NestedLoopJoin) Kind() JoinKind { return j.kind }

func (j *NestedLoopJoin) SharedCols() []string { return j.sharedCols }

func (j *NestedLoopJoin) SharedTypes() []LX.TokenType { return j.sharedTypes }

func (j *NestedLoopJoin) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if j.limitRemaining > 0 && j.totalEmitted >= j.limitRemaining {
		return Row{}, ErrNoRows
	}
	// REQ001235: semi-join — for each left row, probe the right side
	// until a match is found, then return the left row immediately.
	// Unlike INNER/LEFT/RIGHT/FULL, semi-join does NOT concatenate
	// left and right columns — it emits only the left row.
	if j.kind == JoinKindSemi {
		for {
			if j.leftRow == nil {
				row, err := j.left.Next(ctx)
				if err != nil {
					if err == ErrNoRows {
						return Row{}, ErrNoRows
					}
					return Row{}, err
				}
				j.leftRow = &row
			}
			// Probe the right side for this left row
			for {
				r, err := j.right.Next(ctx)
				if err != nil {
					if err == ErrNoRows {
						// Exhausted right — advance to next left row
						j.leftRow = nil
						break
					}
					return Row{}, err
				}
				if j.on != nil {
					ok, oerr := j.on(j.leftRow, &r)
					if oerr != nil {
						return Row{}, oerr
					}
					if !ok {
						continue
					}
				}
				// Match found — return left row, re-enter right on next call
				result := *j.leftRow
				j.leftRow = nil
				return j.emitLimitCheck(result), nil
			}
		}
	}
	// REQ000743/744: for RIGHT/FULL OUTER JOIN, materialize the right
	// side on first call so we can track which rows matched.
	if j.rightOuter && !j.rightMode && j.leftRow == nil && len(j.rightRows) == 0 {
		if err := j.materializeRightForOuter(ctx); err != nil {
			return Row{}, err
		}
	}
	// REQ000743/744: after exhausting the left side, emit unmatched
	// right rows with NULL-padded left columns.
	if j.rightOuter && j.rightEmitted {
		return Row{}, ErrNoRows
	}
	if j.rightOuter && j.leftRow == nil && !j.rightEmitted {
		// Check if we have already exhausted all left rows.
		// Try to get the next left row; if none, switch to
		// right-outer emission phase.
		row, lerr := j.left.Next(ctx)
		if lerr != nil {
			if lerr == ErrNoRows {
				return j.emitUnmatchedRight(), nil
			}
			return Row{}, lerr
		}
		prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		prefixed.TableName = row.TableName
		if !hasAnyPrefix(row.Cols) {
			prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
		} else {
			prefixed.Cols = append([]string(nil), row.Cols...)
		}
		j.leftRow = &prefixed
		j.rightPos = 0
		j.matched = false
		// Iterate over materialized right rows.
		for j.rightPos < len(j.rightRows) {
			r := j.rightRows[j.rightPos]
			j.rightPos++
			r.Outer = j.leftRow
			if j.on != nil {
				ok, err := j.on(j.leftRow, &r)
				if err != nil {
					return Row{}, err
				}
				if !ok {
					continue
				}
			}
			j.matched = true
			j.rightMatched[j.rightPos-1] = true
			return j.emitLimitCheck(j.outerJoinRows(j.leftRow, &r)), nil
		}
		// No matches for this left row.
		if j.leftOuter && !j.matched {
			nullRow := j.nullRightRow()
			result := j.outerJoinRows(j.leftRow, &nullRow)
			j.leftRow = nil
			return j.emitLimitCheck(result), nil
		}
		j.leftRow = nil
		return j.Next(ctx)
	}
	// REQ000800: try hash cross join on first call for small DT.Tables.
	// Guard includes leftRow == nil to avoid re-entering tryHashCrossJoin
	// mid-NLJ iteration — after an abort the operator is partway through
	// the NLJ loop and re-entering would re-drain the left child on
	// every Next() call (catastrophic for large cross products).
	// hashAttempted prevents O(N²) re-entry when left has >1024 rows:
	// without it, every time leftRow becomes nil the guard passes,
	// re-draining the left child each time.
	if !j.hashMode && !j.hashAttempted && j.leftRows == nil && j.rightRows == nil && j.leftRow == nil {
		// REQ000843: skip HashCrossJoin when either side is already
		// a join operator — the bushy group already has all rows
		// materialized and HashCrossJoin would re-materialize them.
		if !isJoinOp(j.left) && !isJoinOp(j.right) {
			if j.tryHashCrossJoin(ctx) {
				// Switched to hash mode — continue with hash iteration.
			}
		} else {
			j.hashAttempted = true
		}
	}
	if j.hashMode {
		return j.nextHash(ctx)
	}
	// REQ000798: block NLJ mode activates when hash mode is
	// unavailable. Batches left rows and re-scans the right side
	// once per batch, reducing scans from N to N/32.
	if !j.blockMode && j.leftRow == nil && len(j.blkLeftBatch) == 0 {
		j.blockMode = true
		nljDebugStrategy("nested_loop", "no equi-join keys", 0)
	}
	if j.blockMode {
		return j.nextBlock(ctx)
	}
	for {
		if j.leftRow == nil {
			row, err := j.left.Next(ctx)
			if err != nil {
				return Row{}, err
			}
			// REQ000725: if the row's columns are already
			// prefixed (e.g. it came from a previous join
			// in a multi-table chain), don't re-prefix — that
			// produces "t29.t51.a51" etc. The NLJ only
			// prefixes the right side; the left side keeps
			// whatever prefix it already has.
			prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
			prefixed.TableName = row.TableName
			if !hasAnyPrefix(row.Cols) {
				prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
			} else {
				prefixed.Cols = append([]string(nil), row.Cols...)
			}
			j.leftRow = &prefixed
			j.rowID++
			nljDebugRowFlow(j.leftTbl, j.rowID, true)
			// REQ000368: drive the right side through its own
			// operator rather than the in-memory `DT.Tables` map.
			// The in-memory map is empty for store-backed
			// DT.Tables, which caused CROSS JOIN (and any JOIN
			// of store-backed DT.Tables) to return zero rows.
			j.rightPos = -1
			j.matched = false
		}
		// Advance the right side. Each call to j.right.Next()
		// yields the next row; we re-init when we've exhausted
		// the right side and need to move to the next left row.
		inner, err := j.advanceRight(ctx)
		if err != nil {
			if err == errRightExhausted {
				// No more right rows for this left row
				if j.leftOuter {
					if !j.matched {
						nullRow := j.nullRightRow()
						result := j.outerJoinRows(j.leftRow, &nullRow)
						j.leftRow = nil
						return j.emitLimitCheck(result), nil
					}
				}
				j.leftRow = nil
				continue
			}
			return Row{}, err
		}
		if !hasAnyPrefix(inner.Cols) {
			inner.Cols = prefixCols(inner.Cols, j.rightTbl)
		}
		inner.Outer = j.leftRow
		j.rowID++
		nljDebugRowFlow(j.rightTbl, j.rowID, true)
		if j.on != nil {
			ok, err := j.on(j.leftRow, &inner)
			if err != nil {
				return Row{}, err
			}
			nljDebugPredicate("on", j.rowID-1, j.rowID, ok)
			if !ok {
				continue
			}
		}
		j.matched = true
		j.rowID++
		nljDebugRowFlow("output", j.rowID, false)
		return j.emitLimitCheck(j.outerJoinRows(j.leftRow, &inner)), nil
	}
}

// tryHashCrossJoin attempts to switch to hash mode. Returns true
// if hash mode was activated. REQ000800.
func (j *NestedLoopJoin) tryHashCrossJoin(ctx context.Context) bool {
	// Only for INNER/CROSS without ON clause (pure cross product).
	if j.on != nil || j.leftOuter || j.rightOuter {
		return false
	}
	// REQ001093: skip hash mode for pure cross-joins (no ON clause).
	// Block NLJ streaming avoids OOM from full Cartesian product
	// materialization. Hash mode is only beneficial for equi-join
	// ON conditions (hash probe), but for cross-joins it just does
	// a nested loop over leftRows x rightRows, pre-allocating
	// dataBuf = O(N^2) which triggers OOM for large DT.Tables.
	j.hashAttempted = true
	return false
}

// nextHash produces the next row from the materialized hash join.
// REQ000800. REQ001094: early-exit when limit is satisfied to
// avoid emitting rows that the Limit operator above would discard.
func (j *NestedLoopJoin) nextHash(_ context.Context) (Row, error) {
	if j.limitRemaining > 0 && j.totalEmitted >= j.limitRemaining {
		j.leftRows = nil
		j.rightRows = nil
		return Row{}, ErrNoRows
	}
	for j.leftIdx < len(j.leftRows) {
		for j.rightIdx < len(j.rightRows) {
			l := j.leftRows[j.leftIdx]
			r := j.rightRows[j.rightIdx]
			j.rightIdx++
			// Carve a non-overlapping sub-slice from the
			// pre-allocated dataBuf. The backing array is shared
			// across all output rows but each sub-slice is
			// disjoint — safe because downstream copies values
			// via rows.Scan before the next row is fetched.
			off := j.dataOffset
			j.dataOffset += j.dataPerRow
			out := Row{
				Cols:     j.sharedCols,
				Types:    j.sharedTypes,
				Data:     j.dataBuf[off : off : off+j.dataPerRow],
				ColIndex: j.sharedColIndex,
			}
			out.Data = append(out.Data, l.Data...)
			out.Data = append(out.Data, r.Data...)
			return j.emitLimitCheck(out), nil
		}
		j.rightIdx = 0
		j.leftIdx++
	}
	// Exhausted.
	j.leftRows = nil
	j.rightRows = nil
	return Row{}, ErrNoRows
}

// errRightExhausted is the sentinel returned by advanceRight when
// the right-side operator has no more rows for the current left row.
var errRightExhausted = errors.New("ex: right side exhausted")

// advanceRight returns the next row from the right-side operator.
// When the right side is exhausted (ErrNoRows), it returns
// errRightExhausted so the outer loop can move to the next left
// row. The right side is reset (Close + re-Next) on each new left
// row so the right's iterator state is rewound.
func (j *NestedLoopJoin) advanceRight(ctx context.Context) (Row, error) {
	if j.rightPos == -1 {
		// First call for this left row: close + reopen the
		// right side so its iterator state is fresh.
		_ = j.right.Close()
		j.rightPos = 0
	}
	row, err := j.right.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			// Mark so the next call resets the iterator.
			j.rightPos = -1
			return Row{}, errRightExhausted
		}
		return Row{}, err
	}
	return row, nil
}

// nullRightRow returns a row with all NULL values for the right table schema.
func (j *NestedLoopJoin) nullRightRow() Row {
	// REQ000870: return cached template if available (all Data is NilValue).
	if j.nullRightRowCache != nil {
		return *j.nullRightRowCache
	}
	DT.TablesMu.RLock()
	rightSchema := DT.Tables[j.rightTbl]
	DT.TablesMu.RUnlock()

	nullRow := Row{
		Cols:  prefixCols(schemaCols(rightSchema), j.rightTbl),
		Types: schemaTypes(rightSchema),
		Data:  make([]Value, len(rightSchema)),
	}
	// Cache for reuse (copy the slice header — Data values are all zero = NULL).
	j.nullRightRowCache = &nullRow
	return nullRow
}

// nullLeftRow returns a row with all NULL values for the left table schema.
// REQ000743: used by RIGHT/FULL OUTER JOIN when a right row has no match.
func (j *NestedLoopJoin) nullLeftRow() Row {
	// REQ000870: return cached template if available.
	if j.nullLeftRowCache != nil {
		return *j.nullLeftRowCache
	}
	DT.TablesMu.RLock()
	leftSchema := DT.Tables[j.leftTbl]
	DT.TablesMu.RUnlock()

	nullRow := Row{
		Cols:  prefixCols(schemaCols(leftSchema), j.leftTbl),
		Types: schemaTypes(leftSchema),
		Data:  make([]Value, len(leftSchema)),
	}
	j.nullLeftRowCache = &nullRow
	return nullRow
}

// materializeRightForOuter reads the entire right side into j.rightRows
// and initializes j.rightMatched. Used by RIGHT/FULL OUTER JOIN.
// REQ000743.
func (j *NestedLoopJoin) materializeRightForOuter(ctx context.Context) error {
	j.rightMode = true
	j.rightMatched = nil
	j.rightEmitted = false
	j.rightRows = make([]Row, 0, 64)
	for {
		row, err := j.right.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return err
		}
		j.rightRows = append(j.rightRows, Row{
			Cols:      prefixCols(row.Cols, j.rightTbl),
			Types:     row.Types,
			Data:      append([]Value(nil), row.Data...),
			TableName: row.TableName,
		})
	}
	j.rightMatched = make([]bool, len(j.rightRows))
	return nil
}

// emitUnmatchedRight returns the next unmatched right row with
// NULL-padded left columns. Sets rightEmitted=true when done.
// REQ000743.
func (j *NestedLoopJoin) emitUnmatchedRight() Row {
	for i, matched := range j.rightMatched {
		if !matched {
			j.rightMatched[i] = true
			nullLeft := j.nullLeftRow()
			r := j.rightRows[i]
			r.Outer = &nullLeft
			return j.emitLimitCheck(j.outerJoinRows(&nullLeft, &r))
		}
	}
	j.rightEmitted = true
	j.rightMode = false
	return Row{}
}

func (j *NestedLoopJoin) Close() error {
	j.hashMode = false
	j.hashAttempted = false
	j.leftRow = nil
	j.leftRows = nil
	j.rightRows = nil
	j.rightMode = false
	j.rightMatched = nil
	j.rightEmitted = false
	j.hashBuckets = nil
	j.sharedColIndex = nil
	j.sharedCols = nil
	j.sharedTypes = nil
	j.dataBuf = nil
	j.dataPerRow = 0
	j.dataOffset = 0
	j.rightPos = 0
	j.leftIdx = 0
	j.rightIdx = 0
	j.blockMode = false
	j.blkLeftBatch = nil
	j.blkLeftPos = 0
	j.blkRightRows = nil
	j.blkRightPos = 0
	j.blkResultBuf = nil
	j.blkResultPos = 0
	j.blkSharedCols = nil
	j.blkSharedTypes = nil
	j.blkSharedColIndex = nil
	j.blkDataBuf = nil
	j.blkDataPerRow = 0
	j.blkDataOffset = 0
	j.leftPrefixedCols = nil
	j.rightPrefixedCols = nil
	j.cachedRightRows = nil
	j.cachedRightCols = nil
	j.cachedRightTypes = nil
	j.rightCached = false
	j.sharedBuilt = false
	j.outerSharedCols = nil
	j.outerSharedTypes = nil
	j.outerSharedColIndex = nil
	_ = j.left.Close()
	return j.right.Close()
}

// nextBlock implements Block Nested-Loop Join (REQ000798). Batches
// up to batchSize left rows, materializes right side, and joins them.
// Output rows carry the shared colIndex so downstream Lookup can
// skip per-row BuildColIndex (REQ000816).
func (j *NestedLoopJoin) nextBlock(ctx context.Context) (Row, error) {
	const batchSize = 64
	// REQ000847: early exit — if limit is already satisfied, skip
	// all batch setup (fill left, materialize right, build shared
	// cols). Without this, LIMIT 10 over a 5-table cross still
	// pays the full batch-setup cost on every Next() call after
	// the limit is reached.
	if j.limitRemaining > 0 && j.totalEmitted >= j.limitRemaining {
		return Row{}, ErrNoRows
	}
	// Drain result buffer.
	if j.blkResultPos < len(j.blkResultBuf) {
		r := j.blkResultBuf[j.blkResultPos]
		j.blkResultPos++
		return j.emitLimitCheck(r), nil
	}
	j.blkResultBuf = j.blkResultBuf[:0]
	j.blkResultPos = 0

	// Fill left batch (up to batchSize rows).
	j.blkLeftBatch = j.blkLeftBatch[:0]
	// REQ000863: compute left prefixed columns once per operator
	// lifetime. All rows from the same SeqScan share the same Cols.
	if j.leftPrefixedCols == nil {
		if firstRow, err := j.left.Next(ctx); err == nil {
			if !hasAnyPrefix(firstRow.Cols) {
				j.leftPrefixedCols = prefixCols(firstRow.Cols, j.leftTbl)
			} else {
				j.leftPrefixedCols = append([]string(nil), firstRow.Cols...)
			}
			prefixed := Row{Types: firstRow.Types, Data: firstRow.Data, Outer: firstRow.Outer}
			prefixed.TableName = firstRow.TableName
			prefixed.Cols = j.leftPrefixedCols
			j.blkLeftBatch = append(j.blkLeftBatch, prefixed)
		}
	}
	for len(j.blkLeftBatch) < batchSize {
		row, err := j.left.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		prefixed.TableName = row.TableName
		prefixed.Cols = j.leftPrefixedCols
		j.blkLeftBatch = append(j.blkLeftBatch, prefixed)
	}
	if len(j.blkLeftBatch) == 0 {
		// Cleanup on exit.
		j.blockMode = false
		return Row{}, ErrNoRows
	}

	// Materialize right side (Close + re-scan once per batch).
	// REQ000873: if right side is small (< 64 rows), cache it for
	// reuse across batches instead of re-scanning.
	if !j.rightCached {
		_ = j.right.Close()
		j.blkRightRows = j.blkRightRows[:0]
		// REQ000863: compute right prefixed columns once. All rows from
		// the same scan share the same Cols, so prefixCols is identical.
		// REQ000861: check hasAnyPrefix first to avoid double-prefixing
		// when the right side is already a join operator (bushy merge).
		// Without this, "t3.a" becomes "t4.t3.a" and column lookups
		// via qualified name ("t4.d") fail to match, causing wrong
		// predicate evaluation and incorrect OR filter results.
		if j.rightPrefixedCols == nil {
			if firstRow, err := j.right.Next(ctx); err == nil {
				if !hasAnyPrefix(firstRow.Cols) {
					j.rightPrefixedCols = prefixCols(firstRow.Cols, j.rightTbl)
				} else {
					j.rightPrefixedCols = append([]string(nil), firstRow.Cols...)
				}
				inner := Row{Types: firstRow.Types, Data: firstRow.Data, Outer: firstRow.Outer}
				inner.TableName = firstRow.TableName
				inner.Cols = j.rightPrefixedCols
				j.blkRightRows = append(j.blkRightRows, inner)
			}
		}
		for {
			row, err := j.right.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				return Row{}, err
			}
			inner := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
			inner.TableName = row.TableName
			inner.Cols = j.rightPrefixedCols
			j.blkRightRows = append(j.blkRightRows, inner)
		}
		// REQ000873: if right side is small, cache it for reuse.
		// REQ000949: for cross-joins (no ON clause), always cache.
		const tinyRightThreshold = 256
		if len(j.blkRightRows) <= tinyRightThreshold || j.on == nil {
			j.cachedRightRows = make([]Row, len(j.blkRightRows))
			copy(j.cachedRightRows, j.blkRightRows)
			j.cachedRightCols = j.rightPrefixedCols
			j.cachedRightTypes = nil // Types are on the rows themselves
			j.rightCached = true
		}
	} else {
		// REQ000884: use cached right rows directly — no copy needed.
		// The cached rows are never modified; the matching loop's
		// `r.Outer = &l` assigns to a range-copy, not the cached element.
		j.blkRightRows = j.cachedRightRows
	}

	// REQ000877: build blkSharedCols/blkSharedTypes/blkSharedColIndex
	// once per operator lifetime — column layout is constant across
	// batches since left/right Cols are fixed for the join's lifetime.
	if !j.sharedBuilt && len(j.blkLeftBatch) > 0 && len(j.blkRightRows) > 0 {
		lCols := j.blkLeftBatch[0].Cols
		rCols := j.blkRightRows[0].Cols
		lTypes := j.blkLeftBatch[0].Types
		rTypes := j.blkRightRows[0].Types
		j.blkSharedCols = make([]string, 0, len(lCols)+len(rCols))
		j.blkSharedCols = append(j.blkSharedCols, lCols...)
		j.blkSharedCols = append(j.blkSharedCols, rCols...)
		j.blkSharedTypes = make([]LX.TokenType, 0, len(lTypes)+len(rTypes))
		j.blkSharedTypes = append(j.blkSharedTypes, lTypes...)
		j.blkSharedTypes = append(j.blkSharedTypes, rTypes...)
		j.blkSharedColIndex = make(map[string]int, len(j.blkSharedCols))
		for i, c := range j.blkSharedCols {
			key := strings.ToLower(c)
			if _, exists := j.blkSharedColIndex[key]; !exists {
				j.blkSharedColIndex[key] = i
			}
		}
		j.sharedBuilt = true
	}

	// Match all left batch rows against right rows, emitting in
	// left-primary order so LEFT JOIN results match NLJ ordering.
	// REQ000802+: use blkDataBuf to eliminate per-row Data allocations.
	if len(j.blkRightRows) == 0 {
		// Right side empty — no matches possible; move to next batch.
		j.blkLeftBatch = j.blkLeftBatch[:0]
		return j.nextBlock(ctx)
	}
	blkDataPerRow := len(j.blkLeftBatch[0].Data) + len(j.blkRightRows[0].Data)
	maxMatches := len(j.blkLeftBatch) * len(j.blkRightRows)
	// Ensure blkDataBuf has enough capacity for this batch.
	if cap(j.blkDataBuf) < maxMatches*blkDataPerRow {
		j.blkDataBuf = make([]Value, 0, maxMatches*blkDataPerRow)
	}
	// Reset length for this batch (capacity retained across batches).
	j.blkDataBuf = j.blkDataBuf[:0]
	for _, l := range j.blkLeftBatch {
		matched := false
		j.rowID++
		leftID := j.rowID
		nljDebugRowFlow(j.leftTbl, leftID, true)
		for _, r := range j.blkRightRows {
			r.Outer = &l
			j.rowID++
			rightID := j.rowID
			nljDebugRowFlow(j.rightTbl, rightID, true)
			if j.on != nil {
				ok, err := j.on(&l, &r)
				if err != nil {
					return Row{}, err
				}
				nljDebugPredicate("on", leftID, rightID, ok)
				if !ok {
					continue
				}
			}
			matched = true
			j.rowID++
			nljDebugRowFlow("output", j.rowID, false)
			// Extend buffer by exactly blkDataPerRow for this row.
			off := len(j.blkDataBuf)
			j.blkDataBuf = j.blkDataBuf[:off+blkDataPerRow]
			dataSlice := j.blkDataBuf[off : off+blkDataPerRow : off+blkDataPerRow]
			result := Row{
				Cols:     j.blkSharedCols,
				Types:    j.blkSharedTypes,
				Data:     dataSlice,
				ColIndex: j.blkSharedColIndex,
			}
			// Fill the data slice directly.
			copy(result.Data, l.Data)
			copy(result.Data[len(l.Data):], r.Data)
			j.blkResultBuf = append(j.blkResultBuf, result)
			// REQ000847: stop filling blkResultBuf early when limit
			// is set. Prevents over-producing rows that the Limit
			// operator above will discard anyway.
			if j.limitRemaining > 0 && int64(len(j.blkResultBuf)) >= j.limitRemaining {
				j.blkLeftBatch = j.blkLeftBatch[:0]
				j.blkRightRows = j.blkRightRows[:0]
				goto matchDone
			}
		}
		if !matched && j.leftOuter {
			nullRow := j.nullRightRow()
			off := len(j.blkDataBuf)
			j.blkDataBuf = j.blkDataBuf[:off+blkDataPerRow]
			dataSlice := j.blkDataBuf[off : off+blkDataPerRow : off+blkDataPerRow]
			result := Row{
				Cols:     j.blkSharedCols,
				Types:    j.blkSharedTypes,
				Data:     dataSlice,
				ColIndex: j.blkSharedColIndex,
			}
			copy(result.Data, l.Data)
			copy(result.Data[len(l.Data):], nullRow.Data)
			j.blkResultBuf = append(j.blkResultBuf, result)
			if j.limitRemaining > 0 && int64(len(j.blkResultBuf)) >= j.limitRemaining {
				j.blkLeftBatch = j.blkLeftBatch[:0]
				j.blkRightRows = j.blkRightRows[:0]
				goto matchDone
			}
		}
	}
matchDone:
	nljDebugCorrelation(1, []string{j.leftTbl, j.rightTbl}, int64(len(j.blkResultBuf)))

	if len(j.blkResultBuf) == 0 {
		// No matches in this batch — try next batch.
		j.blkLeftBatch = j.blkLeftBatch[:0]
		return j.nextBlock(ctx)
	}
	j.blkResultPos = 1
	return j.blkResultBuf[0], nil
}

// joinRowsLL combines two rows into one output row. Callers that
// emit rows in a tight loop must pass `sharedColIndex` and
// `sharedCols` so the downstream Eval path can skip BuildColIndex
// per row (REQ000816). Passing nil for colIndex is allowed but
// forces a per-row colIndex build downstream.
func joinRowsLL(a, b *Row) Row {
	return joinRowsLLWithCols(a, b, nil, nil, nil)
}

// joinRowsLLWithCols is the full-form variant: caller supplies
// pre-built sharedCols, sharedTypes and sharedColIndex to avoid
// per-row allocation in the hot NLJ/HashCrossJoin path. When
// sharedCols is non-nil, out.Cols/Types share the slice (no copy).
// Data is always freshly allocated since it's per-row payload.
func joinRowsLLWithCols(a, b *Row, sharedCols []string, sharedTypes []LX.TokenType, sharedColIndex map[string]int) Row {
	out := Row{
		ColIndex: sharedColIndex,
	}
	if sharedCols != nil {
		out.Cols = sharedCols
	} else {
		nCols := len(a.Cols) + len(b.Cols)
		out.Cols = make([]string, 0, nCols)
		out.Cols = append(out.Cols, a.Cols...)
		out.Cols = append(out.Cols, b.Cols...)
	}
	if sharedTypes != nil {
		out.Types = sharedTypes
	} else {
		nTypes := len(a.Types) + len(b.Types)
		out.Types = make([]LX.TokenType, 0, nTypes)
		out.Types = append(out.Types, a.Types...)
		out.Types = append(out.Types, b.Types...)
	}
	nData := len(a.Data) + len(b.Data)
	out.Data = make([]Value, 0, nData)
	out.Data = append(out.Data, a.Data...)
	out.Data = append(out.Data, b.Data...)
	return out
}

// joinRowsProjected produces a join output row containing only the
// projected columns specified by layout. Each entry in layout is
// (leftIdx, rightIdx); exactly one is ≥0. REQ000803.
// Data is pre-allocated via the caller-supplied dataBuf slice; the
// function returns a Row with Data pointing into dataBuf.
func joinRowsProjected(a, b *Row, cols []string, types []LX.TokenType, colIndex map[string]int, layout [][2]int, dataBuf *[]Value) Row {
	off := len(*dataBuf)
	n := len(cols)
	// Ensure dataBuf has room for n Values.
	required := off + n
	if cap(*dataBuf) < required {
		newCap := cap(*dataBuf) * 2
		if newCap < required {
			newCap = required
		}
		buf := make([]Value, required, newCap)
		copy(buf, *dataBuf)
		*dataBuf = buf
	}
	*dataBuf = (*dataBuf)[:required]
	dataSlice := (*dataBuf)[off : off+n : off+n]
	for i, pair := range layout {
		if pair[0] >= 0 {
			dataSlice[i] = a.Data[pair[0]]
		} else if pair[1] >= 0 {
			dataSlice[i] = b.Data[pair[1]]
		}
		// pair[0] < 0 && pair[1] < 0: column not found, leave as zero Value (NULL)
	}
	return Row{
		Cols:     cols,
		Types:    types,
		Data:     dataSlice,
		ColIndex: colIndex,
	}
}

// buildProjectedLayout creates the projected column layout from a
// set of projected column names and the left/right column name lists.
// Returns (cols, types, colIndex, layout) for use with joinRowsProjected.
func buildProjectedLayout(projectedCols []string, leftCols, rightCols []string, leftTypes, rightTypes []int) ([]string, []int, map[string]int, [][2]int) {
	if projectedCols == nil {
		// No projection — use all columns.
		n := len(leftCols) + len(rightCols)
		allCols := make([]string, n)
		copy(allCols, leftCols)
		copy(allCols[len(leftCols):], rightCols)
		allTypes := make([]int, n)
		copy(allTypes, leftTypes)
		copy(allTypes[len(leftTypes):], rightTypes)
		layout := make([][2]int, n)
		for i := range leftCols {
			layout[i] = [2]int{i, -1}
		}
		for i := range rightCols {
			layout[len(leftCols)+i] = [2]int{-1, i}
		}
		colIndex := make(map[string]int, n)
		for i, c := range allCols {
			colIndex[strings.ToLower(c)] = i
		}
		return allCols, allTypes, colIndex, layout
	}

	n := len(projectedCols)
	cols := make([]string, n)
	types := make([]int, n)
	layout := make([][2]int, n)

	leftLower := make(map[string]int, len(leftCols))
	for i, c := range leftCols {
		leftLower[strings.ToLower(c)] = i
	}
	rightLower := make(map[string]int, len(rightCols))
	for i, c := range rightCols {
		rightLower[strings.ToLower(c)] = i
	}

	for i, pc := range projectedCols {
		cols[i] = pc
		pl := strings.ToLower(pc)
		if li, ok := leftLower[pl]; ok && li < len(leftTypes) {
			layout[i] = [2]int{li, -1}
			types[i] = leftTypes[li]
		} else if ri, ok := rightLower[pl]; ok && ri < len(rightTypes) {
			layout[i] = [2]int{-1, ri}
			types[i] = rightTypes[ri]
		} else {
			// Column not found — mark as invalid (will be skipped at output).
			layout[i] = [2]int{-2, -2}
		}
	}

	colIndex := make(map[string]int, n)
	for i, c := range cols {
		colIndex[strings.ToLower(c)] = i
	}
	return cols, types, colIndex, layout
}

// isJoinOp returns true when the operator is a NestedLoopJoin or
// HashJoin (i.e., it represents a join operator in the plan tree).
// REQ000843: used by tryHashCrossJoin to skip bushy-group joins.
// REQ001102: also includes MergeJoin.
func isJoinOp(op Operator) bool {
	switch op.(type) {
	case *NestedLoopJoin, *HashJoin, *HashCrossJoin, *MergeJoin:
		return true
	}
	return false
}

// schemaCols extracts column names from a schema.
func schemaCols(rows []Row) []string {
	if len(rows) == 0 {
		return nil
	}
	return append([]string(nil), rows[0].Cols...)
}

// schemaTypes extracts column types from a schema.
func schemaTypes(rows []Row) []LX.TokenType {
	if len(rows) == 0 {
		return nil
	}
	return append([]LX.TokenType(nil), rows[0].Types...)
}
