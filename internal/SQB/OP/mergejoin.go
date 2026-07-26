// Package EX's mergejoin.go hosts MergeJoin. Joins are not part of the
// v1 MVP scope per docs/design/ARCH.md; the operator is retained in v1.1
// for upcoming releases.
//
// MergeJoin performs a classic sort-merge join on two pre-sorted inputs.
// REQ001102. Both sides MUST produce rows in ascending order on the
// join keys; the operator does not sort — it relies on Sort operators
// above it (added by the planner when it detects the join keys match an
// existing ORDER BY or index). Compared with NestedLoopJoin's O(N×M),
// MergeJoin runs in O(N+M) for equi-joins, and supports LEFT/RIGHT/FULL
// outer joins via mark/unmark tracking.
//
// Algorithm:
//
//	left cursor i, right cursor j
//	while i < left and j < right:
//	  if left[i].key == right[j].key:
//	    emit all (left[i], right[j]) pairs
//	    advance the side with the smaller key
//	  elif left[i].key < right[j].key:
//	    if LEFT/FULL: emit left[i] with NULL right row
//	    advance i
//	  else:
//	    if RIGHT/FULL: emit right[j] with NULL left row
//	    advance j
//	emit remaining rows (LEFT/RIGHT/FULL only)
package OP

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// MergeJoin joins two pre-sorted streams on equi-keys. REQ001102.
// Both left and right must produce rows sorted ascending on their
// respective join keys; the operator does not sort. NULLs sort
// before any non-NULL value (consistent with REQ000445's NULL
// three-valued logic — NULL = NULL for join purposes).
type MergeJoin struct {
	left      pl.Operator
	right     pl.Operator
	leftTbl   string
	rightTbl  string
	leftKeys  []string
	rightKeys []string
	kind      JoinKind
	// Outer join tracking.
	leftOuter  bool
	rightOuter bool
	// State machine cursors.
	leftRow      *pl.Row
	rightRow     *pl.Row
	rightBuf     []pl.Row // pending right rows with same key as leftRow
	rightIdx     int      // current index into rightBuf
	rightSaved   bool     // whether we still need to re-emit rightBuf for LEFT/FULL
	rightDone    bool     // right side exhausted
	leftDone     bool     // left side exhausted
	rightEmitted bool     // for RIGHT/FULL: unmatched right rows emitted
	// Shared output schema (REQ000794-style perf).
	sharedCols     []string
	sharedTypes    []LX.TokenType
	sharedColIndex map[string]int
	// Reusable buffers.
	dataBuf    []pl.Value
	dataPerRow int
	// Pre-allocated key scratch buffers.
	leftKeyBuf  []pl.Value
	rightKeyBuf []pl.Value
}

// NewMergeJoin creates a sort-merge join. leftKeys and rightKeys are
// the equi-join column names (must be sorted ascending on each side).
// Outer join variants are selected via kind (JoinKindLeft, JoinKindRight,
// JoinKindFull). JoinKindInner is the default.
func NewMergeJoin(left, right pl.Operator, leftTbl, rightTbl string, leftKeys, rightKeys []string) *MergeJoin {
	mk := max(len(leftKeys), len(rightKeys))
	return &MergeJoin{
		left:        left,
		right:       right,
		leftTbl:     leftTbl,
		rightTbl:    rightTbl,
		leftKeys:    leftKeys,
		rightKeys:   rightKeys,
		kind:        JoinKindInner,
		leftOuter:   false,
		rightOuter:  false,
		leftKeyBuf:  make([]pl.Value, mk),
		rightKeyBuf: make([]pl.Value, mk),
	}
}

// WithKind sets the join kind (Inner/Left/Right/Full). REQ001102.
func (j *MergeJoin) WithKind(k JoinKind) *MergeJoin {
	j.kind = k
	j.leftOuter = k == JoinKindLeft || k == JoinKindFull
	j.rightOuter = k == JoinKindRight || k == JoinKindFull
	return j
}

// Kind returns the join kind. REQ001294.
func (j *MergeJoin) Kind() JoinKind { return j.kind }

// WithProjection sets the projected columns. Currently a no-op
// because the planner's column-pruning pass runs after the join;
// kept for API symmetry with HashJoin/NestedLoopJoin.
func (j *MergeJoin) WithProjection(cols []string) *MergeJoin { return j }

// LeftChild / RightChild / LeftTbl / RightTbl satisfy the join-operator
// access pattern used elsewhere in the planner.
func (j *MergeJoin) LeftChild() pl.Operator      { return j.left }
func (j *MergeJoin) RightChild() pl.Operator     { return j.right }
func (j *MergeJoin) LeftTbl() string             { return j.leftTbl }
func (j *MergeJoin) RightTbl() string            { return j.rightTbl }
func (j *MergeJoin) LeftKeys() []string          { return j.leftKeys }
func (j *MergeJoin) RightKeys() []string         { return j.rightKeys }
func (j *MergeJoin) SharedCols() []string        { return j.sharedCols }
func (j *MergeJoin) SharedTypes() []LX.TokenType { return j.sharedTypes }

// Next produces the next joined row. On first call it reads the
// first row from each side and primes the state machine. Subsequent
// calls advance the cursors and emit matches / outer rows.
func (j *MergeJoin) Next(ctx context.Context) (pl.Row, error) {
	if err := ctx.Err(); err != nil {
		return pl.Row{}, err
	}
	for {
		// Prime left/right cursors on first entry and after
		// advancing past a consumed row.
		if j.leftRow == nil && !j.leftDone {
			if err := j.advanceLeft(ctx); err != nil {
				return pl.Row{}, err
			}
		}
		if j.rightRow == nil && !j.rightDone {
			if err := j.advanceRight(ctx); err != nil {
				return pl.Row{}, err
			}
		}
		// Drain both sides: after this returns true, no more rows.
		if j.leftDone && j.rightDone {
			return pl.Row{}, ErrNoRows
		}
		// If left exhausted, emit any remaining right rows for
		// RIGHT/FULL, otherwise drain.
		if j.leftDone {
			if j.rightOuter && !j.rightEmitted {
				j.rightEmitted = true
				return j.emitUnmatchedRight(j.leftTbl), nil
			}
			return pl.Row{}, ErrNoRows
		}
		// If right exhausted, emit unmatched left for LEFT/FULL.
		if j.rightDone {
			if j.leftOuter {
				row := *j.leftRow
				j.leftRow = nil
				return j.emitWithNullRight(row), nil
			}
			j.leftDone = true
			continue
		}
		// Compare keys.
		lk := lookupKeys(*j.leftRow, j.leftKeys, j.leftKeyBuf)
		rk := lookupKeys(*j.rightRow, j.rightKeys, j.rightKeyBuf)
		cmp := mergeJoinCompare(lk, rk)
		if cmp == 0 {
			// Equal — collect all right rows sharing this key.
			if j.rightBuf == nil {
				j.rightBuf = make([]pl.Row, 0, 4)
			}
			j.rightBuf = j.rightBuf[:0]
			j.rightBuf = append(j.rightBuf, *j.rightRow)
			for {
				if err := j.advanceRight(ctx); err != nil {
					return pl.Row{}, err
				}
				if j.rightDone {
					break
				}
				rk2 := lookupKeys(*j.rightRow, j.rightKeys, j.rightKeyBuf)
				if mergeJoinCompare(lk, rk2) != 0 {
					break
				}
				j.rightBuf = append(j.rightBuf, *j.rightRow)
			}
			j.rightIdx = 0
			// Emit (leftRow, rightBuf[i]) pairs.
			out := mergeJoinEmit(*j.leftRow, j.rightBuf[j.rightIdx], j.dataBuf, j.dataPerRow, j.sharedCols, j.sharedColIndex)
			j.rightIdx++
			if j.rightIdx >= len(j.rightBuf) {
				// Consumed all matches for this left row.
				j.leftRow = nil
				j.rightBuf = j.rightBuf[:0]
			}
			return out, nil
		}
		if cmp < 0 {
			// left < right — left row has no match. Emit
			// NULL-padded row if LEFT/FULL.
			row := *j.leftRow
			j.leftRow = nil
			if j.leftOuter {
				return j.emitWithNullRight(row), nil
			}
			continue
		}
		// left > right — right row has no match (yet). If
		// RIGHT/FULL, emit it with NULL left. Otherwise
		// advance right and continue.
		if j.rightOuter && !j.rightEmitted {
			row := *j.rightRow
			j.rightRow = nil
			return j.emitUnmatchedRightAsRow(row), nil
		}
		if err := j.advanceRight(ctx); err != nil {
			return pl.Row{}, err
		}
	}
}

// Close releases the children.
func (j *MergeJoin) Close() error {
	var err error
	if j.left != nil {
		err = j.left.Close()
	}
	if j.right != nil {
		if e := j.right.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// advanceLeft pulls the next left row, prefixing its columns with
// the left table name if they are not already prefixed.
func (j *MergeJoin) advanceLeft(ctx context.Context) error {
	for {
		row, err := j.left.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				j.leftDone = true
				j.leftRow = nil
				return nil
			}
			return err
		}
		prefixed := pl.Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		prefixed.TableName = row.TableName
		if !hasAnyPrefix(row.Cols) {
			prefixed.Cols = prefixCols(row.Cols, j.leftTbl)
		} else {
			prefixed.Cols = append([]string(nil), row.Cols...)
		}
		j.leftRow = &prefixed
		// Initialize shared schema on first row.
		if j.sharedCols == nil {
			j.initSharedSchema(prefixed, row.Cols)
		}
		return nil
	}
}

// advanceRight mirrors advanceLeft for the right side.
func (j *MergeJoin) advanceRight(ctx context.Context) error {
	for {
		row, err := j.right.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				j.rightDone = true
				j.rightRow = nil
				return nil
			}
			return err
		}
		prefixed := pl.Row{Types: row.Types, Data: row.Data, Outer: row.Outer}
		prefixed.TableName = row.TableName
		if !hasAnyPrefix(row.Cols) {
			prefixed.Cols = prefixCols(row.Cols, j.rightTbl)
		} else {
			prefixed.Cols = append([]string(nil), row.Cols...)
		}
		j.rightRow = &prefixed
		if j.sharedCols == nil {
			j.initSharedSchema(prefixed, row.Cols)
		}
		return nil
	}
}

// initSharedSchema builds the output Cols / Types / ColIndex from the
// first input rows. REQ000794-style perf: shared across all emitted rows.
func (j *MergeJoin) initSharedSchema(first pl.Row, firstOrigCols []string) {
	if j.sharedCols != nil {
		return
	}
	// Combine left + right column lists.
	// REQ002045: fix capacity — was len(firstOrigCols)+len(firstOrigCols),
	// should be len(first.Cols)+len(firstOrigCols) to account for the right
	// side having more columns than the left.
	cols := make([]string, 0, len(first.Cols)+len(firstOrigCols))
	types := make([]LX.TokenType, 0, cap(cols))
	if j.leftRow != nil {
		cols = append(cols, j.leftRow.Cols...)
		types = append(types, j.leftRow.Types...)
	}
	if j.rightRow != nil {
		cols = append(cols, j.rightRow.Cols...)
		types = append(types, j.rightRow.Types...)
	}
	if len(cols) == 0 && j.leftRow != nil {
		cols = append(cols, j.leftRow.Cols...)
		types = append(types, j.leftRow.Types...)
	}
	j.sharedCols = cols
	j.sharedTypes = types
	j.dataPerRow = 0
	if j.leftRow != nil {
		j.dataPerRow += len(j.leftRow.Data)
	}
	if j.rightRow != nil {
		j.dataPerRow += len(j.rightRow.Data)
	}
	j.dataBuf = make([]pl.Value, j.dataPerRow)
	// ColIndex: build eagerly so downstream operators can use it.
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[c] = i
	}
	j.sharedColIndex = idx
}

// emitWithNullRight emits a left row padded with NULL on the right.
func (j *MergeJoin) emitWithNullRight(left pl.Row) pl.Row {
	out := pl.Row{
		Cols:     j.sharedCols,
		Types:    j.sharedTypes,
		ColIndex: j.sharedColIndex,
	}
	out.Data = append(out.Data, left.Data...)
	if j.rightRow != nil {
		for range j.rightRow.Data {
			out.Data = append(out.Data, pl.Value{})
		}
	} else {
		// Estimate right width from dataBuf/dataPerRow.
		remain := j.dataPerRow - len(left.Data)
		for i := 0; i < remain; i++ {
			out.Data = append(out.Data, pl.Value{})
		}
	}
	return out
}

// emitUnmatchedRightAsRow emits a right row padded with NULL on the left.
func (j *MergeJoin) emitUnmatchedRightAsRow(right pl.Row) pl.Row {
	out := pl.Row{
		Cols:     j.sharedCols,
		Types:    j.sharedTypes,
		ColIndex: j.sharedColIndex,
	}
	remain := j.dataPerRow - len(right.Data)
	for i := 0; i < remain; i++ {
		out.Data = append(out.Data, pl.Value{})
	}
	out.Data = append(out.Data, right.Data...)
	return out
}

// emitUnmatchedRight produces a fully-NULL-padded row when the left
// side was exhausted but unmatched right rows may remain. Caller must
// have set up dataPerRow.
func (j *MergeJoin) emitUnmatchedRight(leftTbl string) pl.Row {
	out := pl.Row{
		Cols:     j.sharedCols,
		Types:    j.sharedTypes,
		ColIndex: j.sharedColIndex,
	}
	for i := 0; i < j.dataPerRow; i++ {
		out.Data = append(out.Data, pl.Value{})
	}
	return out
}

// mergeJoinCompare compares two key slices for sort order. NULLs sort
// first (consistent with the convention used elsewhere in the codebase).
// Returns -1, 0, or +1.
func mergeJoinCompare(a, b []pl.Value) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := a[i], b[i]
		// NULLs sort first.
		if av.IsNull() && !bv.IsNull() {
			return -1
		}
		if !av.IsNull() && bv.IsNull() {
			return 1
		}
		if av.IsNull() && bv.IsNull() {
			continue
		}
		c := pl.CompareValue(av, bv)
		if c != 0 {
			if c < 0 {
				return -1
			}
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

// mergeJoinEmit combines left and right rows into a single output row
// using the shared data buffer for alloc-free emission.
func mergeJoinEmit(left, right pl.Row, dataBuf []pl.Value, dataPerRow int, sharedCols []string, sharedColIndex map[string]int) pl.Row {
	out := pl.Row{
		Cols:     sharedCols,
		ColIndex: sharedColIndex,
	}
	if cap(dataBuf) < dataPerRow {
		dataBuf = make([]pl.Value, dataPerRow)
	} else {
		dataBuf = dataBuf[:dataPerRow]
	}
	copy(dataBuf, left.Data)
	copy(dataBuf[len(left.Data):], right.Data)
	out.Data = dataBuf
	return out
}
