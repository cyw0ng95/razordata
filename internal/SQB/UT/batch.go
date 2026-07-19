package UT

import (
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// Backward-compat aliases for cross-package references.
type Operator = pl.Operator
type Row = pl.Row
type Value = pl.Value
type Store = DT.Store
type StoreSchema = DT.StoreSchema

// ValueKind aliases so downstream packages can switch on Value.Kind
// without importing PL directly.
const (
	KindNull  = pl.KindNull
	KindInt   = pl.KindInt
	KindFloat = pl.KindFloat
	KindText  = pl.KindText
	KindBlob  = pl.KindBlob
	KindBool  = pl.KindBool
)

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

	// ExecCtx carries per-execution state for row-fallback paths
	// (e.g. subquery evaluation). Set by VectorizedProject before
	// calling EvalBatchExpr. The batch producer owns this field
	// and must ensure it is set before the batch is used for eval.
	// REQ001460.
	ExecCtx *pl.ExecContext
}

// batchPool is the global pool of Batch structs.
// Pre-allocates Cols slice with MaxColumns capacity to avoid
// re-allocation across batches. Data slices within each
// Column are allocated per-batch based on actual column types.

// GetBatch retrieves a batch from the pool with capacity for
// the specified number of columns. The returned batch has
// Size=0, Sel=nil, and all column Data reset to nil. Caller
// must call Put() to return the batch when done.
// If cols > MaxColumns, a new batch is allocated directly
// (without pooling) and Pooled is set to false.

var batchPool = sync.Pool{
	New: func() any {
		return &Batch{
			Cols:   make([]Column, MaxColumns),
			Sel:    make([]uint16, BatchSize),
			Pooled: true,
		}
	},
}

// columnDataPool pools per-(column-index) column data slices
// to eliminate per-batch allocations. Each Batch returns its
// column Data slices on Put() and reuses them on the next
// GetBatch() if the same schema is encountered. REQ001470
// follow-up.
//
// Keyed only by colIdx; the caller must keep the column Type
// consistent across calls (same column position must always
// be the same data type). For mixed-type columns at the same
// index, the pool entries will be stale but the per-type get
// functions ensure only matching slices are returned.
type columnDataPool struct {
	mu     sync.Mutex
	ints   [MaxColumns][][]int64
	floats [MaxColumns][][]float64
	strs   [MaxColumns][][]string
	bools  [MaxColumns][][]bool
}

var colDataPool = &columnDataPool{}

func (p *columnDataPool) getInts(cols, n int) []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.ints[cols] {
		if cap(p.ints[cols][i]) >= n {
			s := p.ints[cols][i][:n]
			last := len(p.ints[cols]) - 1
			p.ints[cols][i] = p.ints[cols][last]
			p.ints[cols][last] = nil
			p.ints[cols] = p.ints[cols][:last]
			return s
		}
	}
	return make([]int64, n)
}

func (p *columnDataPool) putInts(cols int, s []int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cols < MaxColumns {
		p.ints[cols] = append(p.ints[cols], s)
	}
}

func (p *columnDataPool) getFloats(cols, n int) []float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.floats[cols] {
		if cap(p.floats[cols][i]) >= n {
			s := p.floats[cols][i][:n]
			last := len(p.floats[cols]) - 1
			p.floats[cols][i] = p.floats[cols][last]
			p.floats[cols][last] = nil
			p.floats[cols] = p.floats[cols][:last]
			return s
		}
	}
	return make([]float64, n)
}

func (p *columnDataPool) putFloats(cols int, s []float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cols < MaxColumns {
		p.floats[cols] = append(p.floats[cols], s)
	}
}

func (p *columnDataPool) getStrs(cols, n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.strs[cols] {
		if cap(p.strs[cols][i]) >= n {
			s := p.strs[cols][i][:n]
			last := len(p.strs[cols]) - 1
			p.strs[cols][i] = p.strs[cols][last]
			p.strs[cols][last] = nil
			p.strs[cols] = p.strs[cols][:last]
			return s
		}
	}
	return make([]string, n)
}

func (p *columnDataPool) putStrs(cols int, s []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cols < MaxColumns {
		p.strs[cols] = append(p.strs[cols], s)
	}
}

func (p *columnDataPool) getBools(cols, n int) []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.bools[cols] {
		if cap(p.bools[cols][i]) >= n {
			s := p.bools[cols][i][:n]
			last := len(p.bools[cols]) - 1
			p.bools[cols][i] = p.bools[cols][last]
			p.bools[cols][last] = nil
			p.bools[cols] = p.bools[cols][:last]
			return s
		}
	}
	return make([]bool, n)
}

func (p *columnDataPool) putBools(cols int, s []bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cols < MaxColumns {
		p.bools[cols] = append(p.bools[cols], s)
	}
}

// PoolGetInts returns a pooled []int64 slice for column colIdx.
func PoolGetInts(colIdx, n int) []int64 { return colDataPool.getInts(colIdx, n) }

// PoolGetFloats returns a pooled []float64 slice for column colIdx.
func PoolGetFloats(colIdx, n int) []float64 { return colDataPool.getFloats(colIdx, n) }

// PoolGetStrs returns a pooled []string slice for column colIdx.
func PoolGetStrs(colIdx, n int) []string { return colDataPool.getStrs(colIdx, n) }

// PoolGetBools returns a pooled []bool slice for column colIdx.
func PoolGetBools(colIdx, n int) []bool { return colDataPool.getBools(colIdx, n) }

// StringInterner is a batch-scoped string interning map. Reuses the
// backing map across batches via Reset. REQ001481.
type StringInterner struct {
	m map[string]string
}

// Intern returns an interned (deduplicated) copy of s. Returns s
// unchanged for empty strings. The interner keeps the first-seen
// string for each unique byte sequence.
func (si *StringInterner) Intern(s string) string {
	if len(s) == 0 {
		return s
	}
	if si.m == nil {
		si.m = make(map[string]string)
	} else if cached, ok := si.m[s]; ok {
		return cached
	}
	si.m[s] = s
	return s
}

// Reset clears the interner for reuse across batches.
func (si *StringInterner) Reset() {
	si.m = nil
}

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
	b.ExecCtx = nil
	// Reset only the first 'cols' columns to avoid scanning
	// the entire pre-allocated slice. Reuse pooled column data.
	for i := 0; i < cols && i < MaxColumns; i++ {
		b.Cols[i].Type = 0
		b.Cols[i].Data = ColumnData{}
		b.Cols[i].Nulls = nil
	}
	return b
}

// Put returns a batch to the pool. No-op if the batch was not
// obtained from the pool (Pooled == false). Column data slices
// are returned to the columnDataPool for reuse.
func (b *Batch) Put() {
	if b == nil || !b.Pooled {
		return
	}
	b.Size = 0
	b.Sel = nil
	b.ExecCtx = nil
	for i := range b.Cols {
		col := &b.Cols[i]
		if col.Data.Ints != nil {
			colDataPool.putInts(i, col.Data.Ints)
			col.Data.Ints = nil
		}
		if col.Data.Floats != nil {
			colDataPool.putFloats(i, col.Data.Floats)
			col.Data.Floats = nil
		}
		if col.Data.Strs != nil {
			colDataPool.putStrs(i, col.Data.Strs)
			col.Data.Strs = nil
		}
		if col.Data.Bools != nil {
			colDataPool.putBools(i, col.Data.Bools)
			col.Data.Bools = nil
		}
		col.Nulls = nil
		col.Type = 0
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
			col.Data.Ints = colDataPool.getInts(colIdx, BatchSize)
		}
		if v, ok := val.(int64); ok {
			col.Data.Ints[b.Size] = v
		}
	case LX.T_FLOAT_KW:
		if col.Data.Floats == nil {
			col.Data.Floats = colDataPool.getFloats(colIdx, BatchSize)
		}
		if v, ok := val.(float64); ok {
			col.Data.Floats[b.Size] = v
		}
	case LX.T_BOOL:
		if col.Data.Bools == nil {
			col.Data.Bools = colDataPool.getBools(colIdx, BatchSize)
		}
		if v, ok := val.(bool); ok {
			col.Data.Bools[b.Size] = v
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if col.Data.Strs == nil {
			col.Data.Strs = colDataPool.getStrs(colIdx, BatchSize)
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

// ToValue converts a Batch cell (any-typed per the typed ColumnData
// union) back to the strongly-typed Value used by `pl.Row`. Returns
// `Value{Kind: KindNull}` for nulls and an out-of-bounds i. The
// underlying string/int/float/bool stays shared with the Batch
// (callers must not mutate); if isolation is needed, deep-copy via
// `toOwnedValue` before the Batch is Put.
//
// REQ001440: the executor boundary uses ToRows to materialise the
// final *Batch back into []pl.Row for the existing return contract.
func ToValue(col Column, i int) pl.Value {
	if col.Nulls != nil && i < len(col.Nulls) && col.Nulls[i] {
		return pl.Value{Kind: pl.KindNull}
	}
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if i < len(col.Data.Ints) {
			return pl.Value{Kind: pl.KindInt, I64: col.Data.Ints[i]}
		}
	case LX.T_FLOAT_KW:
		if i < len(col.Data.Floats) {
			return pl.Value{Kind: pl.KindFloat, F64: col.Data.Floats[i]}
		}
	case LX.T_BOOL:
		if i < len(col.Data.Bools) {
			return pl.Value{Kind: pl.KindBool, Bo: col.Data.Bools[i]}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if i < len(col.Data.Strs) {
			return pl.Value{Kind: pl.KindText, S: col.Data.Strs[i]}
		}
	}
	return pl.Value{Kind: pl.KindNull}
}

// ColNames returns the column-name slice corresponding to the
// populated prefix of `b.Cols`. Stops at the first zero-length
// type token (treated as "not allocated"). REQ001440.
func (b *Batch) ColNames() []string {
	out := make([]string, 0, len(b.Cols))
	for i := range b.Cols {
		if b.Cols[i].Name == "" && b.Cols[i].Type == 0 {
			continue
		}
		out = append(out, b.Cols[i].Name)
	}
	return out
}

// ToRows materializes the (possibly Sel-filtered) Batch to a
// `[]pl.Row`, sharing Cols across all rows so downstream callers
// see stable column names without copy. The Data slice per row is
// freshly allocated: Batch columnar data (split across Ints/Floats/
// Strs/Bools) is converted row-by-row into the per-row Value slice
// the existing `pl.Operator.Next()` contract returns. REQ001440.
func (b *Batch) ToRows() []pl.Row {
	if b == nil {
		return nil
	}
	names := b.ColNames()
	if len(names) == 0 {
		return nil
	}
	logical := b.LogicalSize()
	out := make([]pl.Row, 0, logical)
	for r := 0; r < logical; r++ {
		phys := r
		if b.Sel != nil {
			phys = int(b.Sel[r])
		}
		row := pl.Row{
			Cols: names,
		}
		row.Data = make([]pl.Value, len(names))
		for c := range names {
			row.Data[c] = ToValue(b.Cols[c], phys)
		}
		out = append(out, row)
	}
	return out
}

// ToRowsShared materializes a Batch to []pl.Row using a pre-allocated
// shared buffer for the per-row Data slices. The buffer is a flat slab
// where each row's Data is a sub-slice. Callers must not mutate the
// returned Data slices across calls.
//
// Returns (rows, usedBuffer). The usedBuffer can be returned to a
// sync.Pool for reuse. The buffer must have capacity >= logical * nCols.
// If buffer is nil or too small, a new buffer is allocated.
// REQ001638.
func (b *Batch) ToRowsShared(buffer []pl.Value) ([]pl.Row, []pl.Value) {
	if b == nil {
		return nil, buffer
	}
	names := b.ColNames()
	if len(names) == 0 {
		return nil, buffer
	}
	logical := b.LogicalSize()
	nCols := len(names)
	needed := logical * nCols
	if cap(buffer) < needed {
		buffer = make([]pl.Value, needed)
	} else {
		buffer = buffer[:needed]
	}

	out := make([]pl.Row, 0, logical)
	for r := 0; r < logical; r++ {
		phys := r
		if b.Sel != nil {
			phys = int(b.Sel[r])
		}
		off := r * nCols
		data := buffer[off : off+nCols : off+nCols]
		for c := range names {
			data[c] = ToValue(b.Cols[c], phys)
		}
		out = append(out, pl.Row{
			Cols: names,
			Data: data,
		})
	}
	return out, buffer
}
