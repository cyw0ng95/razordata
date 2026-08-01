package PX

import (
	"context"
	"errors"
	"math"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// JoinKind distinguishes the type of join operation.
type JoinKind uint8

const (
	JoinKindInner JoinKind = iota
	JoinKindLeft
	JoinKindRight
	JoinKindFull
	JoinKindSemi
)

// HashJoinStageSpec creates HashJoinStage instances. A HashJoinStage is a
// CatJoin stage that performs an equi-join using a hash table built from
// the right (build) side, probing with the left (probe) side.
//
// It replaces VectorizedHashJoin, BatchHashCrossJoin, and serves as the
// primary join implementation in the unified pipeline architecture.
// REQ002180: ResolveKeysAtRuntime enables key resolution from the first
// batch's column metadata when static key indices are not available.
type HashJoinStageSpec struct {
	BuildKeys []int    // build-side column indices for equi-join key
	ProbeKeys []int    // probe-side column indices for equi-join key
	Kind      JoinKind // INNER / LEFT / RIGHT / FULL

	// REQ002180: runtime key resolution — used when keys cannot be resolved
	// against child output schemas at decomposition time.
	ResolveKeysAtRuntime bool
	LeftKeyName          string // probe-side key column name (for runtime resolution)
	RightKeyName         string // build-side key column name (for runtime resolution)
}

// NewRuntime creates a HashJoinStage from this spec.
func (s *HashJoinStageSpec) NewRuntime() Stage {
	return &HashJoinStage{
		buildKeys:            s.BuildKeys,
		probeKeys:            s.ProbeKeys,
		kind:                 s.Kind,
		resolveKeysAtRuntime: s.ResolveKeysAtRuntime,
		leftKeyName:          s.LeftKeyName,
		rightKeyName:         s.RightKeyName,
	}
}

// Category returns CatJoin.
func (s *HashJoinStageSpec) Category() StageCategory { return CatJoin }

// HashJoinStage is a Join-stage that performs a hash equi-join between
// two inputs (build and probe). The build side is fully materialized
// into a hash table on the first NextBatch() call; the probe side is
// streamed through and matched against the hash table.
//
// Output columns are [build_cols..., probe_cols...].
//
// Phases:
//   0 - matched rows (probe streaming with hash lookups)
//   1 - unmatched build rows (LEFT/FULL only)
//   2 - unmatched probe rows (RIGHT/FULL only)
type HashJoinStage struct {
	buildChild Stage
	probeChild Stage
	buildKeys  []int
	probeKeys  []int
	kind       JoinKind

	// REQ002180: runtime key resolution
	resolveKeysAtRuntime bool
	leftKeyName          string
	rightKeyName         string

	// Build-side state
	buildCols   []UT.Column // materialized build columns (flat arrays)
	buildN      int         // total build rows
	ht          UT.HashTableInterface
	rowIDs      [][]uint32 // slot → build row indices
	bloom       *UT.BloomFilter
	matchedBuild []bool    // which build rows matched (LEFT/FULL)

	// Probe-side state
	probeBatch *UT.Batch  // current probe batch
	probeRow   int        // current row in probe batch
	pending    []uint32   // pending build match IDs for current probe row
	pendingPos int        // position in pending
	probeDone  bool       // all probe batches consumed
	matchedProbe []bool   // which probe rows matched (RIGHT/FULL)
	probeBatches []*UT.Batch // retained probe batches for RIGHT/FULL
	totalProbeRows int

	// String-key hash table (used for non-integer key columns).
	// When non-nil, ht/rowIDs are nil and this map is used instead.
	stringHT map[string][]uint32

	// Output state
	phase        int // 0=matched, 1=unmatched build, 2=unmatched probe
	buildDone    bool
	nBuildCols   int
	nProbeCols   int
	unmatchedIdx int // cursor for unmatched build/probe emission
}

// SetChild sets the build or probe child (implements ChildSetter).
func (j *HashJoinStage) SetChild(side ChildSide, child Stage) {
	switch side {
	case LeftChild:
		j.probeChild = child
	case RightChild:
		j.buildChild = child
	default:
		// For SingleChild, treat as probe (single-input test helpers).
		j.probeChild = child
	}
}

// PropagateExecContext stores per-execution context for expression
// evaluation in join keys and predicates. REQ002148.
func (j *HashJoinStage) PropagateExecContext(ec *DT.ExecContext) {
	_ = ec
}

// NextBatch produces the next batch of join results.
// Returns (nil, nil) at EOF.
//
// Semantics (LeftChild=probe, RightChild=build):
//   - INNER: matched rows only
//   - LEFT:  all probe rows + matched build rows (unmatched probe → NULL build)
//   - RIGHT: all build rows + matched probe rows (unmatched build → NULL probe)
//   - FULL:  all rows from both sides
func (j *HashJoinStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !j.buildDone {
		if err := j.buildHashTable(ctx); err != nil {
			return nil, err
		}
		j.buildDone = true
	}

	// Empty build side special case.
	if j.buildN == 0 {
		return j.drainEmptyBuild(ctx)
	}

	for {
		switch j.phase {
		case 0:
			batch, err := j.probeMatched(ctx)
			if err != nil {
				return nil, err
			}
			if batch != nil {
				return batch, nil
			}
			// Probe exhausted → advance phase.
			switch j.kind {
			case JoinKindLeft:
				// LEFT: emit unmatched probe rows (NULL build cols)
				j.phase = 2 // phase 2 = unmatched probe
				j.unmatchedIdx = 0
				continue
			case JoinKindRight:
				// RIGHT: emit unmatched build rows (NULL probe cols)
				j.phase = 1 // phase 1 = unmatched build
				j.unmatchedIdx = 0
				continue
			case JoinKindFull:
				// FULL: emit unmatched build, then unmatched probe
				j.phase = 1
				j.unmatchedIdx = 0
				continue
			default:
				return nil, nil
			}

		case 1:
			// Unmatched build rows → NULL probe cols (RIGHT/FULL)
			batch := j.emitUnmatchedBuild()
			if batch != nil {
				return batch, nil
			}
			if j.kind == JoinKindFull {
				j.phase = 2
				j.unmatchedIdx = 0
				continue
			}
			return nil, nil

		case 2:
			// Unmatched probe rows → NULL build cols (LEFT/FULL)
			batch := j.emitUnmatchedProbe()
			if batch != nil {
				return batch, nil
			}
			return nil, nil
		}
	}
}

// buildHashTable drains the build child and constructs the hash table.
func (j *HashJoinStage) buildHashTable(ctx context.Context) error {
	// Collect all build batches.
	var batches []*UT.Batch
	for {
		batch, err := j.buildChild.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		batches = append(batches, batch)
	}

	if len(batches) == 0 {
		return nil
	}

	// Determine column count from first batch.
	j.nBuildCols = countColumns(batches[0])
	if j.nBuildCols == 0 {
		return nil
	}

	// Always materialize build column metadata (name, type) even when
	// totalRows is 0 — needed for correct output schema in outer joins.
	j.buildCols = make([]UT.Column, j.nBuildCols)
	for c := 0; c < j.nBuildCols; c++ {
		srcCol := &batches[0].Cols[c]
		j.buildCols[c].Name = srcCol.Name
		j.buildCols[c].Type = srcCol.Type
	}

	// REQ002180: resolve keys at runtime if not resolved during decomposition.
	if j.resolveKeysAtRuntime {
		j.probeKeys, j.buildKeys = j.resolveKeysFromNames(batches[0])
		j.resolveKeysAtRuntime = false
		if len(j.probeKeys) == 0 || len(j.buildKeys) == 0 {
			// Still can't resolve — fall through with empty keys (will
			// produce Cartesian product, matching LegacyBatchStageSpec behavior).
		}
	}

	// Count total rows.
	totalRows := 0
	for _, b := range batches {
		totalRows += b.LogicalSize()
	}
	j.buildN = totalRows
	if totalRows == 0 {
		return nil
	}

	// Materialize build column data.
	for c := 0; c < j.nBuildCols; c++ {
		srcCol := &batches[0].Cols[c]
		j.buildCols[c].Nulls = make([]bool, 0, totalRows)
		switch srcCol.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			j.buildCols[c].Data.Ints = make([]int64, 0, totalRows)
		case LX.T_FLOAT_KW:
			j.buildCols[c].Data.Floats = make([]float64, 0, totalRows)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			j.buildCols[c].Data.Strs = make([]string, 0, totalRows)
		case LX.T_BOOL:
			j.buildCols[c].Data.Bools = make([]bool, 0, totalRows)
		}
	}

	rowIdx := 0
	for _, b := range batches {
		size := b.LogicalSize()
		for r := 0; r < size; r++ {
			idx := r
			if b.Sel != nil && r < len(b.Sel) {
				idx = int(b.Sel[r])
			}
			for c := 0; c < j.nBuildCols; c++ {
				src := &b.Cols[c]
				dst := &j.buildCols[c]
				isNull := src.Nulls != nil && idx < len(src.Nulls) && src.Nulls[idx]
				dst.Nulls = append(dst.Nulls, isNull)
				switch src.Type {
				case LX.T_INT_KW, LX.T_BIGINT:
					if idx < len(src.Data.Ints) {
						dst.Data.Ints = append(dst.Data.Ints, src.Data.Ints[idx])
					} else {
						dst.Data.Ints = append(dst.Data.Ints, 0)
					}
				case LX.T_FLOAT_KW:
					if idx < len(src.Data.Floats) {
						dst.Data.Floats = append(dst.Data.Floats, src.Data.Floats[idx])
					} else {
						dst.Data.Floats = append(dst.Data.Floats, 0)
					}
				case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
					if idx < len(src.Data.Strs) {
						dst.Data.Strs = append(dst.Data.Strs, src.Data.Strs[idx])
					} else {
						dst.Data.Strs = append(dst.Data.Strs, "")
					}
				case LX.T_BOOL:
					if idx < len(src.Data.Bools) {
						dst.Data.Bools = append(dst.Data.Bools, src.Data.Bools[idx])
					} else {
						dst.Data.Bools = append(dst.Data.Bools, false)
					}
				}
			}
			rowIdx++
		}
	}

	// Build hash table for integer keys only (equi-join on int columns).
	// For non-int keys, we use a slower string-based fallback.
	allIntKeys := true
	for _, k := range j.buildKeys {
		if k < 0 || k >= len(j.buildCols) {
			allIntKeys = false
			break
		}
		t := j.buildCols[k].Type
		if t != LX.T_INT_KW && t != LX.T_BIGINT {
			allIntKeys = false
			break
		}
	}

	numKeys := len(j.buildKeys)
	if numKeys == 0 {
		// No equi-join keys — produce Cartesian product (all rows match).
		// REQ002307: skip hash table build to avoid Probe() with empty key array.
		// Mark all build rows as matched so the outer-join probe emits them.
		if j.kind == JoinKindLeft || j.kind == JoinKindFull {
			j.matchedBuild = make([]bool, j.buildN)
		}
		return nil
	}
	if allIntKeys {
		if numKeys == 1 {
			j.buildHashTableSingleInt()
		} else {
			j.buildHashTableCompositeInt()
		}
	} else {
		j.buildHashTableStringKey()
	}

	// Bloom filter for single-column int keys.
	if numKeys == 1 && allIntKeys && totalRows >= 64 {
		j.bloom = UT.NewBloomFilter(totalRows, 0.01)
		keyCol := j.buildKeys[0]
		for i := 0; i < totalRows; i++ {
			if !j.buildCols[keyCol].Nulls[i] {
				j.bloom.Add(uint64(j.buildCols[keyCol].Data.Ints[i]))
			}
		}
	}

	// Allocate matched-tracking arrays for outer joins.
	if j.kind == JoinKindLeft || j.kind == JoinKindFull {
		j.matchedBuild = make([]bool, totalRows)
	}

	// Release input batches.
	for _, b := range batches {
		b.Put()
	}

	return nil
}

// buildHashTableSingleInt builds the hash table for a single int64 key column.
func (j *HashJoinStage) buildHashTableSingleInt() {
	keyCol := j.buildKeys[0]
	col := &j.buildCols[keyCol]

	j.ht = UT.NewHashTableWithCols(uint32(j.buildN), 1)
	j.rowIDs = make([][]uint32, j.ht.Cap())

	keys := make([]int64, j.buildN)
	hashes := make([]uint64, j.buildN)
	n := 0
	for i := 0; i < j.buildN; i++ {
		if col.Nulls[i] {
			continue
		}
		keys[n] = col.Data.Ints[i]
		hashes[n] = fnv64uint64(uint64(col.Data.Ints[i]))
		n++
	}

	rowIdx := 0
	j.ht.ProbeInt64(keys, hashes, n, func(slot int, row int) {
		// row is the index in keys/hashes arrays; we need to map back
		// to the actual build row index.
		_ = row
		// Find the actual build row index: skip NULL keys.
		for rowIdx < j.buildN {
			if !col.Nulls[rowIdx] {
				actualRow := rowIdx
				rowIdx++
				j.rowIDs[slot] = append(j.rowIDs[slot], uint32(actualRow))
				return
			}
			rowIdx++
		}
	})
}

// buildHashTableCompositeInt builds the hash table for composite int keys.
func (j *HashJoinStage) buildHashTableCompositeInt() {
	numKeys := len(j.buildKeys)
	j.ht = UT.NewHashTableWithCols(uint32(j.buildN), numKeys)
	j.rowIDs = make([][]uint32, j.ht.Cap())

	// Flat-pack keys: row i's keys start at i*numKeys.
	keyArr := make([]int64, j.buildN*numKeys)
	hashes := make([]uint64, j.buildN)
	n := 0

	for i := 0; i < j.buildN; i++ {
		hasNull := false
		for k, colIdx := range j.buildKeys {
			if j.buildCols[colIdx].Nulls[i] {
				hasNull = true
				break
			}
			keyArr[n*numKeys+k] = j.buildCols[colIdx].Data.Ints[i]
		}
		if hasNull {
			continue
		}
		hashes[n] = UT.HashComposite(keyArr[n*numKeys : n*numKeys+numKeys])
		n++
	}

	rowIdx := 0
	j.ht.Probe(keyArr, hashes, n, func(slot int, row int) {
		_ = row
		for rowIdx < j.buildN {
			hasNull := false
			for _, colIdx := range j.buildKeys {
				if j.buildCols[colIdx].Nulls[rowIdx] {
					hasNull = true
					break
				}
			}
			if !hasNull {
				actualRow := rowIdx
				rowIdx++
				j.rowIDs[slot] = append(j.rowIDs[slot], uint32(actualRow))
				return
			}
			rowIdx++
		}
	})
}

// buildHashTableStringKey builds the hash table using string keys for
// non-integer column types (text, float, etc.). Uses FNV hash of string repr.
func (j *HashJoinStage) buildHashTableStringKey() {
	numKeys := len(j.buildKeys)

	// Build string keys for each row (composite keys concatenated with separators).
	type rowKey struct {
		key  string
		row  int
		null bool
	}

	keys := make([]rowKey, 0, j.buildN)
	for i := 0; i < j.buildN; i++ {
		hasNull := false
		keyParts := make([]byte, 0, 64)
		for k, colIdx := range j.buildKeys {
			if k > 0 {
				keyParts = append(keyParts, 0x00)
			}
			col := &j.buildCols[colIdx]
			if col.Nulls[i] {
				hasNull = true
				break
			}
			switch col.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				keyParts = appendUint64(keyParts, uint64(col.Data.Ints[i]))
			case LX.T_FLOAT_KW:
				keyParts = appendFloat64(keyParts, col.Data.Floats[i])
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				keyParts = append(keyParts, col.Data.Strs[i]...)
			case LX.T_BOOL:
				if col.Data.Bools[i] {
					keyParts = append(keyParts, '1')
				} else {
					keyParts = append(keyParts, '0')
				}
			}
		}
		if hasNull {
			keys = append(keys, rowKey{null: true, row: i})
		} else {
			keys = append(keys, rowKey{key: string(keyParts), row: i})
		}
	}

	// Build hash table using a map[string][]uint32 (simpler for string keys).
	htMap := make(map[string][]uint32, j.buildN)
	for _, rk := range keys {
		if rk.null {
			continue
		}
		htMap[rk.key] = append(htMap[rk.key], uint32(rk.row))
	}

	// Use the interface: wrap the map as a HashTableInterface-like structure.
	// For simplicity and correctness, we store the map directly and use a
	// simple lookup path.
	j.ht = nil // signal string-key path
	j.rowIDs = nil
	j.stringHT = htMap
	_ = numKeys
}

// probeMatched performs the matched-row probe phase. Returns a batch of
// matched rows, or nil when the probe side is exhausted.
func (j *HashJoinStage) probeMatched(ctx context.Context) (*UT.Batch, error) {
	// Ensure we have a probe batch.
	if j.probeBatch == nil && !j.probeDone {
		if err := j.refillProbe(ctx); err != nil {
			return nil, err
		}
		if j.probeBatch == nil {
			j.probeDone = true
			return nil, nil
		}
		j.nProbeCols = countColumns(j.probeBatch)

		// For LEFT/RIGHT/FULL, retain probe batches for unmatched emission.
		if j.kind != JoinKindInner {
			j.probeBatches = append(j.probeBatches, j.probeBatch)
		}
	}

	if j.probeDone {
		return nil, nil
	}

	batchSize := UT.BatchSize
	out := UT.GetBatch(j.nBuildCols + j.nProbeCols)
	out.Size = 0

	// Initialize output column metadata.
	for c := 0; c < j.nBuildCols; c++ {
		out.Cols[c].Name = j.buildCols[c].Name
		out.Cols[c].Type = j.buildCols[c].Type
	}
	for c := 0; c < j.nProbeCols; c++ {
		out.Cols[j.nBuildCols+c].Name = j.probeBatch.Cols[c].Name
		out.Cols[j.nBuildCols+c].Type = j.probeBatch.Cols[c].Type
	}

	// Allocate output column data.
	for c := 0; c < j.nBuildCols+j.nProbeCols; c++ {
		switch out.Cols[c].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			out.Cols[c].Data.Ints = make([]int64, 0, batchSize)
		case LX.T_FLOAT_KW:
			out.Cols[c].Data.Floats = make([]float64, 0, batchSize)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			out.Cols[c].Data.Strs = make([]string, 0, batchSize)
		case LX.T_BOOL:
			out.Cols[c].Data.Bools = make([]bool, 0, batchSize)
		}
		out.Cols[c].Nulls = make([]bool, 0, batchSize)
	}

	for out.Size < batchSize {
		// Drain pending matches first.
		if j.pendingPos < len(j.pending) {
			j.emitMatchedRow(out, j.pending[j.pendingPos])
			j.pendingPos++
			continue
		}

		// Advance probe row.
		if j.probeRow >= j.probeBatch.LogicalSize() {
			// For INNER, we can release the batch. For outer joins,
			// we retained it in probeBatches already.
			if j.kind == JoinKindInner {
				j.probeBatch.Put()
			}
			j.probeBatch = nil
			j.probeRow = 0
			j.pending = nil
			j.pendingPos = 0
			if err := j.refillProbe(ctx); err != nil {
				out.Put()
				return nil, err
			}
			if j.probeBatch == nil {
				j.probeDone = true
				break
			}
			if j.kind != JoinKindInner {
				j.probeBatches = append(j.probeBatches, j.probeBatch)
			}
			continue
		}

		// Probe current row.
		j.probeCurrentRow()
		j.probeRow++

		// If no matches for this row, skip to next.
		if len(j.pending) == 0 {
			continue
		}
		j.pendingPos = 0
	}

	if out.Size == 0 {
		out.Put()
		return nil, nil
	}
	return out, nil
}

// refillProbe fetches the next probe batch.
func (j *HashJoinStage) refillProbe(ctx context.Context) error {
	for {
		batch, err := j.probeChild.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			j.probeBatch = nil
			return nil
		}
		if batch.LogicalSize() == 0 {
			batch.Put()
			continue
		}
		j.probeBatch = batch
		j.probeRow = 0
		return nil
	}
}

// probeCurrentRow looks up the current probe row in the hash table and
// sets j.pending to the matching build row IDs.
func (j *HashJoinStage) probeCurrentRow() {
	j.pending = nil

	batch := j.probeBatch
	rowIdx := j.probeRow
	if batch.Sel != nil && rowIdx < len(batch.Sel) {
		rowIdx = int(batch.Sel[rowIdx])
	}

	// Check for NULL keys — NULLs never match.
	hasNull := false
	for _, k := range j.probeKeys {
		if k >= 0 && k < len(batch.Cols) {
			col := &batch.Cols[k]
			if col.Nulls != nil && rowIdx < len(col.Nulls) && col.Nulls[rowIdx] {
				hasNull = true
				break
			}
		} else {
			hasNull = true
			break
		}
	}
	if hasNull {
		return
	}

	// Determine if all probe key columns are int type.
	allInt := true
	for _, k := range j.probeKeys {
		if k < 0 || k >= len(batch.Cols) {
			allInt = false
			break
		}
		t := batch.Cols[k].Type
		if t != LX.T_INT_KW && t != LX.T_BIGINT {
			allInt = false
			break
		}
	}

	if j.stringHT != nil {
		// String-key path.
		key := j.probeStringKey(batch, rowIdx)
		if ids, ok := j.stringHT[key]; ok {
			j.pending = ids
			if j.matchedBuild != nil {
				for _, id := range ids {
					j.matchedBuild[id] = true
				}
			}
			if j.matchedProbe != nil {
				// Track matched probe rows.
				// We need to map local rowIdx → global probe row index.
				// This is computed during unmatched probe emission.
			}
		}
		return
	}

	// Int-key path.
	if allInt && j.ht != nil {
		numKeys := len(j.probeKeys)
		if numKeys == 1 {
			// Single int key.
			keyCol := j.probeKeys[0]
			col := &batch.Cols[keyCol]
			if rowIdx >= len(col.Data.Ints) {
				return
			}
			val := col.Data.Ints[rowIdx]

			// Bloom filter early exit.
			if j.bloom != nil {
				if !j.bloom.Contains(uint64(val)) {
					return
				}
			}

			hash := fnv64uint64(uint64(val))
			slot, found, _ := j.ht.Lookup([]int64{val}, hash)
			if found && slot < len(j.rowIDs) {
				j.pending = j.rowIDs[slot]
				if j.matchedBuild != nil {
					for _, id := range j.rowIDs[slot] {
						j.matchedBuild[id] = true
					}
				}
			}
		} else {
			// Composite int key.
			keyArr := make([]int64, numKeys)
			for i, k := range j.probeKeys {
				col := &batch.Cols[k]
				if rowIdx < len(col.Data.Ints) {
					keyArr[i] = col.Data.Ints[rowIdx]
				}
			}
			hash := UT.HashComposite(keyArr)
			slot, found, _ := j.ht.Lookup(keyArr, hash)
			if found && slot < len(j.rowIDs) {
				j.pending = j.rowIDs[slot]
				if j.matchedBuild != nil {
					for _, id := range j.rowIDs[slot] {
						j.matchedBuild[id] = true
					}
				}
			}
		}
	}
}

// probeStringKey builds the string key for the current probe row.
func (j *HashJoinStage) probeStringKey(batch *UT.Batch, rowIdx int) string {
	keyParts := make([]byte, 0, 64)
	for k, colIdx := range j.probeKeys {
		if k > 0 {
			keyParts = append(keyParts, 0x00)
		}
		if colIdx < 0 || colIdx >= len(batch.Cols) {
			return ""
		}
		col := &batch.Cols[colIdx]
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if rowIdx < len(col.Data.Ints) {
				keyParts = appendUint64(keyParts, uint64(col.Data.Ints[rowIdx]))
			}
		case LX.T_FLOAT_KW:
			if rowIdx < len(col.Data.Floats) {
				keyParts = appendFloat64(keyParts, col.Data.Floats[rowIdx])
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if rowIdx < len(col.Data.Strs) {
				keyParts = append(keyParts, col.Data.Strs[rowIdx]...)
			}
		case LX.T_BOOL:
			if rowIdx < len(col.Data.Bools) {
				if col.Data.Bools[rowIdx] {
					keyParts = append(keyParts, '1')
				} else {
					keyParts = append(keyParts, '0')
				}
			}
		}
	}
	return string(keyParts)
}

// emitMatchedRow appends one matched row (buildRowId + current probe row)
// to the output batch.
func (j *HashJoinStage) emitMatchedRow(out *UT.Batch, buildRowID uint32) {
	br := int(buildRowID)

	// Copy build columns.
	for c := 0; c < j.nBuildCols; c++ {
		src := &j.buildCols[c]
		dst := &out.Cols[c]
		dst.Nulls = append(dst.Nulls, src.Nulls[br])
		switch src.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			dst.Data.Ints = append(dst.Data.Ints, src.Data.Ints[br])
		case LX.T_FLOAT_KW:
			dst.Data.Floats = append(dst.Data.Floats, src.Data.Floats[br])
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			dst.Data.Strs = append(dst.Data.Strs, src.Data.Strs[br])
		case LX.T_BOOL:
			dst.Data.Bools = append(dst.Data.Bools, src.Data.Bools[br])
		}
	}

	// Copy probe columns (current probe row).
	batch := j.probeBatch
	rowIdx := j.probeRow - 1 // probeRow was already incremented
	if batch.Sel != nil && rowIdx < len(batch.Sel) {
		rowIdx = int(batch.Sel[rowIdx])
	}
	for c := 0; c < j.nProbeCols; c++ {
		src := &batch.Cols[c]
		dst := &out.Cols[j.nBuildCols+c]
		isNull := src.Nulls != nil && rowIdx < len(src.Nulls) && src.Nulls[rowIdx]
		dst.Nulls = append(dst.Nulls, isNull)
		switch src.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			var v int64
			if rowIdx < len(src.Data.Ints) {
				v = src.Data.Ints[rowIdx]
			}
			dst.Data.Ints = append(dst.Data.Ints, v)
		case LX.T_FLOAT_KW:
			var v float64
			if rowIdx < len(src.Data.Floats) {
				v = src.Data.Floats[rowIdx]
			}
			dst.Data.Floats = append(dst.Data.Floats, v)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			var v string
			if rowIdx < len(src.Data.Strs) {
				v = src.Data.Strs[rowIdx]
			}
			dst.Data.Strs = append(dst.Data.Strs, v)
		case LX.T_BOOL:
			var v bool
			if rowIdx < len(src.Data.Bools) {
				v = src.Data.Bools[rowIdx]
			}
			dst.Data.Bools = append(dst.Data.Bools, v)
		}
	}

	out.Size++
}

// emitUnmatchedBuild emits build rows that had no probe match (RIGHT/FULL).
// Probe columns are NULL.
func (j *HashJoinStage) emitUnmatchedBuild() *UT.Batch {
	if j.kind != JoinKindRight && j.kind != JoinKindFull {
		return nil
	}

	batchSize := UT.BatchSize
	out := UT.GetBatch(j.nBuildCols + j.nProbeCols)
	out.Size = 0

	// Initialize column metadata (probe columns from first probe batch,
	// or infer from build for column count).
	for c := 0; c < j.nBuildCols; c++ {
		out.Cols[c].Name = j.buildCols[c].Name
		out.Cols[c].Type = j.buildCols[c].Type
	}
	// Probe column names/types: use stored metadata or leave empty.
	if j.nProbeCols > 0 && len(j.probeBatches) > 0 && j.probeBatches[0] != nil {
		for c := 0; c < j.nProbeCols; c++ {
			out.Cols[j.nBuildCols+c].Name = j.probeBatches[0].Cols[c].Name
			out.Cols[j.nBuildCols+c].Type = j.probeBatches[0].Cols[c].Type
		}
	}

	// Allocate output columns.
	for c := 0; c < j.nBuildCols+j.nProbeCols; c++ {
		switch out.Cols[c].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			out.Cols[c].Data.Ints = make([]int64, 0, batchSize)
		case LX.T_FLOAT_KW:
			out.Cols[c].Data.Floats = make([]float64, 0, batchSize)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			out.Cols[c].Data.Strs = make([]string, 0, batchSize)
		case LX.T_BOOL:
			out.Cols[c].Data.Bools = make([]bool, 0, batchSize)
		}
		out.Cols[c].Nulls = make([]bool, 0, batchSize)
	}

	for out.Size < batchSize && j.unmatchedIdx < j.buildN {
		if j.matchedBuild[j.unmatchedIdx] {
			j.unmatchedIdx++
			continue
		}

		// Build columns: copy from buildCols.
		for c := 0; c < j.nBuildCols; c++ {
			src := &j.buildCols[c]
			dst := &out.Cols[c]
			dst.Nulls = append(dst.Nulls, src.Nulls[j.unmatchedIdx])
			switch src.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				dst.Data.Ints = append(dst.Data.Ints, src.Data.Ints[j.unmatchedIdx])
			case LX.T_FLOAT_KW:
				dst.Data.Floats = append(dst.Data.Floats, src.Data.Floats[j.unmatchedIdx])
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				dst.Data.Strs = append(dst.Data.Strs, src.Data.Strs[j.unmatchedIdx])
			case LX.T_BOOL:
				dst.Data.Bools = append(dst.Data.Bools, src.Data.Bools[j.unmatchedIdx])
			}
		}

		// Probe columns: all NULL.
		for c := 0; c < j.nProbeCols; c++ {
			dst := &out.Cols[j.nBuildCols+c]
			dst.Nulls = append(dst.Nulls, true)
			switch dst.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				dst.Data.Ints = append(dst.Data.Ints, 0)
			case LX.T_FLOAT_KW:
				dst.Data.Floats = append(dst.Data.Floats, 0)
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				dst.Data.Strs = append(dst.Data.Strs, "")
			case LX.T_BOOL:
				dst.Data.Bools = append(dst.Data.Bools, false)
			}
		}

		j.unmatchedIdx++
		out.Size++
	}

	if out.Size == 0 {
		out.Put()
		return nil
	}
	return out
}

// emitUnmatchedProbe emits probe rows that had no build match (LEFT/FULL).
// Build columns are NULL.
func (j *HashJoinStage) emitUnmatchedProbe() *UT.Batch {
	if j.kind != JoinKindLeft && j.kind != JoinKindFull {
		return nil
	}

	// Compute matchedProbe on first call.
	if j.matchedProbe == nil {
		j.computeMatchedProbe()
	}

	if j.unmatchedIdx >= j.totalProbeRows {
		return nil
	}

	batchSize := UT.BatchSize
	out := UT.GetBatch(j.nBuildCols + j.nProbeCols)
	out.Size = 0

	// Initialize column metadata.
	for c := 0; c < j.nBuildCols; c++ {
		out.Cols[c].Name = j.buildCols[c].Name
		out.Cols[c].Type = j.buildCols[c].Type
	}

	// Allocate output columns.
	for c := 0; c < j.nBuildCols+j.nProbeCols; c++ {
		switch out.Cols[c].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			out.Cols[c].Data.Ints = make([]int64, 0, batchSize)
		case LX.T_FLOAT_KW:
			out.Cols[c].Data.Floats = make([]float64, 0, batchSize)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			out.Cols[c].Data.Strs = make([]string, 0, batchSize)
		case LX.T_BOOL:
			out.Cols[c].Data.Bools = make([]bool, 0, batchSize)
		}
		out.Cols[c].Nulls = make([]bool, 0, batchSize)
	}

	for out.Size < batchSize && j.unmatchedIdx < j.totalProbeRows {
		if j.matchedProbe[j.unmatchedIdx] {
			j.unmatchedIdx++
			continue
		}

		// Find the batch and row index for this global row number.
		batch, rowIdx := j.globalProbeRow(j.unmatchedIdx)
		if batch == nil {
			j.unmatchedIdx++
			continue
		}

		// Build columns: all NULL.
		for c := 0; c < j.nBuildCols; c++ {
			dst := &out.Cols[c]
			dst.Nulls = append(dst.Nulls, true)
			switch dst.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				dst.Data.Ints = append(dst.Data.Ints, 0)
			case LX.T_FLOAT_KW:
				dst.Data.Floats = append(dst.Data.Floats, 0)
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				dst.Data.Strs = append(dst.Data.Strs, "")
			case LX.T_BOOL:
				dst.Data.Bools = append(dst.Data.Bools, false)
			}
		}

		// Probe columns: copy from probe batch.
		for c := 0; c < j.nProbeCols; c++ {
			src := &batch.Cols[c]
			dst := &out.Cols[j.nBuildCols+c]
			dst.Name = src.Name
			dst.Type = src.Type
			isNull := src.Nulls != nil && rowIdx < len(src.Nulls) && src.Nulls[rowIdx]
			dst.Nulls = append(dst.Nulls, isNull)
			switch src.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				var v int64
				if rowIdx < len(src.Data.Ints) {
					v = src.Data.Ints[rowIdx]
				}
				dst.Data.Ints = append(dst.Data.Ints, v)
			case LX.T_FLOAT_KW:
				var v float64
				if rowIdx < len(src.Data.Floats) {
					v = src.Data.Floats[rowIdx]
				}
				dst.Data.Floats = append(dst.Data.Floats, v)
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				var v string
				if rowIdx < len(src.Data.Strs) {
					v = src.Data.Strs[rowIdx]
				}
				dst.Data.Strs = append(dst.Data.Strs, v)
			case LX.T_BOOL:
				var v bool
				if rowIdx < len(src.Data.Bools) {
					v = src.Data.Bools[rowIdx]
				}
				dst.Data.Bools = append(dst.Data.Bools, v)
			}
		}

		j.unmatchedIdx++
		out.Size++
	}

	if out.Size == 0 {
		out.Put()
		return nil
	}
	return out
}

// computeMatchedProbe determines which probe rows matched by replaying
// the probe against the hash table. Called once on first unmatched-probe
// emission for RIGHT/FULL joins.
func (j *HashJoinStage) computeMatchedProbe() {
	j.totalProbeRows = 0
	for _, b := range j.probeBatches {
		j.totalProbeRows += b.LogicalSize()
	}
	j.matchedProbe = make([]bool, j.totalProbeRows)

	globalIdx := 0
	for _, batch := range j.probeBatches {
		size := batch.LogicalSize()
		for r := 0; r < size; r++ {
			rowIdx := r
			if batch.Sel != nil && r < len(batch.Sel) {
				rowIdx = int(batch.Sel[r])
			}

			// Check for NULL keys.
			hasNull := false
			for _, k := range j.probeKeys {
				if k < 0 || k >= len(batch.Cols) {
					hasNull = true
					break
				}
				col := &batch.Cols[k]
				if col.Nulls != nil && rowIdx < len(col.Nulls) && col.Nulls[rowIdx] {
					hasNull = true
					break
				}
			}
			if hasNull {
				globalIdx++
				continue
			}

			matched := false
			if j.stringHT != nil {
				key := j.probeStringKey(batch, rowIdx)
				_, matched = j.stringHT[key]
			} else if j.ht != nil {
				numKeys := len(j.probeKeys)
				// Check int keys.
				allInt := true
				for _, k := range j.probeKeys {
					if k < 0 || k >= len(batch.Cols) {
						allInt = false
						break
					}
					t := batch.Cols[k].Type
					if t != LX.T_INT_KW && t != LX.T_BIGINT {
						allInt = false
						break
					}
				}
				if allInt {
					if numKeys == 1 {
						keyCol := j.probeKeys[0]
						col := &batch.Cols[keyCol]
						if rowIdx < len(col.Data.Ints) {
							val := col.Data.Ints[rowIdx]
							hash := fnv64uint64(uint64(val))
							_, found, _ := j.ht.Lookup([]int64{val}, hash)
							matched = found
						}
					} else {
						keyArr := make([]int64, numKeys)
						valid := true
						for i, k := range j.probeKeys {
							col := &batch.Cols[k]
							if rowIdx < len(col.Data.Ints) {
								keyArr[i] = col.Data.Ints[rowIdx]
							} else {
								valid = false
								break
							}
						}
						if valid {
							hash := UT.HashComposite(keyArr)
							_, found, _ := j.ht.Lookup(keyArr, hash)
							matched = found
						}
					}
				}
			}

			j.matchedProbe[globalIdx] = matched
			globalIdx++
		}
	}
}

// globalProbeRow maps a global probe row index to (batch, localRowIdx).
func (j *HashJoinStage) globalProbeRow(globalIdx int) (*UT.Batch, int) {
	remaining := globalIdx
	for _, b := range j.probeBatches {
		size := b.LogicalSize()
		if remaining < size {
			rowIdx := remaining
			if b.Sel != nil && remaining < len(b.Sel) {
				rowIdx = int(b.Sel[remaining])
			}
			return b, rowIdx
		}
		remaining -= size
	}
	return nil, 0
}

// drainEmptyBuild handles the case when the build side is empty.
// INNER: returns EOF.
// RIGHT/FULL: emits all probe rows with NULL build columns.
// LEFT: returns empty result (one row with NULLs for scalar? No — LEFT join with empty build returns all probe rows).
func (j *HashJoinStage) drainEmptyBuild(ctx context.Context) (*UT.Batch, error) {
	switch j.kind {
	case JoinKindInner:
		return nil, nil
	case JoinKindLeft, JoinKindFull:
		// LEFT join with empty build = all probe rows, build columns NULL.
		// This is the same as emitUnmatchedProbe but with build=NULL.
		if j.probeBatch == nil && !j.probeDone {
			if err := j.refillProbe(ctx); err != nil {
				return nil, err
			}
			if j.probeBatch == nil {
				j.probeDone = true
				return nil, nil
			}
			j.nProbeCols = countColumns(j.probeBatch)
		}
		if j.probeDone {
			return nil, nil
		}

		batchSize := UT.BatchSize
		out := UT.GetBatch(j.nBuildCols + j.nProbeCols)
		out.Size = 0

		// Initialize build column metadata from buildCols.
		for c := 0; c < j.nBuildCols; c++ {
			out.Cols[c].Name = j.buildCols[c].Name
			out.Cols[c].Type = j.buildCols[c].Type
		}

		for out.Size < batchSize {
			if j.probeRow >= j.probeBatch.LogicalSize() {
				j.probeBatch.Put()
				j.probeBatch = nil
				j.probeRow = 0
				if err := j.refillProbe(ctx); err != nil {
					out.Put()
					return nil, err
				}
				if j.probeBatch == nil {
					j.probeDone = true
					break
				}
				continue
			}

			rowIdx := j.probeRow
			if j.probeBatch.Sel != nil && j.probeRow < len(j.probeBatch.Sel) {
				rowIdx = int(j.probeBatch.Sel[j.probeRow])
			}

			// Build columns: all NULL.
			for c := 0; c < j.nBuildCols; c++ {
				if out.Cols[c].Nulls == nil {
					out.Cols[c].Nulls = make([]bool, 0, batchSize)
				}
				out.Cols[c].Nulls = append(out.Cols[c].Nulls, true)
				switch out.Cols[c].Type {
				case LX.T_INT_KW, LX.T_BIGINT:
					if out.Cols[c].Data.Ints == nil {
						out.Cols[c].Data.Ints = make([]int64, 0, batchSize)
					}
					out.Cols[c].Data.Ints = append(out.Cols[c].Data.Ints, 0)
				case LX.T_FLOAT_KW:
					if out.Cols[c].Data.Floats == nil {
						out.Cols[c].Data.Floats = make([]float64, 0, batchSize)
					}
					out.Cols[c].Data.Floats = append(out.Cols[c].Data.Floats, 0)
				case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
					if out.Cols[c].Data.Strs == nil {
						out.Cols[c].Data.Strs = make([]string, 0, batchSize)
					}
					out.Cols[c].Data.Strs = append(out.Cols[c].Data.Strs, "")
				case LX.T_BOOL:
					if out.Cols[c].Data.Bools == nil {
						out.Cols[c].Data.Bools = make([]bool, 0, batchSize)
					}
					out.Cols[c].Data.Bools = append(out.Cols[c].Data.Bools, false)
				}
			}

			// Probe columns: copy.
			for c := 0; c < j.nProbeCols; c++ {
				src := &j.probeBatch.Cols[c]
				dst := &out.Cols[j.nBuildCols+c]
				dst.Name = src.Name
				dst.Type = src.Type
				if dst.Nulls == nil {
					dst.Nulls = make([]bool, 0, batchSize)
				}
				isNull := src.Nulls != nil && rowIdx < len(src.Nulls) && src.Nulls[rowIdx]
				dst.Nulls = append(dst.Nulls, isNull)
				switch src.Type {
				case LX.T_INT_KW, LX.T_BIGINT:
					if dst.Data.Ints == nil {
						dst.Data.Ints = make([]int64, 0, batchSize)
					}
					var v int64
					if rowIdx < len(src.Data.Ints) {
						v = src.Data.Ints[rowIdx]
					}
					dst.Data.Ints = append(dst.Data.Ints, v)
				case LX.T_FLOAT_KW:
					if dst.Data.Floats == nil {
						dst.Data.Floats = make([]float64, 0, batchSize)
					}
					var v float64
					if rowIdx < len(src.Data.Floats) {
						v = src.Data.Floats[rowIdx]
					}
					dst.Data.Floats = append(dst.Data.Floats, v)
				case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
					if dst.Data.Strs == nil {
						dst.Data.Strs = make([]string, 0, batchSize)
					}
					var v string
					if rowIdx < len(src.Data.Strs) {
						v = src.Data.Strs[rowIdx]
					}
					dst.Data.Strs = append(dst.Data.Strs, v)
				case LX.T_BOOL:
					if dst.Data.Bools == nil {
						dst.Data.Bools = make([]bool, 0, batchSize)
					}
					var v bool
					if rowIdx < len(src.Data.Bools) {
						v = src.Data.Bools[rowIdx]
					}
					dst.Data.Bools = append(dst.Data.Bools, v)
				}
			}

			j.probeRow++
			out.Size++
		}

		if out.Size == 0 {
			out.Put()
			return nil, nil
		}
		return out, nil

	default: // JoinKindRight
		return nil, nil
	}
}

// Reset clears the join state for plan cache reuse.
// Preserves hash table capacity where possible.
func (j *HashJoinStage) Reset(_ context.Context) error {
	j.buildCols = nil
	j.buildN = 0
	j.ht = nil
	j.rowIDs = nil
	j.bloom = nil
	j.matchedBuild = nil
	j.probeBatch = nil
	j.probeRow = 0
	j.pending = nil
	j.pendingPos = 0
	j.probeDone = false
	j.matchedProbe = nil
	j.totalProbeRows = 0
	j.phase = 0
	j.buildDone = false
	j.nBuildCols = 0
	j.nProbeCols = 0
	j.unmatchedIdx = 0
	j.stringHT = nil

	// Release retained probe batches.
	for _, b := range j.probeBatches {
		b.Put()
	}
	j.probeBatches = nil

	return nil
}

// resolveKeysFromNames resolves key column indices from column names
// in the first batch. REQ002180: used when keys could not be resolved
// during decomposition (ResolveKeysAtRuntime).
// REQ002319: also try matching the bare name (after last dot) to handle
// aliased table scans where columns are prefixed with the alias.
func (j *HashJoinStage) resolveKeysFromNames(batch *UT.Batch) (probeKeys, buildKeys []int) {
	// Build-side: find rightKeyName in build columns.
	bk := make([]int, 0, 1)
	names := batch.ColNames()
	for i, name := range names {
		if name == j.rightKeyName {
			bk = append(bk, i)
			break
		}
		// Try bare name (after last dot) for aliased columns.
		if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
			if name[dot+1:] == j.rightKeyName {
				bk = append(bk, i)
				break
			}
		}
	}
	// Probe-side: find leftKeyName in probe columns.
	// We can't resolve probe keys from the build batch — we need the
	// probe batch. Use the build keys as probe keys for single-table
	// self-joins, or return empty for cross-table joins.
	if len(bk) == 0 {
		return nil, nil
	}
	// For single-column key, use the same index for probe.
	// This is a simplification — multi-column keys and cross-table
	// joins with different column names need full schema propagation.
	return bk, bk
}

// Close releases all resources held by the join stage.
func (j *HashJoinStage) Close() error {
	j.Reset(context.Background())

	var firstErr error
	if j.buildChild != nil {
		if err := j.buildChild.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if j.probeChild != nil {
		if err := j.probeChild.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SemiJoinStageSpec is the cached factory for SemiJoinStage. REQ002152.
// Unlike HashJoinStageSpec (static equi-key indices), the semi-join's
// match predicate is a runtime closure built by the planner's
// decorrelateExists (view_subquery.go) — it resolves column indices
// by name on each call and supports arbitrary comparison operators.
// We therefore carry the closure and fall back to a nested-loop probe
// rather than a hash table.
type SemiJoinStageSpec struct {
	// OnFunc tests whether a (probeRow, buildRow) pair matches. When
	// nil, every probe row with at least one build row matches (i.e.
	// a non-correlated EXISTS where the inner table is non-empty).
	OnFunc func(outer, inner *DT.Row) (bool, error)
}

func (s *SemiJoinStageSpec) NewRuntime() Stage {
	return &SemiJoinStage{on: s.OnFunc}
}

func (s *SemiJoinStageSpec) Category() StageCategory { return CatJoin }

// SemiJoinStage implements SEMI join semantics — emit probe rows where
// at least one matching build row exists. Used for correlated EXISTS
// and IN subqueries decorrelated by the planner. REQ002152.
//
// The match predicate is a closure (OnFunc) that takes (probe, build)
// row pointers, so we materialize the build side into []pl.Row once
// and nested-loop probe it for each probe row. This mirrors the
// row-based NestedLoopJoin SEMI path (OP/join.go:341) but operates
// inside the batch pipeline: probe batches are converted to rows,
// tested, and matching rows are re-columnized into the output batch.
type SemiJoinStage struct {
	probeChild Stage // Left side (rows to potentially emit)
	buildChild Stage // Right side (existence check)
	on         func(outer, inner *DT.Row) (bool, error)

	// buildRows holds the fully-materialized build side. Built once on
	// the first NextBatch call; cleared on Reset.
	buildRows []DT.Row
	// buildExhausted marks that buildChild has returned EOF.
	buildExhausted bool

	// probeDone marks that probeChild has returned EOF.
	probeDone bool
}

func (s *SemiJoinStage) Category() StageCategory { return CatJoin }

func (s *SemiJoinStage) SetChild(side ChildSide, child Stage) {
	switch side {
	case LeftChild:
		s.probeChild = child
	case RightChild:
		s.buildChild = child
	}
}

// materializeBuild drains buildChild into s.buildRows. REQ002152.
func (s *SemiJoinStage) materializeBuild(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := s.buildChild.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		rows := batch.ToRows()
		if batch.Pooled {
			batch.Put()
		}
		s.buildRows = append(s.buildRows, rows...)
	}
	s.buildExhausted = true
	return nil
}

// NextBatch emits the next batch of probe rows that have at least one
// matching build row. Returns (nil, nil) at EOF. REQ002152.
func (s *SemiJoinStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if s.probeChild == nil || s.buildChild == nil {
		return nil, errors.New("px: semi-join stage not properly wired")
	}
	if s.probeDone {
		return nil, nil
	}

	// Build side is materialized once on first call.
	if !s.buildExhausted {
		if err := s.materializeBuild(ctx); err != nil {
			return nil, err
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := s.probeChild.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			s.probeDone = true
			return nil, nil
		}

		// Fast path: empty build side → no probe row can match. SEMI
		// emits nothing, so skip this batch entirely.
		if len(s.buildRows) == 0 {
			if batch.Pooled {
				batch.Put()
			}
			continue
		}

		logical := batch.LogicalSize()
		if logical == 0 {
			if batch.Pooled {
				batch.Put()
			}
			continue
		}

		names := batch.ColNames()
		nCols := len(names)
		out := UT.GetBatch(nCols)
		// Initialize output column names/types from the probe batch.
		// GetBatch already zeroed Data/Nulls for the first nCols columns.
		for c := 0; c < nCols; c++ {
			out.Cols[c].Name = batch.Cols[c].Name
			out.Cols[c].Type = batch.Cols[c].Type
		}
		out.Size = 0
		out.Sel = nil

		// Convert probe batch to rows so the closure can index by column
		// name (the ON closure from buildCorrelationFunc resolves columns
		// by scanning Cols — it does not understand columnar Data).
		probeRows := batch.ToRows()
		for r := 0; r < len(probeRows); r++ {
			probeRow := probeRows[r]
			matched := false
			// Nested-loop probe: stop at first match (short-circuit,
			// matching NestedLoopJoin SEMI semantics at OP/join.go:364).
			if s.on == nil {
				// No predicate → non-correlated EXISTS: any build row matches.
				matched = len(s.buildRows) > 0
			} else {
				for bi := range s.buildRows {
					buildRow := s.buildRows[bi]
					ok, oerr := s.on(&probeRow, &buildRow)
					if oerr != nil {
						if batch.Pooled {
							batch.Put()
						}
						return nil, oerr
					}
					if ok {
						matched = true
						break
					}
				}
			}
			if matched {
				// Append the probe row's values to each output column.
				for c := 0; c < nCols; c++ {
					val := probeRow.Data[c]
					isNull := val.Kind == DT.KindNull
					anyVal := val.ToAny()
					out.AppendRow(c, out.Cols[c].Type, anyVal, isNull)
				}
				out.Size++
			}
		}

		if batch.Pooled {
			batch.Put()
		}

		if out.Size > 0 {
			return out, nil
		}
		// No matches in this probe batch — continue to the next.
	}
}

func (s *SemiJoinStage) Reset(_ context.Context) error {
	s.buildRows = nil
	s.buildExhausted = false
	s.probeDone = false
	return nil
}

func (s *SemiJoinStage) Close() error {
	s.buildRows = nil
	if s.probeChild != nil {
		s.probeChild.Close()
	}
	if s.buildChild != nil {
		s.buildChild.Close()
	}
	return nil
}

// PropagateExecContext implements ExecContextPropagator. The ON closure
// resolves columns from row.Cols directly, so no execCtx wiring is
// needed on the stage itself; child stages receive execCtx via the
// pipeline's standard propagation.
func (s *SemiJoinStage) PropagateExecContext(_ *DT.ExecContext) {}

// --- helpers ---


// countColumns counts the number of populated columns in a batch.
func countColumns(batch *UT.Batch) int {
	n := 0
	for i := range batch.Cols {
		if batch.Cols[i].Type == 0 && batch.Cols[i].Name == "" {
			break
		}
		n = i + 1
	}
	return n
}

// fnv64uint64 computes FNV-1a 64-bit hash of a uint64 value.
func fnv64uint64(v uint64) uint64 {
	const (
		offset64 uint64 = 14695981039346656037
		prime64  uint64 = 1099511628211
	)
	h := offset64
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(v >> (i * 8)))
		h *= prime64
	}
	return h
}

// appendUint64 appends a uint64 as 8 little-endian bytes.
func appendUint64(b []byte, v uint64) []byte {
	for i := 0; i < 8; i++ {
		b = append(b, byte(v>>(i*8)))
	}
	return b
}

// appendFloat64 appends a float64 as its 8-byte representation.
func appendFloat64(b []byte, v float64) []byte {
return appendUint64(b, math.Float64bits(v))
}
