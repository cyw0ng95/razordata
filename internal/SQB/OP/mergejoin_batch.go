package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001615/REQ001625: Pure batch MergeJoin — eliminates the row-based
// wrapper. Reads batches from both pre-sorted sides, performs the
// sort-merge join directly on columnar data, and emits output batches.
//
// Algorithm (sorted-batch merge):
//
//	left cursor (batchIdx, rowIdx), right cursor (batchIdx, rowIdx)
//	compare left.key[i] and right.key[j]:
//	  - lt: emit left[i] with NULL right if LEFT/FULL, advance i
//	  - gt: emit right[j] with NULL left if RIGHT/FULL, advance j
//	  - eq: emit all (left[i], right[*]) pairs, advance i
//	flush remaining unmatched rows (LEFT/RIGHT/FULL only)
type BatchMergeJoin struct {
	left      UT.BatchProducer
	right     UT.BatchProducer
	leftKeys  []int
	rightKeys []int
	kind      JoinKind

	// Cursors: (batchIdx in slice, rowIdx within batch)
	leftBatchIdx  int
	rightBatchIdx int
	leftRowIdx    int
	rightRowIdx   int
	leftBatch     *UT.Batch
	rightBatch    *UT.Batch

	// Schema
	leftNames  []string
	leftTypes  []LX.TokenType
	leftN      int
	rightNames []string
	rightTypes []LX.TokenType
	rightN     int
	outputCols []string
	outputN    int

	// Outer join tracking — mark rows already emitted.
	matchedLeft  []bool // [batchIdx * batchSize + rowIdx]
	matchedRight []bool

	// Reusable buffer for matching right rows with same key.
	rightGroup  []int // physical row indices in current right batch
	rightGroupStart int
	rightGroupEnd   int

	// Build phase state.
	leftBatches  []*UT.Batch
	rightBatches []*UT.Batch
	materialized bool

	// Output batch construction.
	curOutput *UT.Batch
	outputColNames []string
	outputColTypes []LX.TokenType

	done bool
}

// NewBatchMergeJoin creates a pure batch merge join. leftKeys and
// rightKeys are column indices into the batch producers' columnar data.
// REQ001615/REQ001625.
func NewBatchMergeJoin(left, right UT.BatchProducer, leftKeys, rightKeys []int, kind JoinKind) *BatchMergeJoin {
	return &BatchMergeJoin{
		left:     left,
		right:    right,
		leftKeys: leftKeys,
		rightKeys: rightKeys,
		kind:     kind,
	}
}

// materializeBuild drains both sides into batches. Used because we
// need random access into both sides for the merge algorithm —
// streaming merge requires backtracking when many right rows share
// the same key.
func (j *BatchMergeJoin) materializeBuild(ctx context.Context) error {
	for {
		batch, err := j.left.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		j.leftBatches = append(j.leftBatches, batch)
	}
	for {
		batch, err := j.right.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		j.rightBatches = append(j.rightBatches, batch)
	}

	// Build schema from first batch.
	if len(j.leftBatches) > 0 {
		b := j.leftBatches[0]
		j.leftN = meaningfulCols(j.leftBatches)
		j.leftNames = make([]string, j.leftN)
		j.leftTypes = make([]LX.TokenType, j.leftN)
		for i := 0; i < j.leftN; i++ {
			j.leftNames[i] = b.Cols[i].Name
			j.leftTypes[i] = b.Cols[i].Type
		}
	}
	if len(j.rightBatches) > 0 {
		b := j.rightBatches[0]
		j.rightN = meaningfulCols(j.rightBatches)
		j.rightNames = make([]string, j.rightN)
		j.rightTypes = make([]LX.TokenType, j.rightN)
		for i := 0; i < j.rightN; i++ {
			j.rightNames[i] = b.Cols[i].Name
			j.rightTypes[i] = b.Cols[i].Type
		}
	}

	// Build output column list (left cols + right cols).
	j.outputN = j.leftN + j.rightN
	j.outputCols = make([]string, 0, j.outputN)
	j.outputCols = append(j.outputCols, j.leftNames...)
	j.outputCols = append(j.outputCols, j.rightNames...)
	j.outputColNames = j.outputCols
	j.outputColTypes = make([]LX.TokenType, j.outputN)
	copy(j.outputColTypes, j.leftTypes)
	copy(j.outputColTypes[j.leftN:], j.rightTypes)

	// Allocate matched tracking.
	if j.kind == JoinKindLeft || j.kind == JoinKindFull {
		j.matchedLeft = make([]bool, j.totalLeftRows())
	}
	if j.kind == JoinKindRight || j.kind == JoinKindFull {
		j.matchedRight = make([]bool, j.totalRightRows())
	}

	j.materialized = true
	return nil
}

// totalLeftRows returns the total number of left rows across all batches.
func (j *BatchMergeJoin) totalLeftRows() int {
	n := 0
	for _, b := range j.leftBatches {
		n += b.Size
	}
	return n
}

// totalRightRows returns the total number of right rows across all batches.
func (j *BatchMergeJoin) totalRightRows() int {
	n := 0
	for _, b := range j.rightBatches {
		n += b.Size
	}
	return n
}

// keyAt returns the composite key value at (batches, rowIdx) for the
// given key columns. Returns int64 for the column at keyIdx.
func (j *BatchMergeJoin) keyAt(batches []*UT.Batch, batchIdx, rowIdx int, keyCols []int) int64 {
	if batchIdx >= len(batches) {
		return 0
	}
	batch := batches[batchIdx]
	if batch == nil || rowIdx >= batch.Size {
		return 0
	}
	return batchColVal(batch, keyCols[0], rowIdx)
}

// cmpKeys compares keys at (lBatch, lRow) and (rBatch, rRow) for
// ascending sort order. Returns -1, 0, +1. Multi-column: composite compare.
func (j *BatchMergeJoin) cmpKeys(lBatch, lRow, rBatch, rRow int) int {
	for i := 0; i < len(j.leftKeys) && i < len(j.rightKeys); i++ {
		lv := j.keyAt(j.leftBatches, lBatch, lRow, []int{j.leftKeys[i]})
		rv := j.keyAt(j.rightBatches, rBatch, rRow, []int{j.rightKeys[i]})
		if lv < rv {
			return -1
		}
		if lv > rv {
			return 1
		}
	}
	return 0
}

// NextBatch produces the next output batch.
func (j *BatchMergeJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if j.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !j.materialized {
		if err := j.materializeBuild(ctx); err != nil {
			return nil, err
		}
	}
	if j.curOutput == nil {
		j.curOutput = j.newOutputBatch()
	}

	// Drive the merge algorithm until the batch is full.
	for j.curOutput.Size < UT.BatchSize {
		if j.leftBatchIdx >= len(j.leftBatches) && j.rightBatchIdx >= len(j.rightBatches) {
			break
		}
		// If left exhausted, emit unmatched right (RIGHT/FULL).
		if j.leftBatchIdx >= len(j.leftBatches) {
			if !j.emitOneUnmatchedRight() {
				break
			}
			continue
		}
		// If right exhausted, emit unmatched left (LEFT/FULL).
		if j.rightBatchIdx >= len(j.rightBatches) {
			if !j.emitOneUnmatchedLeft() {
				break
			}
			continue
		}
		cmp := j.cmpKeys(j.leftBatchIdx, j.leftRowIdx, j.rightBatchIdx, j.rightRowIdx)
		if cmp < 0 {
			if !j.emitOneUnmatchedLeft() {
				break
			}
		} else if cmp > 0 {
			if !j.emitOneUnmatchedRight() {
				break
			}
		} else {
			// Equal: collect all right rows with same key, emit cartesian.
			j.collectEqualRightGroup()
			if !j.emitOneEqualPair() {
				break
			}
		}
	}

	if j.curOutput.Size == 0 {
		j.curOutput.Put()
		j.curOutput = nil
		// Flush remaining unmatched rows if any (LEFT/RIGHT/FULL).
		if j.leftBatchIdx < len(j.leftBatches) || j.rightBatchIdx < len(j.rightBatches) {
			j.curOutput = j.newOutputBatch()
			return j.flushRemaining()
		}
		j.done = true
		return nil, nil
	}
	batch := j.curOutput
	j.curOutput = nil
	return batch, nil
}

// collectEqualRightGroup scans the right side starting at (rightBatchIdx,
// rightRowIdx) and collects all rows with the same key as
// (leftBatchIdx, leftRowIdx).
func (j *BatchMergeJoin) collectEqualRightGroup() {
	j.rightGroup = j.rightGroup[:0]
	curKey := j.keyAt(j.leftBatches, j.leftBatchIdx, j.leftRowIdx, []int{j.leftKeys[0]})
	for j.rightBatchIdx < len(j.rightBatches) {
		batch := j.rightBatches[j.rightBatchIdx]
		if j.rightRowIdx >= batch.Size {
			j.rightBatchIdx++
			j.rightRowIdx = 0
			continue
		}
		rk := j.keyAt(j.rightBatches, j.rightBatchIdx, j.rightRowIdx, []int{j.rightKeys[0]})
		if rk != curKey {
			break
		}
		j.rightGroup = append(j.rightGroup, j.rightRowIdx)
		j.rightRowIdx++
	}
}

// emitOneEqualPair emits one (leftRow, rightGroup[r]) pair. Returns false
// if the output batch is full or no more pairs in the current group.
func (j *BatchMergeJoin) emitOneEqualPair() bool {
	if j.curOutput.Size >= UT.BatchSize {
		return false
	}
	// If group exhausted, advance left and reset group.
	if len(j.rightGroup) == 0 {
		// Advance left.
		j.advanceLeft()
		return true
	}
	// Emit one pair.
	leftRow := j.leftRowIdx
	rightRow := j.rightGroup[0]
	j.rightGroup = j.rightGroup[1:]

	// Copy left columns.
	for c := 0; c < j.leftN; c++ {
		copyRowToColumn(&j.curOutput.Cols[c], &j.leftBatches[j.leftBatchIdx].Cols[c], j.curOutput.Size, leftRow)
	}
	// Copy right columns.
	for c := 0; c < j.rightN; c++ {
		copyRowToColumn(&j.curOutput.Cols[j.leftN+c], &j.rightBatches[j.rightBatchIdx].Cols[c], j.curOutput.Size, rightRow)
	}
	// Mark matched.
	if j.matchedLeft != nil {
		idx := j.leftBatchIdx*UT.BatchSize + leftRow
		if idx < len(j.matchedLeft) {
			j.matchedLeft[idx] = true
		}
	}
	if j.matchedRight != nil {
		idx := j.rightBatchIdx*UT.BatchSize + rightRow
		if idx < len(j.matchedRight) {
			j.matchedRight[idx] = true
		}
	}
	j.curOutput.Size++

	// If group empty after emit, advance left.
	if len(j.rightGroup) == 0 {
		j.advanceLeft()
	}
	return true
}

// emitOneUnmatchedLeft emits left row with NULL right padding. Returns false
// if batch full.
func (j *BatchMergeJoin) emitOneUnmatchedLeft() bool {
	if j.curOutput.Size >= UT.BatchSize {
		return false
	}
	// Skip left rows not exhausted.
	for j.leftBatchIdx < len(j.leftBatches) {
		batch := j.leftBatches[j.leftBatchIdx]
		if j.leftRowIdx < batch.Size {
			break
		}
		j.leftBatchIdx++
		j.leftRowIdx = 0
	}
	if j.leftBatchIdx >= len(j.leftBatches) {
		return false
	}
	if j.leftOuter() {
		leftRow := j.leftRowIdx
		batch := j.leftBatches[j.leftBatchIdx]
		// Copy left columns.
		for c := 0; c < j.leftN; c++ {
			copyRowToColumn(&j.curOutput.Cols[c], &batch.Cols[c], j.curOutput.Size, leftRow)
		}
		// Right columns stay NULL.
		j.curOutput.Size++
		idx := j.leftBatchIdx*UT.BatchSize + leftRow
		if j.matchedLeft != nil && idx < len(j.matchedLeft) {
			j.matchedLeft[idx] = true
		}
	}
	j.advanceLeft()
	return true
}

// emitOneUnmatchedRight emits right row with NULL left padding.
func (j *BatchMergeJoin) emitOneUnmatchedRight() bool {
	if j.curOutput.Size >= UT.BatchSize {
		return false
	}
	// Skip right rows not exhausted.
	for j.rightBatchIdx < len(j.rightBatches) {
		batch := j.rightBatches[j.rightBatchIdx]
		if j.rightRowIdx < batch.Size {
			break
		}
		j.rightBatchIdx++
		j.rightRowIdx = 0
	}
	if j.rightBatchIdx >= len(j.rightBatches) {
		return false
	}
	if j.rightOuter() {
		batch := j.rightBatches[j.rightBatchIdx]
		rightRow := j.rightRowIdx
		// Left columns stay NULL.
		// Copy right columns.
		for c := 0; c < j.rightN; c++ {
			copyRowToColumn(&j.curOutput.Cols[j.leftN+c], &batch.Cols[c], j.curOutput.Size, rightRow)
		}
		j.curOutput.Size++
		idx := j.rightBatchIdx*UT.BatchSize + rightRow
		if j.matchedRight != nil && idx < len(j.matchedRight) {
			j.matchedRight[idx] = true
		}
	}
	j.advanceRight()
	return true
}

// flushRemaining emits any leftover unmatched rows (outer joins).
func (j *BatchMergeJoin) flushRemaining() (*UT.Batch, error) {
	for j.curOutput.Size < UT.BatchSize {
		if !j.emitOneUnmatchedLeft() {
			break
		}
	}
	for j.curOutput.Size < UT.BatchSize {
		if !j.emitOneUnmatchedRight() {
			break
		}
	}
	if j.curOutput.Size == 0 {
		j.curOutput.Put()
		j.curOutput = nil
		j.done = true
		return nil, nil
	}
	batch := j.curOutput
	j.curOutput = nil
	return batch, nil
}

// advanceLeft moves to the next left row.
func (j *BatchMergeJoin) advanceLeft() {
	j.leftRowIdx++
	for j.leftBatchIdx < len(j.leftBatches) && j.leftRowIdx >= j.leftBatches[j.leftBatchIdx].Size {
		j.leftBatchIdx++
		j.leftRowIdx = 0
	}
}

// advanceRight moves to the next right row.
func (j *BatchMergeJoin) advanceRight() {
	j.rightRowIdx++
	for j.rightBatchIdx < len(j.rightBatches) && j.rightRowIdx >= j.rightBatches[j.rightBatchIdx].Size {
		j.rightBatchIdx++
		j.rightRowIdx = 0
	}
}

// leftOuter returns true if LEFT/FULL.
func (j *BatchMergeJoin) leftOuter() bool {
	return j.kind == JoinKindLeft || j.kind == JoinKindFull
}

// rightOuter returns true if RIGHT/FULL.
func (j *BatchMergeJoin) rightOuter() bool {
	return j.kind == JoinKindRight || j.kind == JoinKindFull
}

// newOutputBatch allocates a fresh output batch with column metadata.
func (j *BatchMergeJoin) newOutputBatch() *UT.Batch {
	output := UT.GetBatch(j.outputN)
	for i := 0; i < j.outputN; i++ {
		output.Cols[i].Name = j.outputColNames[i]
		output.Cols[i].Type = j.outputColTypes[i]
		allocateColData(&output.Cols[i], UT.BatchSize, j.outputColTypes[i])
	}
	return output
}

// Close releases all resources.
func (j *BatchMergeJoin) Close() error {
	for _, b := range j.leftBatches {
		b.Put()
	}
	for _, b := range j.rightBatches {
		b.Put()
	}
	j.leftBatches = nil
	j.rightBatches = nil
	return nil
}

// batchColVal extracts an int64 value from a batch column at a row.
func batchColVal(batch *UT.Batch, colIdx, rowIdx int) int64 {
	if colIdx >= len(batch.Cols) || rowIdx >= batch.Size {
		return 0
	}
	col := &batch.Cols[colIdx]
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if rowIdx < len(col.Data.Ints) {
			return col.Data.Ints[rowIdx]
		}
	case LX.T_FLOAT_KW:
		if rowIdx < len(col.Data.Floats) {
			return int64(col.Data.Floats[rowIdx])
		}
	}
	return 0
}