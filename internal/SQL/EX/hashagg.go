// Package EX's hashagg.go hosts HashAggregate, an alternative to the
// streaming Aggregate. Hash-based aggregation is a v1.1 extension; v1
// only requires the basic Aggregate operator. Retained for upcoming
// releases where grouped performance matters.
package EX

import (
	"context"
	"sort"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type HashAggregate struct {
	child     Operator
	groupCols []PS.Expr
	aggs      []PS.Expr
	keys      [][]interface{}
	buckets   map[string][]Row
	order     []string
	buf       []Row
	pos       int
	params    []interface{}
}

func NewHashAggregate(child Operator, groupCols, aggs []PS.Expr) *HashAggregate {
	return &HashAggregate{child: child, groupCols: groupCols, aggs: aggs, buckets: make(map[string][]Row)}
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (a *HashAggregate) WithParams(p []interface{}) Operator {
	a.params = p
	if a.child != nil {
		if w, ok := a.child.(interface{ WithParams([]interface{}) Operator }); ok {
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
		ks := distinctKey(Row{Data: key})
		if _, ok := a.buckets[ks]; !ok {
			a.buckets[ks] = nil
			a.keys = append(a.keys, key)
			a.order = append(a.order, ks)
		}
		a.buckets[ks] = append(a.buckets[ks], row)
	}
	sort.SliceStable(a.order, func(i, j int) bool {
		return keysLessByDistinct(a.buckets[a.order[i]][0], a.buckets[a.order[j]][0], a.groupCols)
	})
	for _, ks := range a.order {
		key := a.buckets[ks][0]
		keyVals, _ := evalGroupKey(a.groupCols, &key, a.params)
		out := Row{}
		for i, gc := range a.groupCols {
			out.Cols = append(out.Cols, groupColName(gc))
			out.Data = append(out.Data, keyVals[i])
		}
		for _, ag := range a.aggs {
			v, err := evalAggregateOver(ag, a.buckets[ks], a.params)
			if err != nil {
				return err
			}
			out.Cols = append(out.Cols, aggregateColName(ag))
			out.Data = append(out.Data, v)
		}
		a.buf = append(a.buf, out)
	}
	return nil
}

func keysLessByDistinct(a, b Row, groupCols []PS.Expr) bool {
	for _, gc := range groupCols {
		va, _ := Eval(gc, &a, nil)
		vb, _ := Eval(gc, &b, nil)
		c := compare(va, vb)
		if c < 0 {
			return true
		}
		if c > 0 {
			return false
		}
	}
	return false
}
