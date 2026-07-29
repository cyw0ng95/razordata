package PX

import (
	"context"
	"sort"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// SortStageSpec creates SortStage instances. A SortStage is a CatMapReduce
// stage that collects all child rows into memory, sorts them by the
// specified columns, and emits sorted result batches.
type SortStageSpec struct {
	SortCols  []int  // column indices to sort by
	Desc      []bool // true = descending for each column
	BatchSize int    // rows per output batch (0 = UT.BatchSize)
}

// NewRuntime creates a SortStage from this spec.
func (s *SortStageSpec) NewRuntime() Stage {
	batchSize := s.BatchSize
	if batchSize <= 0 {
		batchSize = UT.BatchSize
	}
	return &SortStage{
		sortCols:  s.SortCols,
		desc:      s.Desc,
		batchSize: batchSize,
	}
}

// Category returns CatMapReduce.
func (s *SortStageSpec) Category() StageCategory { return CatMapReduce }

// SortStage is a MapReduce-stage that sorts all child rows by the
// specified columns. It collects all rows in memory, sorts them,
// then emits result batches.
//
// Note: For large datasets, an external merge sort would be needed.
// This implementation is suitable for in-memory workloads that fit
// in the available memory budget.
type SortStage struct {
	child     Stage
	sortCols  []int
	desc      []bool
	batchSize int
	result    []*UT.Batch
	pos       int
	drained   bool
}

// keyedRow holds a reference to a row in a batch for sorting.
type keyedRow struct {
	keys   []sortKey
	batch  *UT.Batch
	rowIdx int
}

// SetChild sets the child stage (implements ChildSetter).
func (s *SortStage) SetChild(_ ChildSide, child Stage) {
	s.child = child
}

// PropagateExecContext stores the per-execution context for expression
// evaluation during sorting (e.g., sort key expressions that need session state). REQ002148.
func (s *SortStage) PropagateExecContext(ec *DT.ExecContext) {
	_ = ec // placeholder; sort stages may need this for complex sort expressions
}

// NextBatch collects all child rows, sorts them, and returns
// sorted result batches. Returns (nil, nil) at EOF.
func (s *SortStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if !s.drained {
		if err := s.drain(ctx); err != nil {
			return nil, err
		}
	}

	if s.pos >= len(s.result) {
		return nil, nil
	}

	batch := s.result[s.pos]
	s.pos++
	return batch, nil
}

// drain reads all child batches, collects rows, sorts, and builds
// result batches.
func (s *SortStage) drain(ctx context.Context) error {
	s.drained = true

	// Collect all rows.
	var batches []*UT.Batch
	for {
		batch, err := s.child.NextBatch(ctx)
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

	// Count total rows.
	totalRows := 0
	for _, b := range batches {
		totalRows += b.LogicalSize()
	}
	if totalRows == 0 {
		return nil
	}

	// Determine the number of populated columns from the first batch.
	nCols := 0
	for i := range batches[0].Cols {
		if batches[0].Cols[i].Type == 0 && batches[0].Cols[i].Name == "" {
			break
		}
		nCols = i + 1
	}

	// Build sort keys and row references.
	rows := make([]keyedRow, 0, totalRows)
	for _, b := range batches {
		size := b.LogicalSize()
		for r := 0; r < size; r++ {
			kr := keyedRow{keys: make([]sortKey, len(s.sortCols)), batch: b, rowIdx: r}
			for i, colIdx := range s.sortCols {
				kr.keys[i] = extractSortKey(b, colIdx, r)
			}
			rows = append(rows, kr)
		}
	}

	// Sort.
	sort.SliceStable(rows, func(i, j int) bool {
		for k := range s.sortCols {
			cmp := compareSortKeys(rows[i].keys[k], rows[j].keys[k])
			if cmp != 0 {
				if k < len(s.desc) && s.desc[k] {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})

	// Build result batches.
	s.result = make([]*UT.Batch, 0, (totalRows+s.batchSize-1)/s.batchSize)
	for len(rows) > 0 {
		n := s.batchSize
		if n > len(rows) {
			n = len(rows)
		}
		batch := UT.GetBatch(nCols)
		batch.Size = n
		for c := 0; c < nCols; c++ {
			batch.Cols[c].Name = batches[0].Cols[c].Name
			batch.Cols[c].Type = batches[0].Cols[c].Type
		}
		for c := 0; c < nCols; c++ {
			buildSortedColumn(&batch.Cols[c], rows[:n], c)
		}
		s.result = append(s.result, batch)
		rows = rows[n:]
	}

	// Release input batches.
	for _, b := range batches {
		b.Put()
	}

	return nil
}

// sortKey is a comparable value extracted from a column for sorting.
type sortKey struct {
	isNull bool
	i64    int64
	f64    float64
	s      string
	typ    LX.TokenType
}

// extractSortKey extracts a sort key from a batch column at the given row.
func extractSortKey(batch *UT.Batch, colIdx, rowIdx int) sortKey {
	if colIdx < 0 || colIdx >= len(batch.Cols) {
		return sortKey{isNull: true}
	}
	col := &batch.Cols[colIdx]

	// Handle selection vector.
	idx := rowIdx
	if batch.Sel != nil && rowIdx < len(batch.Sel) {
		idx = int(batch.Sel[rowIdx])
	}

	// Check for NULL.
	if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
		return sortKey{isNull: true, typ: col.Type}
	}

	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if idx < len(col.Data.Ints) {
			return sortKey{i64: col.Data.Ints[idx], typ: col.Type}
		}
	case LX.T_FLOAT_KW:
		if idx < len(col.Data.Floats) {
			return sortKey{f64: col.Data.Floats[idx], typ: col.Type}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if idx < len(col.Data.Strs) {
			return sortKey{s: col.Data.Strs[idx], typ: col.Type}
		}
	}
	return sortKey{isNull: true, typ: col.Type}
}

// compareSortKeys compares two sort keys. Returns -1, 0, or 1.
func compareSortKeys(a, b sortKey) int {
	// NULLs sort last.
	if a.isNull && b.isNull {
		return 0
	}
	if a.isNull {
		return 1
	}
	if b.isNull {
		return -1
	}

	switch a.typ {
	case LX.T_INT_KW, LX.T_BIGINT:
		if a.i64 < b.i64 {
			return -1
		}
		if a.i64 > b.i64 {
			return 1
		}
		return 0
	case LX.T_FLOAT_KW:
		if a.f64 < b.f64 {
			return -1
		}
		if a.f64 > b.f64 {
			return 1
		}
		return 0
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		if a.s < b.s {
			return -1
		}
		if a.s > b.s {
			return 1
		}
		return 0
	}
	return 0
}

// buildSortedColumn fills a column from the sorted row references.
func buildSortedColumn(dst *UT.Column, rows []keyedRow, colIdx int) {
	n := len(rows)
	if n == 0 {
		return
	}
	src := &rows[0].batch.Cols[colIdx]
	dst.Type = src.Type
	dst.Name = src.Name

	hasNulls := false
	for _, r := range rows {
		c := &r.batch.Cols[colIdx]
		idx := r.rowIdx
		if c.Nulls != nil && idx < len(c.Nulls) && c.Nulls[idx] {
			hasNulls = true
			break
		}
	}

	if hasNulls {
		dst.Nulls = make([]bool, n)
	}

	switch src.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		dst.Data.Ints = make([]int64, n)
		for i, r := range rows {
			c := &r.batch.Cols[colIdx]
			idx := r.rowIdx
			if c.Nulls != nil && idx < len(c.Nulls) && c.Nulls[idx] {
				dst.Nulls[i] = true
			} else if idx < len(c.Data.Ints) {
				dst.Data.Ints[i] = c.Data.Ints[idx]
			}
		}
	case LX.T_FLOAT_KW:
		dst.Data.Floats = make([]float64, n)
		for i, r := range rows {
			c := &r.batch.Cols[colIdx]
			idx := r.rowIdx
			if c.Nulls != nil && idx < len(c.Nulls) && c.Nulls[idx] {
				dst.Nulls[i] = true
			} else if idx < len(c.Data.Floats) {
				dst.Data.Floats[i] = c.Data.Floats[idx]
			}
		}
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		dst.Data.Strs = make([]string, n)
		for i, r := range rows {
			c := &r.batch.Cols[colIdx]
			idx := r.rowIdx
			if c.Nulls != nil && idx < len(c.Nulls) && c.Nulls[idx] {
				dst.Nulls[i] = true
			} else if idx < len(c.Data.Strs) {
				dst.Data.Strs[i] = c.Data.Strs[idx]
			}
		}
	case LX.T_BOOL:
		dst.Data.Bools = make([]bool, n)
		for i, r := range rows {
			c := &r.batch.Cols[colIdx]
			idx := r.rowIdx
			if c.Nulls != nil && idx < len(c.Nulls) && c.Nulls[idx] {
				dst.Nulls[i] = true
			} else if idx < len(c.Data.Bools) {
				dst.Data.Bools[i] = c.Data.Bools[idx]
			}
		}
	}
}

// Reset clears the sort state for plan cache reuse.
func (s *SortStage) Reset(_ context.Context) error {
	s.result = nil
	s.pos = 0
	s.drained = false
	return nil
}

// Close releases all resources held by the sort stage.
func (s *SortStage) Close() error {
	s.result = nil
	if s.child != nil {
		return s.child.Close()
	}
	return nil
}
