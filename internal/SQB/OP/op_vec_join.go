package OP

import (
	"context"
	"hash/fnv"
	"sync"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// VectorizedHashJoin implements an inner equi-join between two batch
// streams using a hash table on the build-side key.
// Supports single or multi-column equi-join keys (REQ001618).
// Supports LEFT/RIGHT/FULL outer joins (REQ001619).
// Supports parallel build phase (REQ001622).
type VectorizedHashJoin struct {
	build    UT.BatchProducer
	probe    UT.BatchProducer
	buildKeys []int
	probeKeys []int

	ht        UT.HashTableInterface
	bloom     *UT.BloomFilter
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

	// REQ001618: composite key buffers for build and probe.
	buildKeyVals []int64
	probeKeyVals []int64

	// REQ001619: outer join state.
	kind           JoinKind
	matchedBuild   []bool
	totalBuildRows int
	matchedProbe   []bool
	unmatchedBuf   []UT.Column
	unmatchedN     int
	probeBatches   []*UT.Batch
	probeBatchIdx  int
	unmatchedBuildEmitted []bool
	matchedProbeRows      int

	// REQ001622: parallel build phase.
	pool *UT.WorkerPool
}

// NewVectorizedHashJoin creates a vectorized inner hash join.
// buildKey and probeKey are column indices into the build/probe side
// respectively. Supports single or multi-column equi-join keys.
func NewVectorizedHashJoin(build, probe UT.BatchProducer, buildKeys, probeKeys []int) *VectorizedHashJoin {
	return &VectorizedHashJoin{
		build:        build,
		probe:        probe,
		buildKeys:    buildKeys,
		probeKeys:    probeKeys,
		buildKeyVals: make([]int64, len(buildKeys)),
		probeKeyVals: make([]int64, len(probeKeys)),
		kind:         JoinKindInner,
	}
}

// NewVectorizedHashJoinWithKind creates a vectorized hash join with the
// specified join kind (INNER/LEFT/RIGHT/FULL). REQ001619.
func NewVectorizedHashJoinWithKind(build, probe UT.BatchProducer, buildKeys, probeKeys []int, kind JoinKind) *VectorizedHashJoin {
	return &VectorizedHashJoin{
		build:        build,
		probe:        probe,
		buildKeys:    buildKeys,
		probeKeys:    probeKeys,
		buildKeyVals: make([]int64, len(buildKeys)),
		probeKeyVals: make([]int64, len(probeKeys)),
		kind:         kind,
	}
}

// WithPool attaches a WorkerPool for parallel build phase.
// REQ001622.
func (j *VectorizedHashJoin) WithPool(pool *UT.WorkerPool) *VectorizedHashJoin {
	j.pool = pool
	return j
}

// NextBatch produces the next output batch. Returns (nil, nil) at EOF.
func (j *VectorizedHashJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	// REQ001619 debug
	_ = j.done
	if j.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if j.done {
		return nil, nil
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

	// REQ001619: multi-phase emission.
	// Phase 0 = matched probe rows (always).
	// Phase 1 = unmatched build rows (LEFT/FULL).
	// Phase 2 = unmatched probe rows (RIGHT/FULL).
	for {
		batch, err := j.probePhase(ctx)
		if err != nil {
			return nil, err
		}
		if batch != nil {
			return batch, nil
		}
		// Probe exhausted. Decide next phase.
		switch j.kind {
		case JoinKindLeft, JoinKindFull:
			// Debug: ensure we reach here
			if j.unmatchedBuf == nil {
				j.initUnmatchedBuf()
			}
			if batch := j.emitUnmatchedBuild(); batch != nil {
				return batch, nil
			}
			if j.kind == JoinKindFull {
				continue // also need RIGHT unmatched
			}
		case JoinKindRight:
			if j.unmatchedBuf == nil {
				j.initUnmatchedBuf()
			}
			if batch := j.emitUnmatchedProbe(); batch != nil {
				return batch, nil
			}
		}
		j.done = true
		return nil, nil
	}
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

	// REQ001618: create hash table with NumCols = len(buildKeys).
	numKeyCols := len(j.buildKeys)
	if numKeyCols < 1 {
		numKeyCols = 1
	}
	// REQ001590: use Robin Hood hashing for large tables, linear probing
	// for small tables where open-addressing overhead is lower.
	if totalRows >= 64 {
		j.ht = UT.NewRobinHoodHashTableWithCols(uint32(totalRows), numKeyCols)
	} else {
		j.ht = UT.NewHashTableWithCols(uint32(totalRows), numKeyCols)
	}
	j.rowIDs = make([][]uint32, j.ht.Cap())

	// REQ001619: allocate matchedBuild for LEFT/FULL outer joins.
	if j.kind == JoinKindLeft || j.kind == JoinKindFull {
		j.matchedBuild = make([]bool, totalRows)
		j.totalBuildRows = totalRows
		j.unmatchedBuildEmitted = make([]bool, totalRows)
	}
	// REQ001619: allocate matchedProbe for RIGHT/FULL outer joins.
	if j.kind == JoinKindRight || j.kind == JoinKindFull {
		// Will be sized when probe batch arrives.
	}

// REQ001622: parallel build phase using WorkerPool.
	if j.pool != nil && totalRows >= 512 {
		if err := j.buildHashTableParallel(totalRows, numKeyCols); err != nil {
			return err
		}
	} else {
		// Build composite key values for each row (sequential).
		if totalRows >= 64 {
			j.ht = UT.NewRobinHoodHashTableWithCols(uint32(totalRows), numKeyCols)
		} else {
			j.ht = UT.NewHashTableWithCols(uint32(totalRows), numKeyCols)
		}
		j.rowIDs = make([][]uint32, j.ht.Cap())
		totalUnique := 0
		for i := 0; i < totalRows; i++ {
			allNonNull := true
			for ki, kc := range j.buildKeys {
				if isColNull(&j.buildCols[kc], i) {
					allNonNull = false
					break
				}
				j.buildKeyVals[ki] = colValAt(&j.buildCols[kc], i)
			}
			if !allNonNull {
				continue
			}
			hash := UT.HashComposite(j.buildKeyVals)
			j.ht.Probe(
				j.buildKeyVals,
				[]uint64{hash},
				1,
				func(idx int, _ int) {
					j.rowIDs[idx] = append(j.rowIDs[idx], uint32(i))
				},
			)
			totalUnique++
		}
		// REQ001494: build bloom filter.
		if numKeyCols == 1 && totalUnique > 0 {
			j.bloom = UT.NewBloomFilter(totalUnique, 0.01)
			kc := j.buildKeys[0]
			for i := 0; i < totalRows; i++ {
				if isColNull(&j.buildCols[kc], i) {
					continue
				}
				j.bloom.Add(uint64(colValAt(&j.buildCols[kc], i)))
			}
}
	}

	j.buildDone = true
	return nil
}

// buildHashTableParallel builds the hash table in parallel using the
// WorkerPool. Each worker processes a chunk of rows, builds a local
// hash table and row IDs, then merges into the global structures.
// REQ001622.
func (j *VectorizedHashJoin) buildHashTableParallel(totalRows int, numKeyCols int) error {
	// Create the global hash table.
	if totalRows >= 64 {
		j.ht = UT.NewRobinHoodHashTableWithCols(uint32(totalRows), numKeyCols)
	} else {
		j.ht = UT.NewHashTableWithCols(uint32(totalRows), numKeyCols)
	}
	j.rowIDs = make([][]uint32, j.ht.Cap())

	// Determine number of workers.
	numWorkers := j.pool.Workers()
	if numWorkers <= 0 {
		numWorkers = 1
	}
	if numWorkers > totalRows/64 {
		numWorkers = totalRows / 64
		if numWorkers < 1 {
			numWorkers = 1
		}
	}

	type localResult struct {
		ht      UT.HashTableInterface
		rowIDs  [][]uint32
		start   int
		end     int
		unique  int
	}

	chunkSize := (totalRows + numWorkers - 1) / numWorkers
	results := make([]localResult, numWorkers)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for wi := 0; wi < numWorkers; wi++ {
		start := wi * chunkSize
		end := start + chunkSize
		if end > totalRows {
			end = totalRows
		}
		if start >= end {
			results[wi] = localResult{start: start, end: end}
			continue
		}

		wg.Add(1)
		wIdx := wi
		wStart := start
		wEnd := end
		_ = j.pool.TrySubmit(func() error {
			defer wg.Done()
			keyVals := make([]int64, numKeyCols)
			local := localResult{
				ht:     UT.NewRobinHoodHashTableWithCols(uint32(wEnd-wStart), numKeyCols),
				rowIDs: make([][]uint32, UT.NewRobinHoodHashTableWithCols(uint32(wEnd-wStart), numKeyCols).Cap()),
				start:  wStart,
				end:    wEnd,
			}
			for i := wStart; i < wEnd; i++ {
				allNonNull := true
				for ki, kc := range j.buildKeys {
					if isColNull(&j.buildCols[kc], i) {
						allNonNull = false
						break
					}
					keyVals[ki] = colValAt(&j.buildCols[kc], i)
				}
				if !allNonNull {
					continue
				}
				hash := UT.HashComposite(keyVals)
				local.ht.Probe(
					keyVals,
					[]uint64{hash},
					1,
					func(idx int, _ int) {
						local.rowIDs[idx] = append(local.rowIDs[idx], uint32(i))
					},
				)
				local.unique++
			}
			mu.Lock()
			results[wIdx] = local
			mu.Unlock()
			return nil
		})
	}
	wg.Wait()

	// Merge local hash tables into the global one.
	totalUnique := 0
	bloomVals := make([]int64, 0, totalRows)
	for _, res := range results {
		if res.ht == nil || res.ht.OccupiedCount() == 0 {
			continue
		}
		entries := res.ht.Entries()
		for _, entry := range entries {
			// Re-probe into global hash table.
			keySlice := make([]int64, numKeyCols)
			copy(keySlice, entry.Key)
			hash := entry.Hash
			j.ht.Probe(
				keySlice,
				[]uint64{hash},
				1,
				func(idx int, _ int) {
					// Find all row IDs from the local table for this slot.
					// We need to match the slot in the local table.
					// Since we don't have a reverse mapping, scan the local rowIDs.
					// This is O(unique_slots) per merge — acceptable for the
					// parallel build case where numKeyCols is small.
				},
			)
			totalUnique++
			if numKeyCols == 1 {
				bloomVals = append(bloomVals, entry.Key[0])
			}
		}

		// Merge row IDs: find the slot in the global table and append.
		for _, ids := range res.rowIDs {
			if len(ids) == 0 {
				continue
			}
			// Re-probe to find the corresponding global slot.
			if len(ids) > 0 {
				firstRow := int(ids[0])
				keyVals := make([]int64, numKeyCols)
				for ki, kc := range j.buildKeys {
					keyVals[ki] = colValAt(&j.buildCols[kc], firstRow)
				}
				hash := UT.HashComposite(keyVals)
				// Find the slot in the global table.
				gIdx, found, _ := j.ht.Lookup(keyVals, hash)
				if found {
					j.rowIDs[gIdx] = append(j.rowIDs[gIdx], ids...)
				} else {
					// Insert into global table.
					j.ht.Probe(
						keyVals,
						[]uint64{hash},
						1,
						func(idx int, _ int) {
							j.rowIDs[idx] = append(j.rowIDs[idx], ids...)
						},
					)
				}
			}
		}
	}

	// Build bloom filter.
	if numKeyCols == 1 && len(bloomVals) > 0 {
		j.bloom = UT.NewBloomFilter(len(bloomVals), 0.01)
		for _, v := range bloomVals {
			j.bloom.Add(uint64(v))
		}
	}

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

	// REQ001619: mark this build row as matched for outer join unmatched emission.
	if j.matchedBuild != nil && buildRowIdx < len(j.matchedBuild) {
		j.matchedBuild[buildRowIdx] = true
	}
	// REQ001619: mark this probe row as matched.
	if j.matchedProbe != nil && j.probeRow < len(j.matchedProbe) {
		j.matchedProbe[j.probeRow] = true
	}
}

func (j *VectorizedHashJoin) probeCurrentRow() {
	// REQ001618: build composite probe key from all key columns.
	allNonNull := true
	for ki, kc := range j.probeKeys {
		if isColNull(&j.probeBatch.Cols[kc], j.probeRow) {
			allNonNull = false
			break
		}
		j.probeKeyVals[ki] = colValAt(&j.probeBatch.Cols[kc], j.probeRow)
	}
	if !allNonNull {
		return
	}

	hash := UT.HashComposite(j.probeKeyVals)

	// REQ001494: bloom filter quick-exit (single-column int keys only).
	if j.bloom != nil && len(j.probeKeys) == 1 {
		kc := j.probeKeys[0]
		key := colValAt(&j.probeBatch.Cols[kc], j.probeRow)
		if !j.bloom.Contains(uint64(key)) {
			return
		}
	}

	idx, found, _ := j.ht.Lookup(j.probeKeyVals, hash)
	if found {
		j.pending = append(j.pending[:0], j.rowIDs[idx]...)
	}
}

func (j *VectorizedHashJoin) refillProbe(ctx context.Context) bool {
	if j.probeBatch != nil {
		// REQ001629: store probe batch for RIGHT/FULL unmatched emission.
		if j.kind == JoinKindRight || j.kind == JoinKindFull {
			j.probeBatches = append(j.probeBatches, j.probeBatch)
		} else {
			j.probeBatch.Put()
		}
		j.probeBatch = nil
	}
	batch, err := j.probe.NextBatch(ctx)
	if err != nil || batch == nil {
		return false
	}
	j.probeBatch = batch
	j.probeRow = -1
	if j.probeNames == nil {
		j.probeN = meaningfulCols([]*UT.Batch{batch})
		j.probeNames = make([]string, j.probeN)
		j.probeTypes = make([]LX.TokenType, j.probeN)
		for i := 0; i < j.probeN; i++ {
			j.probeNames[i] = batch.Cols[i].Name
			j.probeTypes[i] = batch.Cols[i].Type
		}
	}
	// REQ001629: grow matchedProbe for multi-batch probe.
	if j.kind == JoinKindRight || j.kind == JoinKindFull {
		oldLen := len(j.matchedProbe)
		newLen := oldLen + j.probeBatch.Size
		if cap(j.matchedProbe) >= newLen {
			j.matchedProbe = j.matchedProbe[:newLen]
		} else {
			grown := make([]bool, newLen)
			copy(grown, j.matchedProbe)
			j.matchedProbe = grown
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
	// REQ001629: release stored probe batches.
	for _, b := range j.probeBatches {
		b.Put()
	}
	j.probeBatches = nil
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

// --- outer join helpers (REQ001619) ---

// initUnmatchedBuf prepares the output batch columns for unmatched-row emission.
func (j *VectorizedHashJoin) initUnmatchedBuf() {
	if j.unmatchedBuf != nil {
		return
	}
	nCols := j.buildN + j.probeN
	if nCols == 0 {
		return
	}
	j.unmatchedBuf = make([]UT.Column, nCols)
	for i := 0; i < j.buildN; i++ {
		j.unmatchedBuf[i].Name = j.buildNames[i]
		j.unmatchedBuf[i].Type = j.buildTypes[i]
		allocateColData(&j.unmatchedBuf[i], UT.BatchSize, j.buildTypes[i])
	}
	for i := 0; i < j.probeN; i++ {
		j.unmatchedBuf[j.buildN+i].Name = j.probeNames[i]
		j.unmatchedBuf[j.buildN+i].Type = j.probeTypes[i]
		allocateColData(&j.unmatchedBuf[j.buildN+i], UT.BatchSize, j.probeTypes[i])
	}
	j.unmatchedN = nCols
}

// emitUnmatchedBuild emits LEFT/FULL outer unmatched build rows with NULL probe side.
func (j *VectorizedHashJoin) emitUnmatchedBuild() *UT.Batch {
	if j.unmatchedBuf == nil {
		j.initUnmatchedBuf()
	}
	if j.unmatchedBuf == nil || j.totalBuildRows == 0 {
		return nil
	}
	output := UT.GetBatch(j.unmatchedN)
	for i := 0; i < j.unmatchedN; i++ {
		output.Cols[i].Name = j.unmatchedBuf[i].Name
		output.Cols[i].Type = j.unmatchedBuf[i].Type
		allocateColData(&output.Cols[i], UT.BatchSize, j.unmatchedBuf[i].Type)
	}

	// Iterate all build rows, emit those not yet matched/emitted.
	for buildRowIdx := 0; buildRowIdx < j.totalBuildRows && output.Size < UT.BatchSize; buildRowIdx++ {
		if j.matchedBuild != nil && j.matchedBuild[buildRowIdx] {
			continue // already emitted as matched
		}
		if j.unmatchedBuildEmitted != nil && j.unmatchedBuildEmitted[buildRowIdx] {
			continue // already emitted as unmatched
		}
		j.unmatchedBuildEmitted[buildRowIdx] = true
		for c := 0; c < j.buildN; c++ {
			copyRowToColumn(&output.Cols[c], &j.buildCols[c], output.Size, buildRowIdx)
		}
		output.Size++
	}

	if output.Size == 0 {
		output.Put()
		return nil
	}
	return output
}

// emitUnmatchedProbe emits RIGHT/FULL outer unmatched probe rows with NULL build side.
// REQ001619: stores probe rows during probePhase for later unmatched emission.
func (j *VectorizedHashJoin) emitUnmatchedProbe() *UT.Batch {
	// Ensure the last probe batch is stored.
	if j.probeBatch != nil && (j.kind == JoinKindRight || j.kind == JoinKindFull) {
		j.probeBatches = append(j.probeBatches, j.probeBatch)
		j.probeBatch = nil
	}
	if j.unmatchedBuf == nil {
		j.initUnmatchedBuf()
	}
	if j.unmatchedBuf == nil || len(j.probeBatches) == 0 {
		return nil
	}
	output := UT.GetBatch(j.unmatchedN)
	for i := 0; i < j.unmatchedN; i++ {
		output.Cols[i].Name = j.unmatchedBuf[i].Name
		output.Cols[i].Type = j.unmatchedBuf[i].Type
		allocateColData(&output.Cols[i], UT.BatchSize, j.unmatchedBuf[i].Type)
	}

	probeRow := 0
	for batchIdx := 0; batchIdx < len(j.probeBatches) && output.Size < UT.BatchSize; batchIdx++ {
		batch := j.probeBatches[batchIdx]
		for r := 0; r < batch.Size && output.Size < UT.BatchSize; r++ {
			if probeRow < len(j.matchedProbe) && j.matchedProbe[probeRow] {
				probeRow++
				continue
			}
			// Mark as emitted so next call skips it.
			if probeRow < len(j.matchedProbe) {
				j.matchedProbe[probeRow] = true
			}
			// Build columns are NULL (already allocated) — copy probe columns.
			for c := 0; c < j.probeN; c++ {
				copyRowToColumn(&output.Cols[j.buildN+c], &batch.Cols[c], output.Size, r)
			}
			output.Size++
			probeRow++
		}
	}

	if output.Size == 0 {
		output.Put()
		return nil
	}
	return output
}

// replayProbeBatches drains the probe producer and returns all batches.
func (j *VectorizedHashJoin) replayProbeBatches() []*UT.Batch {
	var batches []*UT.Batch
	for {
		batch, err := j.probe.NextBatch(context.Background())
		if err != nil {
			break
		}
		if batch == nil {
			break
		}
		batches = append(batches, batch)
	}
	return batches
}

// copyProbeToOutput copies a probe row into the output batch at the given position.
func (j *VectorizedHashJoin) copyProbeToOutput(output *UT.Batch, probeBatch *UT.Batch, probeRow, outRow int) {
	// Build columns are NULL (already allocated).
	for c := 0; c < j.probeN; c++ {
		copyRowToColumn(&output.Cols[j.buildN+c], &probeBatch.Cols[c], outRow, probeRow)
	}
	output.Size++
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

// colValAt extracts an int64 value from a column at a given row index.
// REQ001618: generic version for composite key support.
func colValAt(col *UT.Column, row int) int64 {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if row < len(col.Data.Ints) {
			return col.Data.Ints[row]
		}
	case LX.T_FLOAT_KW:
		if row < len(col.Data.Floats) {
			return int64(col.Data.Floats[row])
		}
	case LX.T_BOOL:
		if row < len(col.Data.Bools) {
			if col.Data.Bools[row] {
				return 1
			}
			return 0
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if row < len(col.Data.Strs) {
			// Hash string content for composite key lookup.
			h := fnv.New64a()
			h.Write([]byte(col.Data.Strs[row]))
			return int64(h.Sum64())
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
// REQ001630: LEFT/RIGHT/FULL outer join support.
type VectorizedNestedLoopJoin struct {
	left      UT.BatchProducer
	right     UT.BatchProducer
	on        func(*Row, *Row) (bool, error)
	kind      JoinKind

	// REQ001620: columnar predicate for vectorized evaluation.
	colOnLeftIdx  []int
	colOnRightIdx []int
	colOnEq       []bool

	// Materialized build (right) side
	buildCols []UT.Column
	buildN    int
	buildDone bool
	nBuildRow int

	// Probe (left) side streaming
	probeBatch  *UT.Batch
	probeRow    int

	// Pending match pairs for current probe row
	pending struct {
		l []uint32
		r []uint32
	}
	pendingPos int

	// Schema
	leftNames  []string
	leftTypes  []LX.TokenType
	leftN      int
	rightNames []string
	rightTypes []LX.TokenType
	rightN     int

	// REQ001630: outer join state.
	matchedBuild    []bool   // matchedBuild[buildRowIdx]
	matchedProbe    []bool   // matchedProbe[probeRowIdx]
	probeBatches    []*UT.Batch // stored probe batches for unmatched emission
	probeRowCount   int      // total probe rows seen

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

// WithColOn sets a columnar equi-join predicate for vectorized evaluation.
// leftIdx and rightIdx are paired column indices. REQ001620.
func (j *VectorizedNestedLoopJoin) WithColOn(leftIdx, rightIdx []int) *VectorizedNestedLoopJoin {
	j.colOnLeftIdx = leftIdx
	j.colOnRightIdx = rightIdx
	j.colOnEq = make([]bool, len(leftIdx))
	for i := range j.colOnEq {
		j.colOnEq[i] = true // default to equality
	}
	return j
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
	// REQ001630: multi-phase outer join.
	// Phase 0 = matched probe rows (nextInnerBatch).
	// Phase 1 = unmatched build rows (LEFT/FULL).
	// Phase 2 = unmatched probe rows (RIGHT/FULL).
	for {
		batch, err := j.nextInnerBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch != nil {
			return batch, nil
		}
		// Probe exhausted. Emit unmatched rows.
		if j.leftOuter() {
			if batch := j.nljEmitUnmatchedBuild(); batch != nil {
				return batch, nil
			}
		}
		if j.rightOuter() {
			if batch := j.nljEmitUnmatchedProbe(); batch != nil {
				return batch, nil
			}
		}
		j.done = true
		return nil, nil
	}
}

// leftOuter returns true for LEFT/FULL outer joins.
func (j *VectorizedNestedLoopJoin) leftOuter() bool {
	return j.kind == JoinKindLeft || j.kind == JoinKindFull
}

// rightOuter returns true for RIGHT/FULL outer joins.
func (j *VectorizedNestedLoopJoin) rightOuter() bool {
	return j.kind == JoinKindRight || j.kind == JoinKindFull
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
	// REQ001630: initialize matched tracking for outer joins.
	if j.leftOuter() || j.rightOuter() {
		j.matchedBuild = make([]bool, totalRows)
	}
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
		return nil, nil
	}
	return output, nil
}

func (j *VectorizedNestedLoopJoin) drainPending() {
	// REQ001620: if columnar predicate is set, use vectorized evaluation.
	if len(j.colOnLeftIdx) > 0 && len(j.colOnLeftIdx) == len(j.colOnRightIdx) {
		j.drainPendingCol()
		return
	}
	// Fallback to row-based predicate evaluation.
	leftRow := batchRowForProbe(j.probeBatch, j.probeRow)

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
		// REQ001630: mark matched rows.
		if j.matchedBuild != nil && b < len(j.matchedBuild) {
			j.matchedBuild[b] = true
		}
	}
	// REQ001630: mark probe row as matched if any matches found.
	if len(j.pending.l) > 0 && j.matchedProbe != nil && j.probeRow < len(j.matchedProbe) {
		j.matchedProbe[j.probeRow] = true
	}
}

// drainPendingCol evaluates the equi-join predicate using columnar data.
// REQ001620.
func (j *VectorizedNestedLoopJoin) drainPendingCol() {
	for b := 0; b < j.nBuildRow; b++ {
		matched := true
		for ki := 0; ki < len(j.colOnLeftIdx) && matched; ki++ {
			lIdx := j.colOnLeftIdx[ki]
			rIdx := j.colOnRightIdx[ki]
			lVal := j.colValAt(j.probeBatch, lIdx, j.probeRow)
			rVal := j.colValAtCol(&j.buildCols, rIdx, b)
			if lVal != rVal {
				matched = false
			}
		}
		if matched {
			j.pending.l = append(j.pending.l, uint32(j.probeRow))
			j.pending.r = append(j.pending.r, uint32(b))
			// REQ001630: mark matched rows.
			if j.matchedBuild != nil && b < len(j.matchedBuild) {
				j.matchedBuild[b] = true
			}
		}
	}
	// REQ001630: mark probe row as matched if any matches found.
	if len(j.pending.l) > 0 && j.matchedProbe != nil && j.probeRow < len(j.matchedProbe) {
		j.matchedProbe[j.probeRow] = true
	}
}

// colValAt extracts an int64 value from a batch column at a given row.
func (j *VectorizedNestedLoopJoin) colValAt(batch *UT.Batch, colIdx, rowIdx int) int64 {
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

// colValAtCol extracts an int64 value from a materialized column at a given row.
func (j *VectorizedNestedLoopJoin) colValAtCol(cols *[]UT.Column, colIdx, rowIdx int) int64 {
	if colIdx >= len(*cols) || rowIdx >= j.nBuildRow {
		return 0
	}
	col := &(*cols)[colIdx]
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

func (j *VectorizedNestedLoopJoin) refillBuildProbe(ctx context.Context) bool {
	if j.probeBatch != nil {
		// REQ001630: store probe batch for RIGHT/FULL unmatched emission.
		if j.rightOuter() {
			j.probeBatches = append(j.probeBatches, j.probeBatch)
		} else {
			j.probeBatch.Put()
		}
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
	// REQ001630: grow matchedProbe for multi-batch probe.
	if j.rightOuter() {
		oldLen := len(j.matchedProbe)
		newLen := oldLen + j.probeBatch.Size
		if cap(j.matchedProbe) >= newLen {
			j.matchedProbe = j.matchedProbe[:newLen]
		} else {
			grown := make([]bool, newLen)
			copy(grown, j.matchedProbe)
			j.matchedProbe = grown
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
	// REQ001630: release stored probe batches.
	for _, b := range j.probeBatches {
		b.Put()
	}
	j.probeBatches = nil
	if j.left != nil {
		_ = j.left.Close()
	}
	if j.right != nil {
		return j.right.Close()
	}
	return nil
}

// nljEmitUnmatchedBuild emits unmatched build rows with NULL probe side.
// REQ001630: LEFT/FULL outer join.
func (j *VectorizedNestedLoopJoin) nljEmitUnmatchedBuild() *UT.Batch {
	if j.matchedBuild == nil {
		return nil
	}
	leftN, leftNames, leftTypes := j.probeNamesTypes()
	nCols := leftN + j.buildN
	output := UT.GetBatch(nCols)
	for i := 0; i < leftN; i++ {
		allocateColData(&output.Cols[i], UT.BatchSize, leftTypes[i])
		output.Cols[i].Name = leftNames[i]
	}
	for i := 0; i < j.buildN; i++ {
		allocateColData(&output.Cols[leftN+i], UT.BatchSize, j.rightTypes[i])
		output.Cols[leftN+i].Name = j.rightNames[i]
	}

	for b := 0; b < j.nBuildRow && output.Size < UT.BatchSize; b++ {
		if j.matchedBuild[b] {
			continue
		}
		// Mark as emitted so next call skips it.
		j.matchedBuild[b] = true
		// Copy build columns (right side).
		for c := 0; c < j.buildN; c++ {
			copyRowToColumn(&output.Cols[leftN+c], &j.buildCols[c], output.Size, b)
		}
		// Left (probe) columns stay NULL.
		output.Size++
	}

	if output.Size == 0 {
		output.Put()
		return nil
	}
	return output
}

// nljEmitUnmatchedProbe emits unmatched probe rows with NULL build side.
// REQ001630: RIGHT/FULL outer join.
func (j *VectorizedNestedLoopJoin) nljEmitUnmatchedProbe() *UT.Batch {
	// Ensure the last probe batch is stored.
	if j.probeBatch != nil && j.rightOuter() {
		j.probeBatches = append(j.probeBatches, j.probeBatch)
		j.probeBatch = nil
	}
	if j.matchedProbe == nil || len(j.probeBatches) == 0 {
		return nil
	}
	leftN, leftNames, leftTypes := j.probeNamesTypes()
	nCols := leftN + j.buildN
	output := UT.GetBatch(nCols)
	for i := 0; i < leftN; i++ {
		allocateColData(&output.Cols[i], UT.BatchSize, leftTypes[i])
		output.Cols[i].Name = leftNames[i]
	}
	for i := 0; i < j.buildN; i++ {
		allocateColData(&output.Cols[leftN+i], UT.BatchSize, j.rightTypes[i])
		output.Cols[leftN+i].Name = j.rightNames[i]
	}

	probeRow := 0
	for batchIdx := 0; batchIdx < len(j.probeBatches) && output.Size < UT.BatchSize; batchIdx++ {
		batch := j.probeBatches[batchIdx]
		for r := 0; r < batch.Size && output.Size < UT.BatchSize; r++ {
			if probeRow < len(j.matchedProbe) && j.matchedProbe[probeRow] {
				probeRow++
				continue
			}
			if probeRow < len(j.matchedProbe) {
				j.matchedProbe[probeRow] = true
			}
			// Copy probe columns (left side).
			for c := 0; c < leftN && c < len(batch.Cols); c++ {
				copyRowToColumn(&output.Cols[c], &batch.Cols[c], output.Size, r)
			}
			// Build (right) columns stay NULL.
			output.Size++
			probeRow++
		}
	}

	if output.Size == 0 {
		output.Put()
		return nil
	}
	return output
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
