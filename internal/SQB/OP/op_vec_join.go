package OP

import (
	"context"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// VectorizedHashJoin implements an inner equi-join between two batch
// streams using a hash table on the build-side key.
type VectorizedHashJoin struct {
	build    UT.BatchProducer
	probe    UT.BatchProducer
	buildKey int
	probeKey int

	ht        *UT.HashTable
	buildCols []UT.Column
	rowIDs    [][]uint32
	buildDone bool
	done      bool

	pending    []uint32
	probeBatch *UT.Batch
	probeRow   int

	buildNames []string
	buildTypes []LX.TokenType
	buildN     int
	probeNames []string
	probeTypes []LX.TokenType
	probeN     int
}

// NewVectorizedHashJoin creates a vectorized inner hash join.
func NewVectorizedHashJoin(build, probe UT.BatchProducer, buildKey, probeKey int) *VectorizedHashJoin {
	return &VectorizedHashJoin{
		build:    build,
		probe:    probe,
		buildKey: buildKey,
		probeKey: probeKey,
	}
}

// NextBatch produces the next output batch. Returns (nil, nil) at EOF.
func (j *VectorizedHashJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if j.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !j.buildDone {
		if err := j.buildHashTable(ctx); err != nil {
			return nil, err
		}
	}
	if j.ht == nil {
		j.done = true
		return nil, nil
	}
	return j.probePhase(ctx)
}

func (j *VectorizedHashJoin) buildHashTable(ctx context.Context) error {
	var batches []*UT.Batch
	defer func() {
		for _, b := range batches {
			b.Put()
		}
	}()

	for {
		batch, err := j.build.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		batches = append(batches, batch)
	}

	nCols := meaningfulCols(batches)
	j.buildN = nCols
	j.buildCols = make([]UT.Column, nCols)
	j.buildNames = make([]string, nCols)
	j.buildTypes = make([]LX.TokenType, nCols)
	for i := 0; i < nCols; i++ {
		j.buildNames[i] = batches[0].Cols[i].Name
		j.buildTypes[i] = batches[0].Cols[i].Type
	}

	totalRows := 0
	for _, b := range batches {
		totalRows += b.Size
	}

	if totalRows == 0 {
		j.buildDone = true
		return nil
	}

	for i := range j.buildCols {
		allocateColData(&j.buildCols[i], totalRows, j.buildTypes[i])
	}

	row := 0
	for _, b := range batches {
		for r := 0; r < b.Size; r++ {
			for c := range j.buildCols {
				copyRowToColumn(&j.buildCols[c], &b.Cols[c], row, r)
			}
			row++
		}
	}

	j.ht = UT.NewHashTable(uint32(totalRows))
	j.rowIDs = make([][]uint32, j.ht.Capacity)

	keyCol := &j.buildCols[j.buildKey]
	for i := 0; i < totalRows; i++ {
		if isColNull(keyCol, i) {
			continue
		}
		key := keyColVal(keyCol, i)
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

	j.buildDone = true
	return nil
}

// probePhase fills an output batch by consuming pending matches and
// advancing through probe rows until the batch is full or the probe
// side is exhausted.
func (j *VectorizedHashJoin) probePhase(ctx context.Context) (*UT.Batch, error) {
	nBuild := j.buildN

	// Ensure we have the first probe batch so schema is known.
	if j.probeBatch == nil {
		if !j.refillProbe(ctx) {
			return nil, nil
		}
	}

	nCols := nBuild + j.probeN
	output := j.newOutputBatch(nCols)

	for output.Size < UT.BatchSize {
		// Drain pending matches first.
		for len(j.pending) > 0 && output.Size < UT.BatchSize {
			j.emitOneRow(output, nBuild, int(j.pending[0]))
			j.pending = j.pending[1:]
		}
		if output.Size >= UT.BatchSize {
			break
		}

		// Advance probe pointer.
		j.probeRow++

		// Refill if current batch exhausted.
		if j.probeRow >= j.probeBatch.Size {
			if !j.refillProbe(ctx) {
				break
			}
		}

		// Probe hash table for current row.
		j.probeCurrentRow()
	}

	if output.Size == 0 {
		output.Put()
		return nil, nil
	}
	return output, nil
}

func (j *VectorizedHashJoin) emitOneRow(output *UT.Batch, nBuild, buildRowIdx int) {
	outRow := output.Size
	for c := 0; c < nBuild; c++ {
		copyRowToColumn(&output.Cols[c], &j.buildCols[c], outRow, buildRowIdx)
	}
	for c := 0; c < j.probeN; c++ {
		copyRowToColumn(&output.Cols[nBuild+c], &j.probeBatch.Cols[c], outRow, j.probeRow)
	}
	output.Size++
}

func (j *VectorizedHashJoin) probeCurrentRow() {
	keyCol := &j.probeBatch.Cols[j.probeKey]
	if isColNull(keyCol, j.probeRow) {
		return
	}
	key := keyColVal(keyCol, j.probeRow)
	hash := utHashInt64(key)
	idx, found, _ := j.ht.Lookup([]int64{key}, hash)
	if found {
		j.pending = append(j.pending[:0], j.rowIDs[idx]...)
	}
}

func (j *VectorizedHashJoin) refillProbe(ctx context.Context) bool {
	if j.probeBatch != nil {
		j.probeBatch.Put()
		j.probeBatch = nil
	}
	batch, err := j.probe.NextBatch(ctx)
	if err != nil || batch == nil {
		j.done = true
		return false
	}
	j.probeBatch = batch
	j.probeRow = -1 // will be incremented to 0 by the probe loop
	if j.probeNames == nil {
		j.probeN = meaningfulCols([]*UT.Batch{batch})
		j.probeNames = make([]string, j.probeN)
		j.probeTypes = make([]LX.TokenType, j.probeN)
		for i := 0; i < j.probeN; i++ {
			j.probeNames[i] = batch.Cols[i].Name
			j.probeTypes[i] = batch.Cols[i].Type
		}
	}
	return true
}

func (j *VectorizedHashJoin) newOutputBatch(nCols int) *UT.Batch {
	output := UT.GetBatch(nCols)
	for i := 0; i < j.buildN; i++ {
		output.Cols[i].Name = j.buildNames[i]
		output.Cols[i].Type = j.buildTypes[i]
		allocateColData(&output.Cols[i], UT.BatchSize, j.buildTypes[i])
	}
	for i := 0; i < j.probeN; i++ {
		output.Cols[j.buildN+i].Name = j.probeNames[i]
		output.Cols[j.buildN+i].Type = j.probeTypes[i]
		allocateColData(&output.Cols[j.buildN+i], UT.BatchSize, j.probeTypes[i])
	}
	return output
}

// Close releases all resources.
func (j *VectorizedHashJoin) Close() error {
	if j.probeBatch != nil {
		j.probeBatch.Put()
		j.probeBatch = nil
	}
	if j.build != nil {
		if err := j.build.Close(); err != nil {
			return err
		}
	}
	if j.probe != nil {
		return j.probe.Close()
	}
	return nil
}

// --- helpers ---

// meaningfulCols returns the count of columns with non-zero Type.
func meaningfulCols(batches []*UT.Batch) int {
	if len(batches) == 0 {
		return 0
	}
	n := 0
	for i := range batches[0].Cols {
		if batches[0].Cols[i].Type != 0 {
			n = i + 1
		}
	}
	return n
}

func allocateColData(col *UT.Column, n int, typ LX.TokenType) {
	col.Type = typ
	switch typ {
	case LX.T_INT_KW, LX.T_BIGINT:
		col.Data.Ints = make([]int64, n)
	case LX.T_FLOAT_KW:
		col.Data.Floats = make([]float64, n)
	case LX.T_BOOL:
		col.Data.Bools = make([]bool, n)
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		col.Data.Strs = make([]string, n)
	default:
		col.Data.Ints = make([]int64, n)
	}
}

func copyRowToColumn(dst, src *UT.Column, dstRow, srcRow int) {
	if src.Nulls != nil && srcRow < len(src.Nulls) && src.Nulls[srcRow] {
		if dst.Nulls == nil {
			dst.Nulls = make([]bool, dstRow+1)
		} else if dstRow >= len(dst.Nulls) {
			grown := make([]bool, dstRow+1)
			copy(grown, dst.Nulls)
			dst.Nulls = grown
		}
		dst.Nulls[dstRow] = true
		return
	}

	switch src.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if srcRow < len(src.Data.Ints) && dstRow < len(dst.Data.Ints) {
			dst.Data.Ints[dstRow] = src.Data.Ints[srcRow]
		}
	case LX.T_FLOAT_KW:
		if srcRow < len(src.Data.Floats) && dstRow < len(dst.Data.Floats) {
			dst.Data.Floats[dstRow] = src.Data.Floats[srcRow]
		}
	case LX.T_BOOL:
		if srcRow < len(src.Data.Bools) && dstRow < len(dst.Data.Bools) {
			dst.Data.Bools[dstRow] = src.Data.Bools[srcRow]
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if srcRow < len(src.Data.Strs) && dstRow < len(dst.Data.Strs) {
			dst.Data.Strs[dstRow] = src.Data.Strs[srcRow]
		}
	}
}

func isColNull(col *UT.Column, row int) bool {
	return col.Nulls != nil && row < len(col.Nulls) && col.Nulls[row]
}

func keyColVal(col *UT.Column, row int) int64 {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if row < len(col.Data.Ints) {
			return col.Data.Ints[row]
		}
	case LX.T_FLOAT_KW:
		if row < len(col.Data.Floats) {
			return int64(col.Data.Floats[row])
		}
	}
	return 0
}

func utHashInt64(v int64) uint64 {
	h := uint64(v)
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	h ^= h >> 31
	return h
}

// VectorizedNestedLoopJoin implements a batch-based nested loop join.
// INNER and CROSS joins are supported. The build (right) side is fully
// materialized; the probe (left) side is streamed batch-by-batch.
type VectorizedNestedLoopJoin struct {
	left      UT.BatchProducer
	right     UT.BatchProducer
	on        func(*Row, *Row) (bool, error)
	kind      JoinKind

	// Materialized build (right) side
	buildCols []UT.Column
	buildN    int
	buildDone bool
	nBuildRow int

	// Probe (left) side streaming
	probeBatch *UT.Batch
	probeRow   int

	// Pending match pairs for current probe row
	pending struct {
		l []uint32 // left row index (in current probe batch)
		r []uint32 // paired right row index
	}
	pendingPos int

	// Schema
	leftNames  []string
	leftTypes  []LX.TokenType
	leftN      int
	rightNames []string
	rightTypes []LX.TokenType
	rightN     int

	done bool
}

func NewVectorizedNestedLoopJoin(left, right UT.BatchProducer, on func(*Row, *Row) (bool, error), kind JoinKind) *VectorizedNestedLoopJoin {
	return &VectorizedNestedLoopJoin{
		left:  left,
		right: right,
		on:    on,
		kind:  kind,
	}
}

func (j *VectorizedNestedLoopJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if j.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !j.buildDone {
		if err := j.materializeBuild(ctx); err != nil {
			return nil, err
		}
	}
	if j.buildN == 0 || j.nBuildRow == 0 {
		j.done = true
		return nil, nil
	}
	if j.kind == JoinKindCross {
		return j.nextCrossBatch(ctx)
	}
	return j.nextInnerBatch(ctx)
}

func (j *VectorizedNestedLoopJoin) materializeBuild(ctx context.Context) error {
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

	nCols := meaningfulCols(batches)
	j.buildN = nCols
	j.buildCols = make([]UT.Column, nCols)
	j.rightNames = make([]string, nCols)
	j.rightTypes = make([]LX.TokenType, nCols)
	for i := 0; i < nCols; i++ {
		j.rightNames[i] = batches[0].Cols[i].Name
		j.rightTypes[i] = batches[0].Cols[i].Type
	}

	totalRows := 0
	for _, b := range batches {
		totalRows += b.Size
	}
	if totalRows == 0 {
		j.buildDone = true
		j.nBuildRow = 0
		return nil
	}

	for i := range j.buildCols {
		allocateColData(&j.buildCols[i], totalRows, j.rightTypes[i])
	}
	row := 0
	for _, b := range batches {
		for r := 0; r < b.Size; r++ {
			for c := range j.buildCols {
				copyRowToColumn(&j.buildCols[c], &b.Cols[c], row, r)
			}
			row++
		}
	}

	j.nBuildRow = totalRows
	j.buildDone = true
	return nil
}

func (j *VectorizedNestedLoopJoin) nextInnerBatch(ctx context.Context) (*UT.Batch, error) {
	nBuild := j.buildN

	if j.probeBatch == nil {
		if !j.refillBuildProbe(ctx) {
			return nil, nil
		}
	}

	leftN, leftNames, leftTypes := j.probeNamesTypes()
	nCols := leftN + nBuild
	output := UT.GetBatch(nCols)
	for i := 0; i < leftN; i++ {
		allocateColData(&output.Cols[i], UT.BatchSize, leftTypes[i])
		output.Cols[i].Name = leftNames[i]
	}
	for i := 0; i < nBuild; i++ {
		allocateColData(&output.Cols[leftN+i], UT.BatchSize, j.rightTypes[i])
		output.Cols[leftN+i].Name = j.rightNames[i]
	}

	for output.Size < UT.BatchSize {
		// Drain pending matches first
		for j.pendingPos < len(j.pending.l) && output.Size < UT.BatchSize {
			lr := int(j.pending.l[j.pendingPos])
			rr := int(j.pending.r[j.pendingPos])
			j.pendingPos++
			emitNLJRow(output, j.probeBatch, &j.buildCols, lr, rr, leftN, j.probeRow)
		}
		if output.Size >= UT.BatchSize {
			break
		}

		// Advance probe
		j.probeRow++
		if j.probeRow >= j.probeBatch.Size {
			if !j.refillBuildProbe(ctx) {
				break
			}
		}

		// Probe against all build rows
		j.drainPending()
	}

	if output.Size == 0 {
		output.Put()
		j.done = true
		return nil, nil
	}
	return output, nil
}

func (j *VectorizedNestedLoopJoin) drainPending() {
	// For the current probe row, scan all build rows
	leftRow := batchRowForProbe(j.probeBatch, j.probeRow)
	leftRowLen := len(j.leftNames)
	if j.leftNames != nil {
		leftRowLen = len(j.leftNames)
	}
	_ = leftRowLen

	for b := 0; b < j.nBuildRow; b++ {
		buildRow := buildRowForMatch(&j.buildCols, b, j.rightTypes)

		if j.on != nil {
			ok, err := j.on(&leftRow, &buildRow)
			if err != nil {
				continue
			}
			if !ok {
				continue
			}
		}

		j.pending.l = append(j.pending.l, uint32(j.probeRow))
		j.pending.r = append(j.pending.r, uint32(b))
	}
}

func (j *VectorizedNestedLoopJoin) refillBuildProbe(ctx context.Context) bool {
	if j.probeBatch != nil {
		j.probeBatch.Put()
		j.probeBatch = nil
	}
	batch, err := j.left.NextBatch(ctx)
	if err != nil || batch == nil {
		j.done = true
		return false
	}
	j.probeBatch = batch
	j.probeRow = -1
	if j.leftNames == nil {
		j.leftN = meaningfulCols([]*UT.Batch{batch})
		j.leftNames = make([]string, j.leftN)
		j.leftTypes = make([]LX.TokenType, j.leftN)
		for i := 0; i < j.leftN; i++ {
			j.leftNames[i] = batch.Cols[i].Name
			j.leftTypes[i] = batch.Cols[i].Type
		}
	}
	j.pending.l = j.pending.l[:0]
	j.pending.r = j.pending.r[:0]
	j.pendingPos = 0
	return true
}

func (j *VectorizedNestedLoopJoin) probeNamesTypes() (int, []string, []LX.TokenType) {
	if j.leftNames != nil {
		return j.leftN, j.leftNames, j.leftTypes
	}
	if j.probeBatch != nil {
		n := meaningfulCols([]*UT.Batch{j.probeBatch})
		return n, nil, nil
	}
	return 0, nil, nil
}

func (j *VectorizedNestedLoopJoin) nextCrossBatch(ctx context.Context) (*UT.Batch, error) {
	nBuild := j.buildN

	if j.probeBatch == nil {
		if !j.refillBuildProbe(ctx) {
			return nil, nil
		}
	}

	leftN := j.leftN
	if j.leftNames != nil {
		leftN = len(j.leftNames)
	}

	nCols := leftN + nBuild
	output := UT.GetBatch(nCols)
	for i := 0; i < leftN; i++ {
		allocateColData(&output.Cols[i], UT.BatchSize, j.leftTypes[i])
		output.Cols[i].Name = j.leftNames[i]
	}
	for i := 0; i < nBuild; i++ {
		allocateColData(&output.Cols[leftN+i], UT.BatchSize, j.rightTypes[i])
		output.Cols[leftN+i].Name = j.rightNames[i]
	}

	for output.Size < UT.BatchSize {
		// Drain pending
		for j.pendingPos < len(j.pending.l) && output.Size < UT.BatchSize {
			lr := int(j.pending.l[j.pendingPos])
			rr := int(j.pending.r[j.pendingPos])
			j.pendingPos++
			emitNLJRow(output, j.probeBatch, &j.buildCols, lr, rr, leftN, j.probeRow)
		}
		if output.Size >= UT.BatchSize {
			break
		}

		j.probeRow++
		if j.probeRow >= j.probeBatch.Size {
			if !j.refillBuildProbe(ctx) {
				break
			}
		}

		// Cross join: every build row matches
		for b := 0; b < j.nBuildRow; b++ {
			j.pending.l = append(j.pending.l, uint32(j.probeRow))
			j.pending.r = append(j.pending.r, uint32(b))
		}
	}

	if output.Size == 0 {
		output.Put()
		j.done = true
		return nil, nil
	}
	return output, nil
}

func (j *VectorizedNestedLoopJoin) Close() error {
	if j.probeBatch != nil {
		j.probeBatch.Put()
		j.probeBatch = nil
	}
	if j.left != nil {
		_ = j.left.Close()
	}
	if j.right != nil {
		return j.right.Close()
	}
	return nil
}

func emitNLJRow(output *UT.Batch, probeBatch *UT.Batch, buildCols *[]UT.Column, leftRow, rightRow, leftN int, _ int) {
	outRow := output.Size
	for c := 0; c < leftN; c++ {
		if c < len(probeBatch.Cols) {
			copyRowToColumn(&output.Cols[c], &probeBatch.Cols[c], outRow, leftRow)
		}
	}
	for c := 0; c < len(*buildCols); c++ {
		copyRowToColumn(&output.Cols[leftN+c], &(*buildCols)[c], outRow, rightRow)
	}
	output.Size++
}

// batchRowForProbe constructs a Row from a probe batch at position idx.
func batchRowForProbe(batch *UT.Batch, idx int) Row {
	n := meaningfulCols([]*UT.Batch{batch})
	data := make([]Value, n)
	cols := make([]string, n)
	types := make([]LX.TokenType, n)
	for c := 0; c < n; c++ {
		cols[c] = batch.Cols[c].Name
		types[c] = batch.Cols[c].Type
		if batch.Cols[c].Nulls != nil && idx < len(batch.Cols[c].Nulls) && batch.Cols[c].Nulls[idx] {
			data[c] = Value{Kind: KindNull}
			continue
		}
		switch batch.Cols[c].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if idx < len(batch.Cols[c].Data.Ints) {
				data[c] = Value{Kind: KindInt, I64: batch.Cols[c].Data.Ints[idx]}
			}
		case LX.T_FLOAT_KW:
			if idx < len(batch.Cols[c].Data.Floats) {
				data[c] = Value{Kind: KindFloat, F64: batch.Cols[c].Data.Floats[idx]}
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if idx < len(batch.Cols[c].Data.Strs) {
				data[c] = Value{Kind: KindText, S: batch.Cols[c].Data.Strs[idx]}
			}
		case LX.T_BOOL:
			if idx < len(batch.Cols[c].Data.Bools) {
				data[c] = Value{Kind: KindBool, Bo: batch.Cols[c].Data.Bools[idx]}
			}
		}
	}
	return Row{Data: data, Cols: cols, Types: types}
}

// buildRowForMatch constructs a Row from the materialized build columns.
func buildRowForMatch(buildCols *[]UT.Column, idx int, types []LX.TokenType) Row {
	n := len(*buildCols)
	data := make([]Value, n)
	cols := make([]string, n)
	for c := 0; c < n; c++ {
		cols[c] = (*buildCols)[c].Name
		if (*buildCols)[c].Nulls != nil && idx < len((*buildCols)[c].Nulls) && (*buildCols)[c].Nulls[idx] {
			data[c] = Value{Kind: KindNull}
			continue
		}
		switch (*buildCols)[c].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if idx < len((*buildCols)[c].Data.Ints) {
				data[c] = Value{Kind: KindInt, I64: (*buildCols)[c].Data.Ints[idx]}
			}
		case LX.T_FLOAT_KW:
			if idx < len((*buildCols)[c].Data.Floats) {
				data[c] = Value{Kind: KindFloat, F64: (*buildCols)[c].Data.Floats[idx]}
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if idx < len((*buildCols)[c].Data.Strs) {
				data[c] = Value{Kind: KindText, S: (*buildCols)[c].Data.Strs[idx]}
			}
		case LX.T_BOOL:
			if idx < len((*buildCols)[c].Data.Bools) {
				data[c] = Value{Kind: KindBool, Bo: (*buildCols)[c].Data.Bools[idx]}
			}
		}
	}
	return Row{Data: data, Cols: cols, Types: types}
}
