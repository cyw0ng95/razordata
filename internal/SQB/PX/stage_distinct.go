package PX

import (
	"context"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// DistinctStageSpec creates DistinctStage instances. A DistinctStage is a
// CatMapReduce stage that deduplicates rows across all child batches,
// emitting a single result batch with unique rows.
type DistinctStageSpec struct {
	KeyCols []int // column indices used for distinct comparison
}

// NewRuntime creates a DistinctStage from this spec.
func (s *DistinctStageSpec) NewRuntime() Stage {
	return &DistinctStage{
		keyCols: s.KeyCols,
	}
}

// Category returns CatMapReduce.
func (s *DistinctStageSpec) Category() StageCategory { return CatMapReduce }

// DistinctStage is a MapReduce-stage that deduplicates rows. It drains
// all child batches, builds a hash set of distinct row keys, and emits
// a single result batch with the unique rows.
type DistinctStage struct {
	child   Stage
	keyCols []int
	seen    map[string]bool
	result  []*UT.Batch
	pos     int
	drained bool
}

// SetChild sets the child stage (implements ChildSetter).
func (d *DistinctStage) SetChild(_ ChildSide, child Stage) {
	d.child = child
}

// NextBatch drains all child rows, deduplicates them, and returns
// result batches. Returns (nil, nil) at EOF.
func (d *DistinctStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if !d.drained {
		if err := d.drain(ctx); err != nil {
			return nil, err
		}
	}

	if d.pos >= len(d.result) {
		return nil, nil
	}

	batch := d.result[d.pos]
	d.pos++
	return batch, nil
}

// drain reads all child batches, deduplicates rows, and builds result.
func (d *DistinctStage) drain(ctx context.Context) error {
	d.drained = true
	d.seen = make(map[string]bool)

	var uniqueBatches []*UT.Batch
	var uniqueRows []struct {
		batch  *UT.Batch
		rowIdx int
	}

	for {
		batch, err := d.child.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}

		size := batch.LogicalSize()
		for r := 0; r < size; r++ {
			key := d.extractKey(batch, r)
			if !d.seen[key] {
				d.seen[key] = true
				uniqueBatches = append(uniqueBatches, batch)
				uniqueRows = append(uniqueRows, struct {
					batch  *UT.Batch
					rowIdx int
				}{batch: batch, rowIdx: r})
			}
		}
		batch.Put()
	}

	if len(uniqueRows) == 0 {
		return nil
	}

	// Build a single result batch from the unique rows.
	// Determine the number of columns.
	nCols := 0
	for i := range uniqueRows[0].batch.Cols {
		if uniqueRows[0].batch.Cols[i].Type == 0 && uniqueRows[0].batch.Cols[i].Name == "" {
			break
		}
		nCols = i + 1
	}

	out := UT.GetBatch(nCols)
	out.Size = len(uniqueRows)

	for c := 0; c < nCols; c++ {
		src := &uniqueRows[0].batch.Cols[c]
		out.Cols[c].Name = src.Name
		out.Cols[c].Type = src.Type

		hasNulls := false
		for _, r := range uniqueRows {
			col := &r.batch.Cols[c]
			idx := r.rowIdx
			if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
				hasNulls = true
				break
			}
		}
		if hasNulls {
			out.Cols[c].Nulls = make([]bool, len(uniqueRows))
		}

		switch src.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			out.Cols[c].Data.Ints = make([]int64, len(uniqueRows))
			for i, r := range uniqueRows {
				col := &r.batch.Cols[c]
				idx := r.rowIdx
				if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
					if out.Cols[c].Nulls != nil {
						out.Cols[c].Nulls[i] = true
					}
				} else if idx < len(col.Data.Ints) {
					out.Cols[c].Data.Ints[i] = col.Data.Ints[idx]
				}
			}
		case LX.T_FLOAT_KW:
			out.Cols[c].Data.Floats = make([]float64, len(uniqueRows))
			for i, r := range uniqueRows {
				col := &r.batch.Cols[c]
				idx := r.rowIdx
				if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
					if out.Cols[c].Nulls != nil {
						out.Cols[c].Nulls[i] = true
					}
				} else if idx < len(col.Data.Floats) {
					out.Cols[c].Data.Floats[i] = col.Data.Floats[idx]
				}
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			out.Cols[c].Data.Strs = make([]string, len(uniqueRows))
			for i, r := range uniqueRows {
				col := &r.batch.Cols[c]
				idx := r.rowIdx
				if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
					if out.Cols[c].Nulls != nil {
						out.Cols[c].Nulls[i] = true
					}
				} else if idx < len(col.Data.Strs) {
					out.Cols[c].Data.Strs[i] = col.Data.Strs[idx]
				}
			}
		case LX.T_BOOL:
			out.Cols[c].Data.Bools = make([]bool, len(uniqueRows))
			for i, r := range uniqueRows {
				col := &r.batch.Cols[c]
				idx := r.rowIdx
				if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
					if out.Cols[c].Nulls != nil {
						out.Cols[c].Nulls[i] = true
					}
				} else if idx < len(col.Data.Bools) {
					out.Cols[c].Data.Bools[i] = col.Data.Bools[idx]
				}
			}
		}
	}

	d.result = []*UT.Batch{out}
	return nil
}

// extractKey extracts a string-based deduplication key from a batch row.
func (d *DistinctStage) extractKey(batch *UT.Batch, rowIdx int) string {
	idx := rowIdx
	if batch.Sel != nil && rowIdx < len(batch.Sel) {
		idx = int(batch.Sel[rowIdx])
	}

	var buf []byte
	for _, colIdx := range d.keyCols {
		if colIdx < 0 || colIdx >= len(batch.Cols) {
			continue
		}
		col := &batch.Cols[colIdx]
		// NULL check
		if col.Nulls != nil && idx < len(col.Nulls) && col.Nulls[idx] {
			buf = append(buf, 0xFF) // NULL marker
			continue
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if idx < len(col.Data.Ints) {
				buf = fmt.Appendf(buf, "%d|", col.Data.Ints[idx])
			}
		case LX.T_FLOAT_KW:
			if idx < len(col.Data.Floats) {
				buf = fmt.Appendf(buf, "%g|", col.Data.Floats[idx])
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if idx < len(col.Data.Strs) {
				buf = append(buf, col.Data.Strs[idx]...)
				buf = append(buf, '|')
			}
		}
	}
	return string(buf)
}

// Reset clears the distinct state for plan cache reuse.
func (d *DistinctStage) Reset(_ context.Context) error {
	d.seen = nil
	d.result = nil
	d.pos = 0
	d.drained = false
	return nil
}

// Close releases all resources held by the distinct stage.
func (d *DistinctStage) Close() error {
	d.seen = nil
	d.result = nil
	if d.child != nil {
		return d.child.Close()
	}
	return nil
}
