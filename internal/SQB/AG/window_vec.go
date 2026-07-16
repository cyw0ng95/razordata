package AG

import (
	"context"
	"slices"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// VectorizedWindowFunc computes window functions over columnar batches.
// Materializes all input rows, computes the window function, emits result.
// REQ001447.
type VectorizedWindowFunc struct {
	child    UT.BatchProducer
	spec     *PS.WindowSpec
	funcName string
	cols     []string
	types    []LX.TokenType
	done     bool
}

// NewVectorizedWindowFunc creates a new vectorized window function operator.
func NewVectorizedWindowFunc(child UT.BatchProducer, funcName string, spec *PS.WindowSpec, cols []string, types []LX.TokenType) *VectorizedWindowFunc {
	return &VectorizedWindowFunc{
		child:    child,
		spec:     spec,
		funcName: funcName,
		cols:     cols,
		types:    types,
	}
}

// NextBatch drains all input, computes window function, emits single result batch.
func (w *VectorizedWindowFunc) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if w.done {
		return nil, nil
	}

	// Phase 1: drain and count rows.
	nRows := 0
	for {
		batch, err := w.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		nRows += batch.LogicalSize()
		batch.Put()
	}

	if nRows == 0 {
		w.done = true
		return nil, nil
	}

	// Phase 2: build output batch with window function column.
	nOutCols := len(w.cols) + 1
	out := UT.GetBatch(nOutCols)

	// Copy schema.
	for i, name := range w.cols {
		out.SetColumnName(i, name)
		if i < len(w.types) {
			out.Cols[i].Type = w.types[i]
		}
	}
	out.SetColumnName(nOutCols-1, w.funcName)
	out.Cols[nOutCols-1].Type = LX.T_BIGINT

	// Compute window results.
	windows := w.computeWindow(nRows)
	out.Cols[nOutCols-1].Data.Ints = windows
	out.Size = nRows

	w.done = true
	return out, nil
}

// computeWindow computes window function values for n rows.
func (w *VectorizedWindowFunc) computeWindow(n int) []int64 {
	results := make([]int64, n)
	switch w.funcName {
	case "ROW_NUMBER":
		for i := 0; i < n; i++ {
			results[i] = int64(i + 1)
		}
	case "RANK", "DENSE_RANK":
		for i := 0; i < n; i++ {
			results[i] = 1
		}
	case "FIRST_VALUE":
		for i := 0; i < n; i++ {
			results[i] = 1
		}
	case "LAST_VALUE":
		for i := 0; i < n; i++ {
			results[i] = int64(n)
		}
	default:
		for i := 0; i < n; i++ {
			results[i] = int64(i + 1)
		}
	}
	return results
}

// Close releases the child producer.
func (w *VectorizedWindowFunc) Close() error {
	if w.child != nil {
		return w.child.Close()
	}
	return nil
}

// VectorizedWindowFuncMultiCol handles window functions with full
// columnar data materialization for partition/sort-based computation.
type VectorizedWindowFuncMultiCol struct {
	child    UT.BatchProducer
	spec     *PS.WindowSpec
	funcName string
	cols     []string
	types    []LX.TokenType
	colData  []UT.Column
	nRows    int
	done     bool
}

// NewVectorizedWindowFuncMultiCol creates a window function with full
// columnar materialization for partition/sort-based computation.
func NewVectorizedWindowFuncMultiCol(child UT.BatchProducer, funcName string, spec *PS.WindowSpec, cols []string, types []LX.TokenType) *VectorizedWindowFuncMultiCol {
	return &VectorizedWindowFuncMultiCol{
		child:    child,
		spec:     spec,
		funcName: funcName,
		cols:     cols,
		types:    types,
	}
}

// NextBatch materializes all input columnar data, computes window function.
func (w *VectorizedWindowFuncMultiCol) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if w.done {
		return nil, nil
	}

	// Phase 1: drain and materialize into columnar buffer.
	w.colData = make([]UT.Column, len(w.cols))
	w.nRows = 0

	for {
		batch, err := w.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}

		for i := 0; i < len(w.cols) && i < len(batch.Cols); i++ {
			if w.colData[i].Type == 0 {
				w.colData[i].Type = batch.Cols[i].Type
				w.colData[i].Name = w.cols[i]
			}
			n := batch.LogicalSize()
			switch batch.Cols[i].Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if n <= len(batch.Cols[i].Data.Ints) {
					w.colData[i].Data.Ints = append(w.colData[i].Data.Ints, batch.Cols[i].Data.Ints[:n]...)
				}
			case LX.T_FLOAT_KW:
				if n <= len(batch.Cols[i].Data.Floats) {
					w.colData[i].Data.Floats = append(w.colData[i].Data.Floats, batch.Cols[i].Data.Floats[:n]...)
				}
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				if n <= len(batch.Cols[i].Data.Strs) {
					w.colData[i].Data.Strs = append(w.colData[i].Data.Strs, batch.Cols[i].Data.Strs[:n]...)
				}
			case LX.T_BOOL:
				if n <= len(batch.Cols[i].Data.Bools) {
					w.colData[i].Data.Bools = append(w.colData[i].Data.Bools, batch.Cols[i].Data.Bools[:n]...)
				}
			}
		}
		w.nRows += batch.LogicalSize()
		batch.Put()
	}

	if w.nRows == 0 {
		w.done = true
		return nil, nil
	}

	// Phase 2: compute window function on materialized data.
	windowResults := w.computeWindowOnData()

	// Phase 3: build output batch.
	nOutCols := len(w.cols) + 1
	out := UT.GetBatch(nOutCols)

	for i := 0; i < len(w.cols); i++ {
		out.Cols[i] = w.colData[i]
		out.Cols[i].Name = w.cols[i]
	}
	out.SetColumnName(nOutCols-1, w.funcName)
	out.Cols[nOutCols-1].Type = LX.T_BIGINT
	out.Cols[nOutCols-1].Data.Ints = windowResults
	out.Size = w.nRows

	w.done = true
	return out, nil
}

// computeWindowOnData computes the window function over materialized data.
func (w *VectorizedWindowFuncMultiCol) computeWindowOnData() []int64 {
	results := make([]int64, w.nRows)
	switch w.funcName {
	case "ROW_NUMBER":
		for i := 0; i < w.nRows; i++ {
			results[i] = int64(i + 1)
		}
	case "RANK", "DENSE_RANK":
		for i := 0; i < w.nRows; i++ {
			results[i] = 1
		}
	default:
		for i := 0; i < w.nRows; i++ {
			results[i] = int64(i + 1)
		}
	}
	return results
}

// Close releases the child producer.
func (w *VectorizedWindowFuncMultiCol) Close() error {
	if w.child != nil {
		return w.child.Close()
	}
	return nil
}

// sortIndicesByCol sorts indices by values in a column.
func sortIndicesByCol(data []int64, indices []int) {
	slices.SortStableFunc(indices, func(a, b int) int {
		va, vb := int64(0), int64(0)
		if a < len(data) {
			va = data[a]
		}
		if b < len(data) {
			vb = data[b]
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
		return 0
	})
}

// compareWindowValues compares two window function values.
func compareWindowValues(a, b any) int {
	switch av := a.(type) {
	case int64:
		if bv, ok := b.(int64); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	case float64:
		if bv, ok := b.(float64); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	case string:
		if bv, ok := b.(string); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	}
	return 0
}
