// Package EX's join.go hosts NestedLoopJoin. Joins are not part of the
// v1 MVP scope per docs/design/ARCH.md; the operator is retained in v1.1 for
// upcoming releases.
package EX

import (
	"context"
	"errors"
	"strings"
)

// JoinKind specifies the type of join.
type JoinKind string

const (
	JoinKindInner JoinKind = "INNER"
	JoinKindLeft  JoinKind = "LEFT"
	JoinKindRight JoinKind = "RIGHT"
	JoinKindFull  JoinKind = "FULL"
	JoinKindCross JoinKind = "CROSS"
)

type NestedLoopJoin struct {
	left      Operator
	right     Operator
	leftTbl   string
	rightTbl  string
	on        func(outer, inner *Row) (bool, error)
	kind      JoinKind
	leftOuter bool // REQ000685: true for LEFT/LEFT OUTER JOIN
	leftRow   *Row
	rightPos  int
	rightRows []Row
	matched   bool // for OUTER JOIN: track if left row found a match
	// REQ000800: hash-based cross join for small tables.
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
	// downstream Filter/Eval doesn't have to call buildColIndex
	// per row. j3 perf: ~2.7GB of allocations eliminated per
	// 1M-row benchmark run.
	sharedColIndex map[string]int
	// sharedCols and sharedTypes are pre-built from materialized
	// left+right column layouts and shared across all hash-mode
	// output rows instead of allocating fresh slices per row.
	sharedCols  []string
	sharedTypes []int
	// dataBuf is a single pre-allocated []any covering all output
	// rows' Data slices. Each row gets a non-overlapping sub-slice
	// [off:off:off+dataPerRow], eliminating per-row make([]Value, ...)
	// allocations. j3 perf: 39% of alloc_space was here.
	dataBuf    []Value
	dataPerRow int
	dataOffset int // running write offset into dataBuf
	// REQ000798: Block NLJ mode — batch left rows and re-scan right
	// per batch. Used when hash mode is unavailable (left > 1024
	// or ON clause exists). Reduces right-side scans from N to N/32.
	blockMode    bool
	blkLeftBatch []Row
	blkLeftPos   int   // position within left batch
	blkRightRows []Row // materialized right side (recreated per batch)
	blkRightPos  int
	blkResultBuf []Row // buffered matches for current batch
	blkResultPos int
}

func NewNestedLoopJoin(left, right Operator, leftTable, rightTable string, on func(outer, inner *Row) (bool, error), kind JoinKind) *NestedLoopJoin {
	return &NestedLoopJoin{
		left:      left,
		right:     right,
		leftTbl:   leftTable,
		rightTbl:  rightTable,
		on:        on,
		kind:      kind,
		leftOuter: kind == JoinKindLeft || kind == JoinKindFull,
	}
}

func (j *NestedLoopJoin) LeftChild() Operator { return j.left }

func (j *NestedLoopJoin) RightChild() Operator { return j.right }

func (j *NestedLoopJoin) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	// REQ000800: try hash cross join on first call for small tables.
	// Guard includes leftRow == nil to avoid re-entering tryHashCrossJoin
	// mid-NLJ iteration — after an abort the operator is partway through
	// the NLJ loop and re-entering would re-drain the left child on
	// every Next() call (catastrophic for large cross products).
	// hashAttempted prevents O(N²) re-entry when left has >1024 rows:
	// without it, every time leftRow becomes nil the guard passes,
	// re-draining the left child each time.
	if !j.hashMode && !j.hashAttempted && j.leftRows == nil && j.rightRows == nil && j.leftRow == nil {
		if j.tryHashCrossJoin(ctx) {
			// Switched to hash mode — continue with hash iteration.
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
			prefixed.tableName = row.tableName
			if !hasAnyPrefix(row.Cols) {
				prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
			} else {
				prefixed.Cols = append([]string(nil), row.Cols...)
			}
			j.leftRow = &prefixed
			// REQ000368: drive the right side through its own
			// operator rather than the in-memory `tables` map.
			// The in-memory map is empty for store-backed
			// tables, which caused CROSS JOIN (and any JOIN
			// of store-backed tables) to return zero rows.
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
						result := joinRowsLL(j.leftRow, &nullRow)
						j.leftRow = nil
						return result, nil
					}
				}
				j.leftRow = nil
				continue
			}
			return Row{}, err
		}
		inner.Cols = prefixCols(inner.Cols, j.rightTbl)
		inner.Outer = j.leftRow
		if j.on != nil {
			ok, err := j.on(j.leftRow, &inner)
			if err != nil {
				return Row{}, err
			}
			if !ok {
				continue
			}
		}
		j.matched = true
		return joinRowsLL(j.leftRow, &inner), nil
	}
}

// tryHashCrossJoin attempts to switch to hash mode. Returns true
// if hash mode was activated. REQ000800.
func (j *NestedLoopJoin) tryHashCrossJoin(ctx context.Context) bool {
	// Only for INNER/CROSS without ON clause (pure cross product).
	if j.on != nil || j.leftOuter {
		return false
	}
	// Materialize left side.
	const maxMaterialize = 1024
	j.leftRows = make([]Row, 0, 64)
	for {
		row, err := j.left.Next(ctx)
		if err != nil {
			break
		}
		prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		prefixed.tableName = row.tableName
		if !hasAnyPrefix(row.Cols) {
			prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
		} else {
			prefixed.Cols = append([]string(nil), row.Cols...)
		}
		j.leftRows = append(j.leftRows, prefixed)
		if len(j.leftRows) >= maxMaterialize {
			// Too many rows — abort, use NLJ. Close() to reset
			// operator state; the NLJ loop re-initializes from
			// scratch on the next call.
			j.left.Close()
			j.leftRows = nil
			j.hashAttempted = true
			return false
		}
	}
	// Materialize right side.
	j.rightRows = make([]Row, 0, 64)
	for {
		row, err := j.right.Next(ctx)
		if err != nil {
			break
		}
		j.rightRows = append(j.rightRows, Row{
			Cols:      prefixCols(row.Cols, j.rightTbl),
			Types:     row.Types,
			Data:      append([]Value(nil), row.Data...),
			tableName: row.tableName,
		})
		if len(j.rightRows) >= maxMaterialize {
			// Too many rows — abort, use NLJ. Close both sides
			// and reset.
			j.right.Close()
			j.left.Close()
			j.rightRows = nil
			j.leftRows = nil
			j.hashAttempted = true
			return false
		}
	}
	if len(j.leftRows) == 0 || len(j.rightRows) == 0 {
		// Empty side — result is empty.
		j.leftRows = nil
		j.rightRows = nil
		j.hashAttempted = true
		return false
	}
	// Build a shared colIndex for the join output shape. This
	// eliminates per-row map allocation in Row.Lookup downstream.
	// j3 perf: the colIndex depends on the row shape (left.Cols +
	// right.Cols) which is stable for the lifetime of this join
	// since both sides come from the same SeqScan snapshots.
	nCols := len(j.leftRows[0].Cols) + len(j.rightRows[0].Cols)
	j.sharedCols = make([]string, 0, nCols)
	j.sharedCols = append(j.sharedCols, j.leftRows[0].Cols...)
	j.sharedCols = append(j.sharedCols, j.rightRows[0].Cols...)

	j.sharedTypes = make([]int, 0, nCols)
	j.sharedTypes = append(j.sharedTypes, j.leftRows[0].Types...)
	j.sharedTypes = append(j.sharedTypes, j.rightRows[0].Types...)

	j.sharedColIndex = make(map[string]int, nCols)
	for i, c := range j.sharedCols {
		j.sharedColIndex[strings.ToLower(c)] = i
	}
	// Pre-allocate a single contiguous Data buffer for all output
	// rows. Each output row gets a non-overlapping sub-slice
	// [off:off:off+dataPerRow] — no per-row make([]Value) needed.
	j.dataPerRow = len(j.leftRows[0].Data) + len(j.rightRows[0].Data)
	totalRows := len(j.leftRows) * len(j.rightRows)
	j.dataBuf = make([]Value, 0, totalRows*j.dataPerRow)
	j.dataOffset = 0
	// Build hash from right side for equi-join lookups.
	// For cross join, we don't hash — just iterate.
	// Hash is useful for equi-join ON conditions.
	j.hashMode = true
	j.leftIdx = 0
	j.rightIdx = 0
	return true
}

// nextHash produces the next row from the materialized hash join.
// REQ000800.
func (j *NestedLoopJoin) nextHash(_ context.Context) (Row, error) {
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
				colIndex: j.sharedColIndex,
			}
			out.Data = append(out.Data, l.Data...)
			out.Data = append(out.Data, r.Data...)
			return out, nil
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
	tablesMu.RLock()
	rightSchema := tables[j.rightTbl]
	tablesMu.RUnlock()

	nullRow := Row{
		Cols:  prefixCols(schemaCols(rightSchema), j.rightTbl),
		Types: schemaTypes(rightSchema),
		Data:  make([]Value, len(rightSchema)),
	}
	return nullRow
}

func (j *NestedLoopJoin) Close() error {
	j.hashMode = false
	j.hashAttempted = false
	j.leftRow = nil
	j.leftRows = nil
	j.rightRows = nil
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
	_ = j.left.Close()
	return j.right.Close()
}

// nextBlock implements Block Nested-Loop Join (REQ000798). Batches
// left rows and re-scans the right side once per batch, reducing
// right-side scans from N to ceil(N/batchSize).
func (j *NestedLoopJoin) nextBlock(ctx context.Context) (Row, error) {
	const batchSize = 32
	// Drain result buffer.
	if j.blkResultPos < len(j.blkResultBuf) {
		r := j.blkResultBuf[j.blkResultPos]
		j.blkResultPos++
		return r, nil
	}
	j.blkResultBuf = j.blkResultBuf[:0]
	j.blkResultPos = 0

	// Fill left batch (up to batchSize rows).
	j.blkLeftBatch = j.blkLeftBatch[:0]
	for len(j.blkLeftBatch) < batchSize {
		row, err := j.left.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		prefixed := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		prefixed.tableName = row.tableName
		if !hasAnyPrefix(row.Cols) {
			prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
		} else {
			prefixed.Cols = append([]string(nil), row.Cols...)
		}
		j.blkLeftBatch = append(j.blkLeftBatch, prefixed)
	}
	if len(j.blkLeftBatch) == 0 {
		// Cleanup on exit.
		j.blockMode = false
		return Row{}, ErrNoRows
	}

	// Materialize right side (Close + re-scan once per batch).
	_ = j.right.Close()
	j.blkRightRows = j.blkRightRows[:0]
	for {
		row, err := j.right.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		inner := Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		inner.tableName = row.tableName
		inner.Cols = prefixCols(row.Cols, j.rightTbl)
		j.blkRightRows = append(j.blkRightRows, inner)
	}

	// Match all left batch rows against right rows, emitting in
	// left-primary order so LEFT JOIN results match NLJ ordering.
	for _, l := range j.blkLeftBatch {
		matched := false
		for _, r := range j.blkRightRows {
			r.Outer = &l
			if j.on != nil {
				ok, err := j.on(&l, &r)
				if err != nil {
					return Row{}, err
				}
				if !ok {
					continue
				}
			}
			matched = true
			result := joinRowsLL(&l, &r)
			j.blkResultBuf = append(j.blkResultBuf, result)
		}
		if !matched && j.leftOuter {
			nullRow := j.nullRightRow()
			result := joinRowsLL(&l, &nullRow)
			j.blkResultBuf = append(j.blkResultBuf, result)
		}
	}

	if len(j.blkResultBuf) == 0 {
		// No matches in this batch — try next batch.
		j.blkLeftBatch = j.blkLeftBatch[:0]
		return j.nextBlock(ctx)
	}
	j.blkResultPos = 1
	return j.blkResultBuf[0], nil
}

func joinRowsLL(a, b *Row) Row {
	// Pre-compute total lengths and allocate once. The 6× append-
	// from-slice idiom in the previous implementation over-allocated
	// and required multiple grow operations per row. j3 perf: this
	// is the hottest allocation site.
	nCols := len(a.Cols) + len(b.Cols)
	nTypes := len(a.Types) + len(b.Types)
	nData := len(a.Data) + len(b.Data)
	out := Row{
		Cols:  make([]string, 0, nCols),
		Types: make([]int, 0, nTypes),
		Data:  make([]Value, 0, nData),
	}
	out.Cols = append(out.Cols, a.Cols...)
	out.Cols = append(out.Cols, b.Cols...)
	out.Types = append(out.Types, a.Types...)
	out.Types = append(out.Types, b.Types...)
	out.Data = append(out.Data, a.Data...)
	out.Data = append(out.Data, b.Data...)
	// colIndex is intentionally NOT built here — operators that
	// emit rows via joinRowsLL assign a shared colIndex from the
	// operator's sharedColIndex field (see NestedLoopJoin).
	// j3 perf: this avoids allocating a map per output row.
	return out
}

func prefixCols(cols []string, prefix string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + "." + c
	}
	return out
}

// hasAnyPrefix reports whether any column name in cols contains
// a dot, indicating it has already been prefixed by a prior join
// in a multi-table chain. REQ000725.
func hasAnyPrefix(cols []string) bool {
	for _, c := range cols {
		if i := strings.IndexByte(c, '.'); i >= 0 && i < len(c)-1 {
			return true
		}
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
func schemaTypes(rows []Row) []int {
	if len(rows) == 0 {
		return nil
	}
	return append([]int(nil), rows[0].Types...)
}
