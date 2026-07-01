package UT

import (
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// Backward-compat aliases for cross-package references.
type Operator = pl.Operator
type Row = pl.Row
type Value = pl.Value
type Store = DT.Store
type StoreSchema = DT.StoreSchema

// ErrNoRows signals end-of-stream from operators.
var ErrNoRows = pl.ErrNoRows

// SelRange is a contiguous inclusive-end run of row indices
// in a selection vector: [Start, End). It is used to compactly
// represent filtered batches where the surviving rows happen to
// form consecutive runs (typical of BETWEEN, range predicates,
// and time-range filters).
// A SelRange of {0, 1024} covers every row in a full batch and
// costs 4 bytes instead of 2 KiB for an equivalent []uint16.
// Multi-range selections (e.g. WHERE col IN (1,2,5,6)) become a
// slice of SelRange rather than a single flat index list.
// The trade-off: operators that consume a SelRange must iterate
// `for r := range ranges { for i := r.Start; i < r.End; i++ {} }`
// rather than `for _, idx := range sel`. That nested form is a
// SelRange is a contiguous inclusive-end run of row indices
// selected by a vectorized predicate. End is inclusive (so a
// single-row range has Start == End). The pair {Start, End} is
// a compact representation of the contiguous uint16 sequence
// Start, Start+1, ..., End — better fit for SIMD and for
// columnar kernels that already want contiguous row spans. For
// now the helpers exist alongside the existing []uint16 Sel
// field — adoption is downstream
// (see REQ000545 in REQUIREMENTS.md).
type SelRange struct {
	Start uint16
	End   uint16
}

// selToRanges compacts a flat selection vector into a sequence of
// contiguous inclusive-end runs. Consecutive indices where each
// next element equals previous + 1 are coalesced into a single
// SelRange. A gap (next != prev+1) terminates the current run
// and starts a new one.
// End is inclusive (so a single-row range has Start == End), which
// keeps End representable in uint16 without overflow at row 65535.
// The returned slice aliases no memory; callers may mutate it
// freely. Empty input yields a nil slice.
// Complexity: O(len(sel)) with a single forward pass and at most
// one SelRange emitted per run.
func selToRanges(sel []uint16) []SelRange {
	if len(sel) == 0 {
		return nil
	}
	out := make([]SelRange, 0, len(sel))
	runStart := sel[0]
	runEnd := sel[0]
	for i := 1; i < len(sel); i++ {
		v := sel[i]
		if v == runEnd+1 {
			runEnd = v
			continue
		}
		out = append(out, SelRange{Start: runStart, End: runEnd})
		runStart = v
		runEnd = v
	}
	out = append(out, SelRange{Start: runStart, End: runEnd})
	return out
}

// rangesToSel expands a sequence of inclusive-end runs back into
// a flat []uint16 selection vector. Each range {Start, End}
// contributes Start, Start+1, ..., End to the output.
// The returned slice is freshly allocated; callers may mutate
// or hand it to the existing Sel-based code paths. Empty input
// yields a nil slice.
// Complexity: O(sum of range widths) — caller should prefer
// the range form when contiguity is high.
func rangesToSel(ranges []SelRange) []uint16 {
	if len(ranges) == 0 {
		return nil
	}
	var total uint32
	for _, r := range ranges {
		total += uint32(r.End) - uint32(r.Start) + 1
	}
	out := make([]uint16, 0, total)
	for _, r := range ranges {
		// Use uint32 for the loop counter to avoid uint16 overflow
		// when End is uint16 max (65535).
		for v := uint32(r.Start); v <= uint32(r.End); v++ {
			out = append(out, uint16(v))
		}
	}
	return out
}

// BatchSize is the default number of rows per columnar batch.
// 1024 balances cache locality (fits in L1/L2) with per-batch
// overhead (allocation, function call). Tuned per SQL.md:412.
const BatchSize = 1024

// MaxColumns is the maximum number of columns in a single batch.
// 64 is more than sufficient for typical tables and bounds
// pre-allocation cost in the sync.Pool.
const MaxColumns = 64

// ColumnData is a concrete union of typed slices for columnar batch data.
// Exactly one field is non-nil at any time, determined by Column.Type.
// Avoids the interface boxing of `any` in the hot path.
type ColumnData struct {
	Ints   []int64
	Floats []float64
	Strs   []string
	Bools  []bool
}

// Column is a typed container for batch column data.
// Data is stored as a concrete slice ([]int64, []float64,
// []string, []bool) to avoid any boxing in the hot
// path. Use Type to switch on the concrete type.
type Column struct {
	Name  string
	Type  LX.TokenType
	Data  ColumnData
	Nulls []bool
}

// Batch represents a columnar batch of up to BatchSize rows.
// Memory is pooled via sync.Pool to avoid per-batch allocation.
// All column data is borrowed from the pool's pre-allocated
// slices when possible. Caller must call Put() to return the
// batch when done.
type Batch struct {
	Cols []Column

	// Sel is the selection vector (rows that passed filter).
	// If nil, all rows (Size) are valid.
	// Non-nil means the batch has a logical size of len(Sel),
	// but the underlying Data slices still have physical Size
	// elements (data is not compacted; Sel indexes into them).
	Sel []uint16

	// Size is the physical number of rows in this batch.
	Size int

	// Pooled indicates whether this batch came from the pool
	// and should be returned to it via Put(). Set to false if
	// a batch is constructed directly (e.g., in tests).
	Pooled bool

	// colMap provides O(1) lookup from column name to index.
	// Set by VectorizedSeqScan; nil for synthetic batches.
	colMap map[string]int
}

// batchPool is the global pool of Batch structs.
// Pre-allocates Cols slice with MaxColumns capacity to avoid
// re-allocation across batches. Data slices within each
// Column are allocated per-batch based on actual column types.
var batchPool = sync.Pool{
	New: func() any {
		return &Batch{
			Cols:   make([]Column, MaxColumns),
			Sel:    make([]uint16, BatchSize),
			Pooled: true,
		}
	},
}

// GetBatch retrieves a batch from the pool with capacity for
// the specified number of columns. The returned batch has
// Size=0, Sel=nil, and all column Data reset to nil. Caller
// must call Put() to return the batch when done.
// If cols > MaxColumns, a new batch is allocated directly
// (without pooling) and Pooled is set to false.
func GetBatch(cols int) *Batch {
	if cols > MaxColumns {
		// Oversized batch: allocate directly, no pooling.
		return &Batch{
			Cols:   make([]Column, cols),
			Sel:    make([]uint16, BatchSize),
			Pooled: false,
		}
	}

	b := batchPool.Get().(*Batch)
	b.Size = 0
	b.Sel = nil
	b.Pooled = true
	// Reset only the first 'cols' columns to avoid scanning
	// the entire pre-allocated slice.
	for i := 0; i < cols && i < MaxColumns; i++ {
		b.Cols[i].Type = 0
		b.Cols[i].Data = ColumnData{}
		b.Cols[i].Nulls = nil
	}
	return b
}

// Put returns a batch to the pool. No-op if the batch was not
// obtained from the pool (Pooled == false). All column Data
// and Nulls are released to GC; the batch struct itself is
// reset to zero-state for reuse.
func (b *Batch) Put() {
	if b == nil || !b.Pooled {
		return
	}
	b.Size = 0
	b.Sel = nil
	// Release column data for GC. We do not re-allocate the
	// slices in the pool to avoid keeping memory alive across
	// batches with different schemas. The sync.Pool will
	// reallocate on demand via the New function.
	for i := range b.Cols {
		b.Cols[i].Type = 0
		b.Cols[i].Data = ColumnData{}
		b.Cols[i].Nulls = nil
	}
	batchPool.Put(b)
}

// AppendRow adds a single value to the column at colIdx.
// Grows the underlying slice on first use. For typed columns
// (int64/float64/string/bool), this avoids allocations after
// the first append.
func (b *Batch) AppendRow(colIdx int, typ LX.TokenType, val any, isNull bool) {
	if colIdx >= len(b.Cols) {
		return // out of bounds, silently drop
	}
	col := &b.Cols[colIdx]
	col.Type = typ

	if isNull {
		if col.Nulls == nil {
			col.Nulls = make([]bool, BatchSize)
		}
		col.Nulls[b.Size] = true
	}

	switch typ {
	case LX.T_INT_KW, LX.T_BIGINT:
		if col.Data.Ints == nil {
			col.Data.Ints = make([]int64, BatchSize)
		}
		if v, ok := val.(int64); ok {
			col.Data.Ints[b.Size] = v
		}
	case LX.T_FLOAT_KW:
		if col.Data.Floats == nil {
			col.Data.Floats = make([]float64, BatchSize)
		}
		if v, ok := val.(float64); ok {
			col.Data.Floats[b.Size] = v
		}
	case LX.T_BOOL:
		if col.Data.Bools == nil {
			col.Data.Bools = make([]bool, BatchSize)
		}
		if v, ok := val.(bool); ok {
			col.Data.Bools[b.Size] = v
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if col.Data.Strs == nil {
			col.Data.Strs = make([]string, BatchSize)
		}
		if v, ok := val.(string); ok {
			col.Data.Strs[b.Size] = v
		}
	}
}

// SetColumnName sets the name of a column for O(1) lookup via colMap.
func (b *Batch) SetColumnName(colIdx int, name string) {
	if colIdx < len(b.Cols) {
		b.Cols[colIdx].Name = name
	}
}

// SetColMap installs a name-to-index map for O(1) column lookups.
// Called by VectorizedSeqScan after setting column names.
func (b *Batch) SetColMap(m map[string]int) {
	b.colMap = m
}

// AdvanceSize increments the batch's row count. Call after
// all columns have been populated for the current row.
func (b *Batch) AdvanceSize() {
	b.Size++
}

// IsFull returns true if the batch has reached BatchSize rows.
func (b *Batch) IsFull() bool {
	return b.Size >= BatchSize
}

// ColMap returns the name-to-index map for O(1) column lookups.
func (b *Batch) ColMap() map[string]int {
	return b.colMap
}

// Value returns the value at column colIdx and row rowIdx.
// Returns nil if the value is null or out of range.
func (b *Batch) Value(colIdx, rowIdx int) any {
	if colIdx < 0 || colIdx >= len(b.Cols) || rowIdx < 0 || rowIdx >= b.Size {
		return nil
	}
	col := &b.Cols[colIdx]
	if col.Nulls != nil && rowIdx < len(col.Nulls) && col.Nulls[rowIdx] {
		return nil
	}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if rowIdx < len(col.Data.Ints) {
			return col.Data.Ints[rowIdx]
		}
	case LX.T_FLOAT_KW:
		if rowIdx < len(col.Data.Floats) {
			return col.Data.Floats[rowIdx]
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if rowIdx < len(col.Data.Strs) {
			return col.Data.Strs[rowIdx]
		}
	case LX.T_BOOL:
		if rowIdx < len(col.Data.Bools) {
			return col.Data.Bools[rowIdx]
		}
	}
	return nil
}

// LogicalSize returns the number of valid rows in the batch,
// accounting for the selection vector. If Sel is nil, returns
// Size. Otherwise returns len(Sel).
func (b *Batch) LogicalSize() int {
	if b.Sel != nil {
		return len(b.Sel)
	}
	return b.Size
}

// BatchValueAt extracts the i-th value from a column.
func BatchValueAt(col Column, i int) any {
	if col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i] {
		return nil
	}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if i < len(col.Data.Ints) {
			return col.Data.Ints[i]
		}
	case LX.T_FLOAT_KW:
		if i < len(col.Data.Floats) {
			return col.Data.Floats[i]
		}
	case LX.T_BOOL:
		if i < len(col.Data.Bools) {
			return col.Data.Bools[i]
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if i < len(col.Data.Strs) {
			return col.Data.Strs[i]
		}
	}
	return nil
}
