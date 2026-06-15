package EX

import (
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

// BatchSize is the default number of rows per columnar batch.
// 1024 balances cache locality (fits in L1/L2) with per-batch
// overhead (allocation, function call). Tuned per SQL.md:412.
const BatchSize = 1024

// MaxColumns is the maximum number of columns in a single batch.
// 64 is more than sufficient for typical tables and bounds
// pre-allocation cost in the sync.Pool.
const MaxColumns = 64

// Column is a typed container for batch column data.
// Data is stored as a concrete slice ([]int64, []float64,
// []string, []bool) to avoid interface{} boxing in the hot
// path. Use Type to switch on the concrete type.
type Column struct {
	Name  string
	Type  LX.TokenType
	Data  any
	Nulls []bool
}

// Batch represents a columnar batch of up to BatchSize rows.
// Memory is pooled via sync.Pool to avoid per-batch allocation.
// All column data is borrowed from the pool's pre-allocated
// slices when possible. Caller must call Put() to return the
// batch when done.
type Batch struct {
	// Cols holds column data in columnar layout.
	// Slice is pre-allocated in the pool; entries are
	// type-switched on the underlying Data field.
	Cols []Column

	// Sel is the selection vector (rows that passed filter).
	// If nil, all rows (Size) are valid.
	// Non-nil means the batch has a logical size of len(Sel),
	// but the underlying Data slices still have physical Size
	// elements (data is not compacted; Sel indexes into them).
	Sel []uint16

	// Size is the physical number of rows in this batch.
	// Logical size is Size if Sel is nil, else len(Sel).
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
//
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
		b.Cols[i].Data = nil
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
		b.Cols[i].Data = nil
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
		if col.Data == nil {
			col.Data = make([]int64, BatchSize)
		}
		if v, ok := val.(int64); ok {
			col.Data.([]int64)[b.Size] = v
		}
	case LX.T_FLOAT_KW:
		if col.Data == nil {
			col.Data = make([]float64, BatchSize)
		}
		if v, ok := val.(float64); ok {
			col.Data.([]float64)[b.Size] = v
		}
	case LX.T_BOOL:
		if col.Data == nil {
			col.Data = make([]bool, BatchSize)
		}
		if v, ok := val.(bool); ok {
			col.Data.([]bool)[b.Size] = v
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if col.Data == nil {
			col.Data = make([]string, BatchSize)
		}
		if v, ok := val.(string); ok {
			col.Data.([]string)[b.Size] = v
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
	if col.Data == nil {
		return nil
	}
	switch d := col.Data.(type) {
	case []int64:
		if rowIdx < len(d) {
			return d[rowIdx]
		}
	case []float64:
		if rowIdx < len(d) {
			return d[rowIdx]
		}
	case []string:
		if rowIdx < len(d) {
			return d[rowIdx]
		}
	case []bool:
		if rowIdx < len(d) {
			return d[rowIdx]
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
