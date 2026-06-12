package EX

import (
	"context"
	"fmt"
	"sort"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// WindowOperator executes window functions over partitioned, sorted data.
type WindowOperator struct {
	input    Operator
	spec     *PS.WindowSpec
	funcName string
	args     []PS.Expr
	cols     []string
	rows     []Row
	results  []interface{}
	idx      int
	outCols  []string
	outData  []interface{}
}

// NewWindowOperator creates a window operator.
func NewWindowOperator(input Operator, funcName string, args []PS.Expr, spec *PS.WindowSpec, cols []string) *WindowOperator {
	return &WindowOperator{
		input:    input,
		spec:     spec,
		funcName: funcName,
		args:     args,
		cols:     cols,
		idx:      0,
	}
}

func (w *WindowOperator) Next(ctx context.Context) (Row, error) {
	if w.rows == nil {
		if err := w.materialize(ctx); err != nil {
			return Row{}, err
		}
	}
	if w.idx >= len(w.rows) {
		return Row{}, ErrNoRows
	}
	row := w.rows[w.idx]
	result := w.results[w.idx]
	w.idx++

	if w.outCols == nil {
		w.outCols = make([]string, len(w.cols)+1)
		copy(w.outCols, w.cols)
		w.outCols[len(w.cols)] = w.funcName
	}
	w.outData = w.outData[:0]
	if cap(w.outData) < len(w.cols)+1 {
		w.outData = make([]interface{}, len(w.cols)+1)
	} else {
		w.outData = w.outData[:len(w.cols)+1]
	}
	copy(w.outData, row.Data)
	w.outData[len(w.cols)] = result
	return Row{Cols: w.outCols, Data: w.outData}, nil
}

func (w *WindowOperator) Close() error {
	if w.input != nil {
		return w.input.Close()
	}
	return nil
}

func (w *WindowOperator) materialize(ctx context.Context) error {
	for {
		row, err := w.input.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return err
		}
		w.rows = append(w.rows, row)
	}

	if len(w.rows) == 0 {
		return nil
	}

	w.results = make([]interface{}, len(w.rows))
	partitions := w.partitionRows()
	for _, part := range partitions {
		w.sortPartition(part)
		w.computeWindowFunc(part)
	}
	return nil
}

func (w *WindowOperator) partitionRows() [][]int {
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

func (w *WindowOperator) partitionKey(row *Row) string {
	if len(w.spec.PartitionBy) == 0 {
		return ""
	}
	key := ""
	for i, expr := range w.spec.PartitionBy {
		val, err := Eval(expr, row, nil)
		if err != nil {
			val = nil
		}
		if i > 0 {
			key += "|"
		}
		key += fmt.Sprintf("%v", val)
	}
	return key
}

func (w *WindowOperator) sortPartition(indices []int) {
	if len(w.spec.OrderBy) == 0 {
		return
	}
	sort.SliceStable(indices, func(i, j int) bool {
		ri, rj := indices[i], indices[j]
		for _, item := range w.spec.OrderBy {
			vi, _ := Eval(item.Expr, &w.rows[ri], nil)
			vj, _ := Eval(item.Expr, &w.rows[rj], nil)
			cmp := compare(vi, vj)
			if cmp != 0 {
				if item.Desc {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})
}

func (w *WindowOperator) computeWindowFunc(indices []int) {
	switch w.funcName {
	case "ROW_NUMBER":
		for rank, idx := range indices {
			w.results[idx] = int64(rank + 1)
		}
	case "RANK":
		w.computeRank(indices, false)
	case "DENSE_RANK":
		w.computeRank(indices, true)
	case "LAG":
		w.computeLagLead(indices, -1)
	case "LEAD":
		w.computeLagLead(indices, 1)
	default:
		for _, idx := range indices {
			w.results[idx] = nil
		}
	}
}

func (w *WindowOperator) computeRank(indices []int, dense bool) {
	rank := int64(1)
	for i, idx := range indices {
		if i > 0 {
			prevIdx := indices[i-1]
			equal := true
			for _, item := range w.spec.OrderBy {
				vi, _ := Eval(item.Expr, &w.rows[prevIdx], nil)
				vj, _ := Eval(item.Expr, &w.rows[idx], nil)
				if compare(vi, vj) != 0 {
					equal = false
					break
				}
			}
			if !equal {
				if dense {
					rank++
				} else {
					rank = int64(i + 1)
				}
			}
		}
		w.results[idx] = rank
	}
}

func (w *WindowOperator) computeLagLead(indices []int, defaultOffset int) {
	n := len(w.args)
	defaultVal := interface{}(nil)
	offset := defaultOffset

	// REQ000290: read offset from args[1] if provided
	if n >= 2 {
		if v, err := Eval(w.args[1], nil, nil); err == nil {
			if ov, ok := toInt64(v); ok {
				offset = int(ov)
				if defaultOffset < 0 {
					offset = -offset // LAG: negative offset
				}
			}
		}
	}
	if n >= 3 {
		var err error
		defaultVal, err = Eval(w.args[2], nil, nil)
		if err != nil {
			defaultVal = nil
		}
	}

	for i, idx := range indices {
		srcPos := i + offset
		if srcPos < 0 || srcPos >= len(indices) {
			w.results[idx] = defaultVal
			continue
		}
		srcIdx := indices[srcPos]
		if n >= 1 {
			val, _ := Eval(w.args[0], &w.rows[srcIdx], nil)
			w.results[idx] = val
		} else {
			w.results[idx] = defaultVal
		}
	}
}

// evalWindowFunc evaluates a WindowFunc expression.
func evalWindowFunc(e *PS.WindowFunc, row *Row, params []interface{}) (interface{}, error) {
	return nil, fmt.Errorf("window function %s requires WindowOperator execution", e.Name)
}
