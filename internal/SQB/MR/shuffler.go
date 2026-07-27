package MR

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// Shuffler organizes rows into groups and dispatches to a Reducer.
// During the map phase, rows are accepted one at a time via Accept.
// After all rows are processed, Finalize triggers the reduce phase
// and produces output batches.
type Shuffler interface {
	// Accept adds one row to the shuffle phase.
	// groupKey is currently unused by int64-keyed shufflers;
	// key extraction happens internally via AcceptBatch for efficiency.
	Accept(ctx context.Context, batch *UT.Batch, rowIdx int, groupKey any) error

	// AcceptBatch processes an entire batch at once. More efficient
	// than per-row Accept because it can use bulk key extraction and
	// hash table probing.
	AcceptBatch(ctx context.Context, batch *UT.Batch) error

	// Finalize completes the shuffle and triggers the reduce phase.
	// Returns the output batches produced by the Reducer.
	Finalize(ctx context.Context) ([]*UT.Batch, error)

	// Reset clears all state but preserves allocated capacity.
	Reset()
}

// KeyExtractor extracts group key values from a batch.
type KeyExtractor interface {
	// NumKeys returns the number of key columns.
	NumKeys() int
}

// IntKeyExtractor extracts int64 group keys from a batch.
type IntKeyExtractor interface {
	KeyExtractor
	// ExtractKeys extracts int64 keys for all logical rows in the batch.
	// Returns keys (flat-packed: row_i * numKeys + col), hashes (per row),
	// and validRows (physical indices of rows with all-NOT-NULL keys).
	ExtractKeys(batch *UT.Batch) (keys []int64, hashes []uint64, validRows []int)
}

// SimpleIntKeyExtractor extracts a single int64 key column.
type SimpleIntKeyExtractor struct {
	ColIdx  int
	scratch []int64
	hashes  []uint64
	valid   []int
}

// NewSimpleIntKeyExtractor creates a single-column int64 key extractor.
func NewSimpleIntKeyExtractor(colIdx int) *SimpleIntKeyExtractor {
	return &SimpleIntKeyExtractor{ColIdx: colIdx}
}

func (e *SimpleIntKeyExtractor) NumKeys() int { return 1 }

func (e *SimpleIntKeyExtractor) ExtractKeys(batch *UT.Batch) ([]int64, []uint64, []int) {
	n := batch.LogicalSize()
	if cap(e.scratch) < n {
		e.scratch = make([]int64, n)
		e.hashes = make([]uint64, n)
		e.valid = make([]int, 0, n)
	} else {
		e.scratch = e.scratch[:n]
		e.hashes = e.hashes[:n]
		e.valid = e.valid[:0]
	}

	col := batch.Cols[e.ColIdx]
	for i := 0; i < n; i++ {
		src := physicalRow(batch, i)
		if src >= len(col.Data.Ints) || (src < len(col.Nulls) && col.Nulls[src]) {
			continue
		}
		val := col.Data.Ints[src]
		e.scratch[len(e.valid)] = val
		e.hashes[len(e.valid)] = hashInt64(val)
		e.valid = append(e.valid, src)
	}
	return e.scratch[:len(e.valid)], e.hashes[:len(e.valid)], e.valid
}

// physicalRow returns the physical row index for logical row i.
func physicalRow(batch *UT.Batch, i int) int {
	if batch.Sel != nil {
		return int(batch.Sel[i])
	}
	return i
}

// hashInt64 computes a uint64 hash of an int64 key using FNV-1a mixing.
func hashInt64(x int64) uint64 {
	u := uint64(x)
	return u*0x9e3779b97f4a7c15 ^ (u >> 31)
}

// ScalarShuffler is a single-group shuffler for scalar aggregates.
// All rows go to group slot 0.
type ScalarShuffler struct {
	reducer Reducer
	specs   []AccumulatorSpec
}

// NewScalarShuffler creates a scalar (no GROUP BY) shuffler.
func NewScalarShuffler(reducer Reducer, specs []AccumulatorSpec) *ScalarShuffler {
	return &ScalarShuffler{reducer: reducer, specs: specs}
}

func (s *ScalarShuffler) Accept(ctx context.Context, batch *UT.Batch, rowIdx int, groupKey any) error {
	return s.reducer.Accumulate(0, batch, rowIdx)
}

func (s *ScalarShuffler) AcceptBatch(ctx context.Context, batch *UT.Batch) error {
	n := batch.LogicalSize()
	for i := 0; i < n; i++ {
		src := physicalRow(batch, i)
		if err := s.reducer.Accumulate(0, batch, src); err != nil {
			return err
		}
	}
	return nil
}

func (s *ScalarShuffler) Finalize(ctx context.Context) ([]*UT.Batch, error) {
	return s.reducer.Finalize(nil)
}

func (s *ScalarShuffler) Reset() {
	s.reducer.Reset()
}

// HashShuffler groups rows by int64 key(s) using a hash table.
// Uses UT.HashTable for slot management and payload indexing.
type HashShuffler struct {
	reducer   Reducer
	specs     []AccumulatorSpec
	extractor IntKeyExtractor
	ht        *UT.HashTable
	numGroups int

	// Scratch buffers reused across batches
	scratchKeys   []int64
	scratchHashes []uint64
	scratchValid  []int
}

// NewHashShuffler creates a hash-table-based shuffler for GROUP BY.
func NewHashShuffler(reducer Reducer, specs []AccumulatorSpec, extractor IntKeyExtractor) *HashShuffler {
	numKeys := extractor.NumKeys()
	ht := UT.NewHashTableWithCols(64, numKeys)
	ht.Payloads = make([]any, 0, 64) // enable payload tracking
	return &HashShuffler{
		reducer:   reducer,
		specs:     specs,
		extractor: extractor,
		ht:        ht,
	}
}

func (h *HashShuffler) Accept(ctx context.Context, batch *UT.Batch, rowIdx int, groupKey any) error {
	// Per-row accept is less efficient but supported for generality.
	if h.ht == nil {
		h.ht = UT.NewHashTableWithCols(64, h.extractor.NumKeys())
		h.ht.Payloads = make([]any, 0, 64)
	}
	// Extract single key
	val, ok := groupKey.(int64)
	if !ok {
		return nil
	}
	key := [1]int64{val}
	hash := hashInt64(val)
	h.ht.Probe(key[:], []uint64{hash}, 1, func(slotIdx int, _ int) {
		pIdx := h.ht.PayloadIdx[slotIdx]
		if pIdx < 0 || pIdx >= len(h.ht.Payloads) {
			return
		}
		_ = h.reducer.Accumulate(pIdx, batch, rowIdx)
	})
	return nil
}

func (h *HashShuffler) AcceptBatch(ctx context.Context, batch *UT.Batch) error {
	if h.ht == nil {
		h.ht = UT.NewHashTableWithCols(64, h.extractor.NumKeys())
		h.ht.Payloads = make([]any, 0, 64)
	}

	keys, hashes, validRows := h.extractor.ExtractKeys(batch)
	if len(validRows) == 0 {
		return nil
	}

	h.ht.Probe(keys, hashes, len(validRows), func(slotIdx int, row int) {
		pIdx := h.ht.PayloadIdx[slotIdx]
		if pIdx < 0 || pIdx >= len(h.ht.Payloads) {
			return
		}
		_ = h.reducer.Accumulate(pIdx, batch, validRows[row])
	})
	return nil
}

func (h *HashShuffler) Finalize(ctx context.Context) ([]*UT.Batch, error) {
	source := &hashGroupKeySource{ht: h.ht, numKeys: h.extractor.NumKeys()}
	return h.reducer.Finalize(source)
}

func (h *HashShuffler) Reset() {
	if h.ht != nil {
		// Reset hash table but keep capacity
		h.ht.Occupied = 0
		for i := range h.ht.Bitmap {
			h.ht.Bitmap[i] = 0
		}
		h.ht.Payloads = h.ht.Payloads[:0]
		for i := range h.ht.PayloadIdx {
			h.ht.PayloadIdx[i] = -1
		}
	}
	h.numGroups = 0
	h.reducer.Reset()
}

// hashGroupKeySource provides group key access from a HashTable.
// Group indices are sequential (0..N-1) matching payload insertion order.
type hashGroupKeySource struct {
	ht      *UT.HashTable
	numKeys int
	slots   []uint32 // slot index for each group (pIdx → slot)
}

func (s *hashGroupKeySource) buildIndex() {
	if s.slots != nil {
		return
	}
	s.slots = make([]uint32, len(s.ht.Payloads))
	for i := uint32(0); i < s.ht.Capacity; i++ {
		if (s.ht.Bitmap[i/64]>>(i%64))&1 == 0 {
			continue
		}
		pIdx := s.ht.PayloadIdx[i]
		if pIdx >= 0 && pIdx < len(s.ht.Payloads) {
			s.slots[pIdx] = i
		}
	}
}

func (s *hashGroupKeySource) GroupCount() int {
	s.buildIndex()
	return len(s.ht.Payloads)
}

func (s *hashGroupKeySource) KeyColumns() int {
	return s.numKeys
}

func (s *hashGroupKeySource) KeyValue(groupIdx int, colIdx int) (int64, bool) {
	if colIdx >= s.numKeys || groupIdx < 0 || groupIdx >= len(s.slots) {
		return 0, false
	}
	slot := int(s.slots[groupIdx])
	base := slot * s.numKeys
	if base+colIdx >= len(s.ht.Keys) {
		return 0, false
	}
	return s.ht.Keys[base+colIdx], true
}
