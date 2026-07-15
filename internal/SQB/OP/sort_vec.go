package OP

import (
	"context"
	"sort"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// VectorizedSort sorts an entire batch of rows by the given keys.
// It materializes all rows from the child, sorts them, and emits
// batches of up to batchSize rows in sorted order. REQ001441.
type VectorizedSort struct {
	child     UT.BatchProducer
	keys      []PS.OrderItem
	batchSize int
	// cols holds the columnar data, indexed by column index.
	cols []UT.Column
	// numRows is the total number of rows in cols.
	numRows int
	// pos is the current output row index.
	pos int
}

// NewVectorizedSort creates a vectorized sort operator.
func NewVectorizedSort(child UT.BatchProducer, keys []PS.OrderItem) *VectorizedSort {
	return &VectorizedSort{
		child:     child,
		keys:      keys,
		batchSize: UT.BatchSize,
	}
}

// NextBatch returns the next sorted batch. REQ001441.
func (s *VectorizedSort) NextBatch(ctx context.Context) (*UT.Batch, error) {
	// Phase 1: materialize all rows from child into columnar storage.
	if s.numRows == 0 {
		for {
			batch, err := s.child.NextBatch(ctx)
			if err != nil {
				return nil, err
			}
			if batch == nil {
				break
			}
			s.appendBatch(batch)
			batch.Put()
		}
		// Sort if we have data and keys.
		if len(s.keys) > 0 && s.numRows > 0 {
			s.doSort()
		}
	}

	// Phase 2: emit a batch of up to batchSize rows.
	if s.pos >= s.numRows {
		return nil, nil // EOF
	}
	end := s.pos + s.batchSize
	if end > s.numRows {
		end = s.numRows
	}

	out := UT.GetBatch(len(s.cols))
	for row := s.pos; row < end; row++ {
		for colIdx := range s.cols {
			if s.cols[colIdx].Type == 0 {
				continue
			}
			out.SetColumnName(colIdx, s.cols[colIdx].Name)
			out.Cols[colIdx].Type = s.cols[colIdx].Type
			switch s.cols[colIdx].Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if row < len(s.cols[colIdx].Data.Ints) {
					out.AppendRow(colIdx, LX.T_INT_KW, s.cols[colIdx].Data.Ints[row], false)
				}
			case LX.T_FLOAT_KW:
				if row < len(s.cols[colIdx].Data.Floats) {
					out.AppendRow(colIdx, LX.T_FLOAT_KW, s.cols[colIdx].Data.Floats[row], false)
				}
			case LX.T_BOOL:
				if row < len(s.cols[colIdx].Data.Bools) {
					out.AppendRow(colIdx, LX.T_BOOL, s.cols[colIdx].Data.Bools[row], false)
				}
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				if row < len(s.cols[colIdx].Data.Strs) {
					out.AppendRow(colIdx, LX.T_TEXT, s.cols[colIdx].Data.Strs[row], false)
				}
			}
		}
		out.AdvanceSize()
	}
	s.pos = end
	return out, nil
}

// appendBatch appends a batch's rows into the columnar buffer.
func (s *VectorizedSort) appendBatch(b *UT.Batch) {
	// Determine the actual number of columns.
	// We count columns that have non-zero Type AND non-nil Data.
	// This avoids stale pool data where Type might be non-zero but
	// Data is nil (meaning the column wasn't used in this batch).
	actualCols := 0
	for i := 0; i < len(b.Cols); i++ {
		if b.Cols[i].Type != 0 && (b.Cols[i].Data.Ints != nil || b.Cols[i].Data.Floats != nil || b.Cols[i].Data.Strs != nil || b.Cols[i].Data.Bools != nil) {
			actualCols = i + 1
		} else if actualCols > 0 {
			// We've seen non-zero columns, then hit a zero — stop.
			break
		}
	}
	// Only iterate over actual columns.
	for i := 0; i < actualCols; i++ {
		if b.Cols[i].Type == 0 {
			continue
		}
		if i >= len(s.cols) {
			s.cols = append(s.cols, UT.Column{})
		}
		s.cols[i].Type = b.Cols[i].Type
		s.cols[i].Name = b.Cols[i].Name
		s.cols[i].Nulls = append(s.cols[i].Nulls, b.Cols[i].Nulls...)
		// Only copy the first b.Size elements — AppendRow pre-allocates
		// BatchSize slots but only the first b.Size are populated.
		n := b.LogicalSize()
		switch b.Cols[i].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if len(b.Cols[i].Data.Ints) >= n {
				s.cols[i].Data.Ints = append(s.cols[i].Data.Ints, b.Cols[i].Data.Ints[:n]...)
			}
		case LX.T_FLOAT_KW:
			if len(b.Cols[i].Data.Floats) >= n {
				s.cols[i].Data.Floats = append(s.cols[i].Data.Floats, b.Cols[i].Data.Floats[:n]...)
			}
		case LX.T_BOOL:
			if len(b.Cols[i].Data.Bools) >= n {
				s.cols[i].Data.Bools = append(s.cols[i].Data.Bools, b.Cols[i].Data.Bools[:n]...)
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if len(b.Cols[i].Data.Strs) >= n {
				s.cols[i].Data.Strs = append(s.cols[i].Data.Strs, b.Cols[i].Data.Strs[:n]...)
			}
		}
	}
	s.numRows += b.LogicalSize()
}

// doSort sorts the columnar data by the sort keys.
func (s *VectorizedSort) doSort() {
	indices := make([]int, s.numRows)
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		for _, key := range s.keys {
			cmp := s.cmpKey(key, indices[i], indices[j])
			if cmp != 0 {
				if key.Desc {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})
	// Reorder columns according to sorted indices.
	for colIdx := range s.cols {
		col := &s.cols[colIdx]
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			reorderInts(col.Data.Ints, indices)
		case LX.T_FLOAT_KW:
			reorderFloats(col.Data.Floats, indices)
		case LX.T_BOOL:
			reorderBools(col.Data.Bools, indices)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			reorderStrs(col.Data.Strs, indices)
		}
	}
}

// cmpKey compares two row indices by a single sort key.
func (s *VectorizedSort) cmpKey(key PS.OrderItem, i, j int) int {
	ident, ok := key.Expr.(*PS.Ident)
	if !ok {
		return 0
	}
	colIdx := -1
	for ci, col := range s.cols {
		if col.Name == ident.Name {
			colIdx = ci
			break
		}
	}
	if colIdx < 0 {
		return 0
	}
	col := &s.cols[colIdx]
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		a, b := int64(0), int64(0)
		if i < len(col.Data.Ints) {
			a = col.Data.Ints[i]
		}
		if j < len(col.Data.Ints) {
			b = col.Data.Ints[j]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	case LX.T_FLOAT_KW:
		a, b := 0.0, 0.0
		if i < len(col.Data.Floats) {
			a = col.Data.Floats[i]
		}
		if j < len(col.Data.Floats) {
			b = col.Data.Floats[j]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		a, b := "", ""
		if i < len(col.Data.Strs) {
			a = col.Data.Strs[i]
		}
		if j < len(col.Data.Strs) {
			b = col.Data.Strs[j]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	return 0
}

// reorder helpers.
func reorderInts(vals []int64, indices []int) {
	n := len(indices)
	newVals := make([]int64, n)
	for i, idx := range indices {
		if idx < len(vals) {
			newVals[i] = vals[idx]
		}
	}
	for i := range vals {
		vals[i] = newVals[i]
	}
}

func reorderFloats(vals []float64, indices []int) {
	n := len(indices)
	newVals := make([]float64, n)
	for i, idx := range indices {
		if idx < len(vals) {
			newVals[i] = vals[idx]
		}
	}
	for i := range vals {
		vals[i] = newVals[i]
	}
}

func reorderBools(vals []bool, indices []int) {
	n := len(indices)
	newVals := make([]bool, n)
	for i, idx := range indices {
		if idx < len(vals) {
			newVals[i] = vals[idx]
		}
	}
	for i := range vals {
		vals[i] = newVals[i]
	}
}

func reorderStrs(vals []string, indices []int) {
	n := len(indices)
	newVals := make([]string, n)
	for i, idx := range indices {
		if idx < len(vals) {
			newVals[i] = vals[idx]
		}
	}
	for i := range vals {
		vals[i] = newVals[i]
	}
}

// Close releases resources.
func (s *VectorizedSort) Close() error {
	if s.child != nil {
		return s.child.Close()
	}
	return nil
}

// Req001441: ensure VectorizedSort and VectorizedLimit implement BatchProducer.
var _ UT.BatchProducer = (*VectorizedSort)(nil)
var _ UT.BatchProducer = (*VectorizedLimit)(nil)

// VectorizedLimit emits at most n rows from the child batch stream.
type VectorizedLimit struct {
	child     UT.BatchProducer
	limit     int64
	remaining int64
}

// NewVectorizedLimit creates a vectorized limit operator.
func NewVectorizedLimit(child UT.BatchProducer, n int64) *VectorizedLimit {
	return &VectorizedLimit{child: child, limit: n, remaining: n}
}

// NextBatch returns the next batch, capped at the remaining limit.
func (l *VectorizedLimit) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if l.remaining <= 0 {
		return nil, nil // EOF
	}
	batch, err := l.child.NextBatch(ctx)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, nil
	}
	// Cap the batch size to remaining limit.
	if int64(batch.LogicalSize()) > l.remaining {
		truncated := UT.GetBatch(len(batch.Cols))
		for i, col := range batch.Cols {
			if col.Type == 0 {
				continue
			}
			truncated.SetColumnName(i, col.Name)
			truncated.Cols[i].Type = col.Type
			rem := int(l.remaining)
			switch col.Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if col.Data.Ints != nil && len(col.Data.Ints) > rem {
					truncated.Cols[i].Data.Ints = col.Data.Ints[:rem]
				}
			case LX.T_FLOAT_KW:
				if col.Data.Floats != nil && len(col.Data.Floats) > rem {
					truncated.Cols[i].Data.Floats = col.Data.Floats[:rem]
				}
			case LX.T_BOOL:
				if col.Data.Bools != nil && len(col.Data.Bools) > rem {
					truncated.Cols[i].Data.Bools = col.Data.Bools[:rem]
				}
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				if col.Data.Strs != nil && len(col.Data.Strs) > rem {
					truncated.Cols[i].Data.Strs = col.Data.Strs[:rem]
				}
			}
		}
		truncated.Size = int(l.remaining)
		l.remaining = 0
		batch.Put()
		return truncated, nil
	}
	l.remaining -= int64(batch.LogicalSize())
	return batch, nil
}

// Close releases resources.
func (l *VectorizedLimit) Close() error {
	if l.child != nil {
		return l.child.Close()
	}
	return nil
}
