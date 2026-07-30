package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001628: Pure batch HashCrossJoin — eliminates the row-based wrapper.
// Builds a hash table from the batch-producer right side, then probes
// with the left side batch rows and emits output batches directly
// without per-row Row construction.
//
// HashCrossJoin is used for small-table equi-joins (< 1024 rows).
// Both sides are materialized into columnar arrays; the right side
// is hashed on the join key for O(1) probe.
type BatchHashCrossJoin struct {
	left      UT.BatchProducer
	right     UT.BatchProducer
	leftKey   int // column index for the left join key
	rightKey  int // column index for the right join key

	// Schema info.
	leftN      int
	leftNames  []string
	leftTypes  []LX.TokenType
	rightN     int
	rightNames []string
	rightTypes []LX.TokenType
	outputN    int

	// Materialized build (right) side.
	rightCols  []UT.Column
	rightRows  int
	rightBuilt bool

	// Hash table: rightKey value → list of right row indices.
	ht       *UT.HashTable
	rowIDs   [][]uint32

	// Probe (left) side materialized.
	leftCols  []UT.Column
	leftRows  int
	leftBuilt bool

	// Pre-computed match pairs.
	pending struct {
		l []uint32 // left row index
		r []uint32 // right row index
	}
	pendingPos int

	outputColNames []string
	outputColTypes []LX.TokenType

	done bool
}

// NewBatchHashCrossJoin creates a pure batch hash cross join.
// leftKey and rightKey are column indices into the left/right batches.
// REQ001628.
func NewBatchHashCrossJoin(left, right UT.BatchProducer, leftKey, rightKey int) *BatchHashCrossJoin {
	return &BatchHashCrossJoin{
		left:     left,
		right:    right,
		leftKey:  leftKey,
		rightKey: rightKey,
	}
}

// materializeRight drains the right side and builds a hash table.
func (j *BatchHashCrossJoin) materializeRight(ctx context.Context) error {
	if j.rightBuilt {
		return nil
	}

	var batches []*UT.Batch
	defer func() {
		for _, b := range batches {
			b.Put()
		}
	}()

	for {
		batch, err := j.right.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		batches = append(batches, batch)
	}

	if len(batches) == 0 {
		j.rightBuilt = true
		return nil
	}

	j.rightN = meaningfulCols(batches)
	j.rightNames = make([]string, j.rightN)
	j.rightTypes = make([]LX.TokenType, j.rightN)
	for i := 0; i < j.rightN; i++ {
		j.rightNames[i] = batches[0].Cols[i].Name
		j.rightTypes[i] = batches[0].Cols[i].Type
	}

	totalRows := 0
	for _, b := range batches {
		totalRows += b.Size
	}
	if totalRows == 0 {
		j.rightBuilt = true
		return nil
	}

	j.rightCols = make([]UT.Column, j.rightN)
	for i := range j.rightCols {
		allocateColData(&j.rightCols[i], totalRows, j.rightTypes[i])
	}

	row := 0
	for _, b := range batches {
		for r := 0; r < b.Size; r++ {
			for c := range j.rightCols {
				copyRowToColumn(&j.rightCols[c], &b.Cols[c], row, r)
			}
			row++
		}
	}
	j.rightRows = totalRows

	// Build hash table from right key column.
	j.ht = UT.NewHashTable(uint32(totalRows))
	j.rowIDs = make([][]uint32, j.ht.Capacity)

	keyCol := &j.rightCols[j.rightKey]
	for i := 0; i < totalRows; i++ {
		if isColNull(keyCol, i) {
			continue
		}
		key := colValAt(keyCol, i)
		hash := utHashInt64(key)
		j.ht.ProbeInt64(
			[]int64{key},
			[]uint64{hash},
			1,
			func(idx int, _ int) {
				j.rowIDs[idx] = append(j.rowIDs[idx], uint32(i))
			},
		)
	}

	j.rightBuilt = true
	return nil
}

// materializeLeft drains the left side into columnar arrays.
func (j *BatchHashCrossJoin) materializeLeft(ctx context.Context) error {
	if j.leftBuilt {
		return nil
	}

	var batches []*UT.Batch
	defer func() {
		for _, b := range batches {
			b.Put()
		}
	}()

	for {
		batch, err := j.left.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		batches = append(batches, batch)
	}

	if len(batches) == 0 {
		j.leftBuilt = true
		return nil
	}

	j.leftN = meaningfulCols(batches)
	j.leftNames = make([]string, j.leftN)
	j.leftTypes = make([]LX.TokenType, j.leftN)
	for i := 0; i < j.leftN; i++ {
		j.leftNames[i] = batches[0].Cols[i].Name
		j.leftTypes[i] = batches[0].Cols[i].Type
	}

	totalRows := 0
	for _, b := range batches {
		totalRows += b.Size
	}
	if totalRows == 0 {
		j.leftBuilt = true
		return nil
	}

	j.leftCols = make([]UT.Column, j.leftN)
	for i := range j.leftCols {
		allocateColData(&j.leftCols[i], totalRows, j.leftTypes[i])
	}

	row := 0
	for _, b := range batches {
		for r := 0; r < b.Size; r++ {
			for c := range j.leftCols {
				copyRowToColumn(&j.leftCols[c], &b.Cols[c], row, r)
			}
			row++
		}
	}
	j.leftRows = totalRows

	// Build output schema.
	j.outputN = j.leftN + j.rightN
	j.outputColNames = make([]string, j.outputN)
	j.outputColTypes = make([]LX.TokenType, j.outputN)
	copy(j.outputColNames, j.leftNames)
	copy(j.outputColNames[j.leftN:], j.rightNames)
	copy(j.outputColTypes, j.leftTypes)
	copy(j.outputColTypes[j.leftN:], j.rightTypes)

	// REQ002044: if hash table is nil (right side was empty), skip
	// the probe loop to avoid nil pointer dereference.
	if j.ht == nil {
		j.leftBuilt = true
		return nil
	}

	// Pre-compute all match pairs.
	for li := 0; li < totalRows; li++ {
		if isColNull(&j.leftCols[j.leftKey], li) {
			continue
		}
		key := colValAt(&j.leftCols[j.leftKey], li)
		hash := utHashInt64(key)
		idx, found, _ := j.ht.Lookup([]int64{key}, hash)
		if found {
			rids := j.rowIDs[idx]
			for _, ri := range rids {
				j.pending.l = append(j.pending.l, uint32(li))
				j.pending.r = append(j.pending.r, ri)
			}
		}
	}

	j.leftBuilt = true
	return nil
}

// NextBatch returns the next batch of matched rows.
func (j *BatchHashCrossJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if j.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !j.rightBuilt {
		if err := j.materializeRight(ctx); err != nil {
			return nil, err
		}
	}
	if !j.leftBuilt {
		if err := j.materializeLeft(ctx); err != nil {
			return nil, err
		}
	}
	if j.ht == nil || j.leftRows == 0 {
		j.done = true
		return nil, nil
	}

	// Drain pending matches into output batches.
	for j.pendingPos < len(j.pending.l) {
		output := UT.GetBatch(j.outputN)
		for i := 0; i < j.outputN; i++ {
			output.Cols[i].Name = j.outputColNames[i]
			output.Cols[i].Type = j.outputColTypes[i]
			allocateColData(&output.Cols[i], UT.BatchSize, j.outputColTypes[i])
		}

		n := 0
		for j.pendingPos < len(j.pending.l) && n < UT.BatchSize {
			li := int(j.pending.l[j.pendingPos])
			ri := int(j.pending.r[j.pendingPos])
			j.pendingPos++

			// Copy left columns.
			for c := 0; c < j.leftN; c++ {
				copyRowToColumn(&output.Cols[c], &j.leftCols[c], n, li)
			}
			// Copy right columns.
			for c := 0; c < j.rightN; c++ {
				copyRowToColumn(&output.Cols[j.leftN+c], &j.rightCols[c], n, ri)
			}
			n++
		}

		if n == 0 {
			output.Put()
			break
		}
		output.Size = n
		return output, nil
	}

	j.done = true
	return nil, nil
}

// Close releases all resources.
func (j *BatchHashCrossJoin) Close() error {
	j.leftCols = nil
	j.rightCols = nil
	j.rowIDs = nil
	j.ht = nil
	j.pending.l = nil
	j.pending.r = nil
	return nil
}