package PX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

type Shuffler interface {
	Accept(ctx context.Context, batch *UT.Batch, rowIdx int, groupKey any) error
	AcceptBatch(ctx context.Context, batch *UT.Batch) error
	Finalize(ctx context.Context) ([]*UT.Batch, error)
	Reset()
}

type KeyExtractor interface {
	NumKeys() int
}

type IntKeyExtractor interface {
	KeyExtractor
	ExtractKeys(batch *UT.Batch) (keys []int64, hashes []uint64, validRows []int)
}

type SimpleIntKeyExtractor struct {
	ColIdx  int
	scratch []int64
	hashes  []uint64
	valid   []int
}

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

func physicalRow(batch *UT.Batch, i int) int {
	if batch.Sel != nil {
		return int(batch.Sel[i])
	}
	return i
}

func hashInt64(x int64) uint64 {
	u := uint64(x)
	return u*0x9e3779b97f4a7c15 ^ (u >> 31)
}

type ScalarShuffler struct {
	reducer Reducer
	specs   []AccumulatorSpec
}

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

func (s *ScalarShuffler) DebugAccumCount() int {
	if r, ok := s.reducer.(*AggregateReducer); ok {
		return r.debugAccumCount()
	}
	return -1
}

func (s *ScalarShuffler) DebugHasValue() bool {
	if r, ok := s.reducer.(*AggregateReducer); ok {
		return r.debugAccumHasValue()
	}
	return false
}

func (s *ScalarShuffler) Finalize(ctx context.Context) ([]*UT.Batch, error) {
	return s.reducer.Finalize(nil)
}

func (s *ScalarShuffler) Reset() {
	s.reducer.Reset()
}

type HashShuffler struct {
	reducer   Reducer
	specs     []AccumulatorSpec
	extractor IntKeyExtractor
	ht        *UT.HashTable
	numGroups int

	scratchKeys   []int64
	scratchHashes []uint64
	scratchValid  []int
}

func NewHashShuffler(reducer Reducer, specs []AccumulatorSpec, extractor IntKeyExtractor) *HashShuffler {
	numKeys := extractor.NumKeys()
	ht := UT.NewHashTableWithCols(64, numKeys)
	ht.Payloads = make([]any, 0, 64)
	return &HashShuffler{
		reducer:   reducer,
		specs:     specs,
		extractor: extractor,
		ht:        ht,
	}
}

func (h *HashShuffler) Accept(ctx context.Context, batch *UT.Batch, rowIdx int, groupKey any) error {
	if h.ht == nil {
		h.ht = UT.NewHashTableWithCols(64, h.extractor.NumKeys())
		h.ht.Payloads = make([]any, 0, 64)
	}
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

type hashGroupKeySource struct {
	ht      *UT.HashTable
	numKeys int
	slots   []uint32
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

// SimpleStringKeyExtractor extracts a single string key column.
// REQ002192: supports string GROUP BY columns.
type SimpleStringKeyExtractor struct {
	ColIdx  int
	scratch []uint64
	hashes  []uint64
	valid   []int
}

// NewSimpleStringKeyExtractor creates a single-column string key extractor.
func NewSimpleStringKeyExtractor(colIdx int) *SimpleStringKeyExtractor {
	return &SimpleStringKeyExtractor{ColIdx: colIdx}
}

func (e *SimpleStringKeyExtractor) NumKeys() int { return 1 }

func (e *SimpleStringKeyExtractor) ExtractKeys(batch *UT.Batch) ([]uint64, []uint64, []int) {
	n := batch.LogicalSize()
	if cap(e.scratch) < n {
		e.scratch = make([]uint64, n)
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
		if src >= len(col.Data.Strs) || (src < len(col.Nulls) && col.Nulls[src]) {
			continue
		}
		val := col.Data.Strs[src]
		h := hashString(val)
		e.scratch[len(e.valid)] = h
		e.hashes[len(e.valid)] = h
		e.valid = append(e.valid, src)
	}
	return e.scratch[:len(e.valid)], e.hashes[:len(e.valid)], e.valid
}

// hashString computes a uint64 hash of a string using FNV-1a.
func hashString(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}