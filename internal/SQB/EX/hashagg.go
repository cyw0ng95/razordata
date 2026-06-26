// Package EX's hashagg.go hosts HashAggregate, an alternative to the
// streaming Aggregate. Hash-based aggregation is a v1.1 extension; v1
// only requires the basic Aggregate operator. Retained for upcoming
// releases where grouped performance matters.
package EX

import (
	"context"
	"slices"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type HashAggregate struct {
	child      Operator
	groupCols  []PS.Expr
	aggs       []PS.Expr
	keys       [][]any
	buckets    map[string][]Row
	order      []string
	buf        []Row
	pos        int
	params     []any
	expandStar bool
}

func NewHashAggregate(child Operator, groupCols, aggs []PS.Expr) *HashAggregate {
	return &HashAggregate{child: child, groupCols: groupCols, aggs: aggs, buckets: make(map[string][]Row)}
}

// SetExpandStar enables full-row output for SELECT * with GROUP BY.
func (a *HashAggregate) SetExpandStar() { a.expandStar = true }

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (a *HashAggregate) WithParams(p []any) Operator {
	a.params = p
	if a.child != nil {
		if w, ok := a.child.(interface{ WithParams([]any) Operator }); ok {
			w.WithParams(p)
		}
	}
	return a
}

func (a *HashAggregate) Next(ctx context.Context) (Row, error) {
	if a.buf == nil {
		if err := a.materialize(ctx); err != nil {
			return Row{}, err
		}
	}
	if a.pos >= len(a.buf) {
		return Row{}, ErrNoRows
	}
	r := a.buf[a.pos]
	a.pos++
	return r, nil
}

func (a *HashAggregate) Close() error {
	a.buf = nil
	a.pos = 0
	a.buckets = make(map[string][]Row)
	a.keys = nil
	a.order = nil
	return a.child.Close()
}

func (a *HashAggregate) materialize(ctx context.Context) error {
	for {
		row, err := a.child.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return err
		}
		key, err := evalGroupKey(a.groupCols, &row, a.params)
		if err != nil {
			return err
		}
		ks := distinctKey(Row{Data: valueFromAnySlice(key)})
		if _, ok := a.buckets[ks]; !ok {
			a.buckets[ks] = nil
			a.keys = append(a.keys, key)
			a.order = append(a.order, ks)
		}
		a.buckets[ks] = append(a.buckets[ks], row)
	}
	slices.SortStableFunc(a.order, func(i, j string) int {
		ai := a.buckets[i][0]
		aj := a.buckets[j][0]
		return keysLessByDistinctCmp(ai, aj, a.groupCols)
	})
	for _, ks := range a.order {
		rows := a.buckets[ks]
		var out Row
		if a.expandStar {
			firstRow := rows[0]
			out = Row{
				Cols: make([]string, len(firstRow.Cols), len(firstRow.Cols)+len(a.aggs)),
				Data: make([]Value, len(firstRow.Data), len(firstRow.Data)+len(a.aggs)),
			}
			copy(out.Cols, firstRow.Cols)
			copy(out.Data, firstRow.Data)
			keyVals, _ := evalGroupKey(a.groupCols, &firstRow, a.params)
			for i, gc := range a.groupCols {
				name := groupColName(gc)
				for j, c := range out.Cols {
					if c == name {
						out.Data[j] = valueFromAny(keyVals[i])
						break
					}
				}
			}
		} else {
			key := rows[0]
			keyVals, _ := evalGroupKey(a.groupCols, &key, a.params)
			out = Row{}
			for i, gc := range a.groupCols {
				out.Cols = append(out.Cols, groupColName(gc))
				out.Data = append(out.Data, valueFromAny(keyVals[i]))
			}
		}
		for _, ag := range a.aggs {
			v, err := evalAggregateOver(ag, rows, a.params)
			if err != nil {
				return err
			}
			out.Cols = append(out.Cols, aggregateColName(ag))
			out.Data = append(out.Data, valueFromAny(v))
		}
		a.buf = append(a.buf, out)
	}
	return nil
}

func keysLessByDistinct(a, b Row, groupCols []PS.Expr) bool {
	return keysLessByDistinctCmp(a, b, groupCols) < 0
}

func keysLessByDistinctCmp(a, b Row, groupCols []PS.Expr) int {
	for _, gc := range groupCols {
		va, _ := EvalValue(gc, &a, nil)
		vb, _ := EvalValue(gc, &b, nil)
		c := compareValue(va, vb)
		if c != 0 {
			return c
		}
	}
	return 0
}
