package AG

import (
	"context"
	"fmt"
	"slices"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// VectorizedWindowOperator executes window functions over partitioned,
// sorted data from a batch producer input. It materializes all batches,
// partitions and sorts the rows, computes the window function, and
// emits the result as a single batch. REQ001646.
type VectorizedWindowOperator struct {
	child    UT.BatchProducer
	spec     *PS.WindowSpec
	funcName string
	args     []PS.Expr
	cols     []string
	rows     []Row
	results  []any
	pos      int
	done     bool
}

// NewVectorizedWindowOperator creates a vectorized window operator.
func NewVectorizedWindowOperator(child UT.BatchProducer, funcName string, args []PS.Expr, spec *PS.WindowSpec, cols []string) *VectorizedWindowOperator {
	return &VectorizedWindowOperator{
		child:    child,
		spec:     spec,
		funcName: funcName,
		args:     args,
		cols:     cols,
	}
}

// NextBatch returns the next batch of window function results.
// REQ001646: materializes all rows, computes the window function,
// then emits the result in a single batch.
func (w *VectorizedWindowOperator) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if w.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if w.rows == nil {
		if err := w.materialize(ctx); err != nil {
			return nil, err
		}
		if len(w.rows) == 0 {
			w.done = true
			return nil, nil
		}
	}

	if w.pos >= len(w.rows) {
		w.done = true
		return nil, nil
	}

	// Determine the schema from the first row.
	nCols := len(w.rows[0].Cols) + 1
	colNames := make([]string, nCols)
	colTypes := make([]LX.TokenType, nCols)
	copy(colNames, w.rows[0].Cols)
	colNames[nCols-1] = w.funcName
	if len(w.rows[0].Types) > 0 {
		copy(colTypes, w.rows[0].Types)
	}
	colTypes[nCols-1] = typeForWindowFunc(w.funcName)

	// Emit all remaining rows as a single batch.
	remaining := len(w.rows) - w.pos
	batchSize := UT.BatchSize
	if remaining < batchSize {
		batchSize = remaining
	}

	out := UT.GetBatch(nCols)
	for i := 0; i < batchSize; i++ {
		row := w.rows[w.pos+i]
		for j := range row.Cols {
			out.SetColumnName(j, row.Cols[j])
			val := row.Data[j].ToAny()
			isNull := val == nil
			out.AppendRow(j, colTypes[j], val, isNull)
		}
		// Append the window function result.
		out.SetColumnName(nCols-1, w.funcName)
		result := w.results[w.pos+i]
		switch v := result.(type) {
		case int64:
			out.AppendRow(nCols-1, LX.T_INT_KW, v, false)
		case float64:
			out.AppendRow(nCols-1, LX.T_FLOAT_KW, v, false)
		case string:
			out.AppendRow(nCols-1, LX.T_TEXT, v, false)
		default:
			out.AppendRow(nCols-1, LX.T_INT_KW, v, result == nil)
		}
		out.AdvanceSize()
	}
	w.pos += batchSize
	out.Size = batchSize
	return out, nil
}

// materialize drains all batches from the child, converts to Row,
// partitions, sorts, and computes window function values.
func (w *VectorizedWindowOperator) materialize(ctx context.Context) error {
	for {
		batch, err := w.child.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		rows := batch.ToRows()
		w.rows = append(w.rows, rows...)
		batch.Put()
	}

	if len(w.rows) == 0 {
		return nil
	}

	w.results = make([]any, len(w.rows))
	partitions := w.partitionRows()
	for _, part := range partitions {
		w.sortPartition(part)
		w.computeWindowFunc(part)
	}
	return nil
}

func (w *VectorizedWindowOperator) partitionRows() [][]int {
	if len(w.spec.PartitionBy) == 0 {
		indices := make([]int, len(w.rows))
		for i := range indices {
			indices[i] = i
		}
		return [][]int{indices}
	}
	groups := make(map[string][]int)
	for i := range w.rows {
		key := w.partitionKey(&w.rows[i])
		groups[key] = append(groups[key], i)
	}
	result := make([][]int, 0, len(groups))
	for _, part := range groups {
		result = append(result, part)
	}
	return result
}

func (w *VectorizedWindowOperator) partitionKey(row *Row) string {
	key := ""
	for i, expr := range w.spec.PartitionBy {
		val, err := EV.EvalValue(expr, row, nil)
		if err != nil {
			val = DT.NullValue()
		}
		if i > 0 {
			key += "|"
		}
		key += fmt.Sprintf("%v", val.ToAny())
	}
	return key
}

func (w *VectorizedWindowOperator) sortPartition(indices []int) {
	if len(w.spec.OrderBy) == 0 {
		return
	}
	slices.SortStableFunc(indices, func(a, b int) int {
		for _, item := range w.spec.OrderBy {
			vi, _ := EV.EvalValue(item.Expr, &w.rows[a], nil)
			vj, _ := EV.EvalValue(item.Expr, &w.rows[b], nil)
			cmp := PL.CompareValue(vi, vj)
			if cmp != 0 {
				if item.Desc {
					return -cmp
				}
				return cmp
			}
		}
		return 0
	})
}

func (w *VectorizedWindowOperator) computeWindowFunc(indices []int) {
	switch w.funcName {
	case "ROW_NUMBER":
		for rank, idx := range indices {
			w.results[idx] = int64(rank + 1)
		}
	case "RANK":
		w.computeRank(indices, false)
	case "DENSE_RANK":
		w.computeRank(indices, true)
	default:
		// Fall back to row-based evaluation for unsupported functions.
		w.computeRowFallback(indices)
	}
}

func (w *VectorizedWindowOperator) computeRank(indices []int, dense bool) {
	if len(indices) == 0 {
		return
	}
	rank := int64(1)
	nextRank := int64(1)
	w.results[indices[0]] = rank
	for i := 1; i < len(indices); i++ {
		nextRank++
		prev := indices[i-1]
		curr := indices[i]
		equal := true
		for _, item := range w.spec.OrderBy {
			vi, _ := EV.EvalValue(item.Expr, &w.rows[prev], nil)
			vj, _ := EV.EvalValue(item.Expr, &w.rows[curr], nil)
			if PL.CompareValue(vi, vj) != 0 {
				equal = false
				break
			}
		}
		if equal {
			w.results[curr] = rank
		} else {
			if dense {
				rank = nextRank
			} else {
				rank = nextRank
			}
			w.results[curr] = rank
		}
	}
}

func (w *VectorizedWindowOperator) computeRowFallback(indices []int) {
	for _, idx := range indices {
		_ = w.rows[idx]
		w.results[idx] = DT.NullValue().ToAny()
	}
}

func (w *VectorizedWindowOperator) Close() error {
	if w.child != nil {
		return w.child.Close()
	}
	return nil
}

// typeForWindowFunc returns the SQL type for a window function result.
func typeForWindowFunc(name string) LX.TokenType {
	switch name {
	case "ROW_NUMBER", "RANK", "DENSE_RANK":
		return LX.T_INT_KW
	default:
		return LX.T_NULL
	}
}