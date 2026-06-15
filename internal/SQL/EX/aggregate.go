// Package EX's aggregate.go hosts the Aggregate and HashAggregate
// operators (COUNT/SUM/AVG/MIN/MAX plus grouped variants). These are
// working end-to-end against the executor's in-memory and engine-backed
// paths but are not part of the v1 MVP scope per docs/design/ARCH.md
// ("Out of Scope (v1)" — joins, aggregates, subqueries, DISTINCT).
// They are retained in v1.1 for upcoming releases.
package EX

import (
	"context"
	"fmt"
	"sort"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Aggregate struct {
	child     Operator
	groupCols []PS.Expr
	aggs      []PS.Expr
	buf       []Row
	pos       int
	params    []interface{}
}

func NewAggregate(child Operator, groupCols, aggs []PS.Expr) *Aggregate {
	return &Aggregate{child: child, groupCols: groupCols, aggs: aggs}
}

// WithParams propagates the bound `?` placeholders to this
// operator and its child (R16-1..2).
func (a *Aggregate) WithParams(p []interface{}) Operator {
	a.params = p
	if a.child != nil {
		if w, ok := a.child.(interface{ WithParams([]interface{}) Operator }); ok {
			w.WithParams(p)
		}
	}
	return a
}

func (a *Aggregate) Next(ctx context.Context) (Row, error) {
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

func (a *Aggregate) Close() error {
	a.buf = nil
	a.pos = 0
	return a.child.Close()
}

type groupBucket struct {
	key  []interface{}
	rows []Row
}

func (a *Aggregate) materialize(ctx context.Context) error {
	var groups []groupBucket
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		idx := -1
		for i, g := range groups {
			if keysEqual(g.key, key) {
				idx = i
				break
			}
		}
		if idx < 0 {
			groups = append(groups, groupBucket{key: key, rows: []Row{row}})
		} else {
			groups[idx].rows = append(groups[idx].rows, row)
		}
	}
	// REQ000345: no GROUP BY + empty input = single row with
	// NULL aggregates. With GROUP BY + empty input = 0 rows.
	if len(groups) == 0 && len(a.groupCols) > 0 {
		a.buf = []Row{}
		return nil
	}
	if len(groups) == 0 {
		// scalar aggregate: one group with zero rows
		groups = []groupBucket{{key: nil, rows: nil}}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return keysLess(groups[i].key, groups[j].key)
	})
	for _, g := range groups {
		out := Row{Cols: make([]string, 0, len(a.groupCols)+len(a.aggs))}
		for i, gc := range a.groupCols {
			name := groupColName(gc)
			out.Cols = append(out.Cols, name)
			out.Data = append(out.Data, g.key[i])
		}
		for _, ag := range a.aggs {
			v, err := evalAggregateOver(ag, g.rows, a.params)
			if err != nil {
				return err
			}
			name := aggregateColName(ag)
			out.Cols = append(out.Cols, name)
			out.Data = append(out.Data, v)
		}
		a.buf = append(a.buf, out)
	}
	return nil
}

func evalGroupKey(cols []PS.Expr, row *Row, params []interface{}) ([]interface{}, error) {
	if len(cols) == 0 {
		return nil, nil
	}
	out := make([]interface{}, len(cols))
	for i, c := range cols {
		v, err := Eval(c, row, params)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func keysEqual(a, b []interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalValue(a[i], b[i]) {
			return false
		}
	}
	return true
}

func keysLess(a, b []interface{}) bool {
	for i := range a {
		if i >= len(b) {
			return false
		}
		c := compare(a[i], b[i])
		if c < 0 {
			return true
		}
		if c > 0 {
			return false
		}
	}
	return len(a) < len(b)
}

func groupColName(e PS.Expr) string {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name
	case *PS.AliasedExpr:
		if a, ok := v.Expr.(*PS.Ident); ok {
			return a.Name
		}
		return v.Alias
	}
	return ""
}

func aggregateColName(e PS.Expr) string {
	agg, ok := e.(*PS.AggregateFunc)
	if !ok {
		return ""
	}
	if _, ok := agg.Arg.(*PS.StarExpr); ok {
		return agg.Name + "(*)"
	}
	if ident, ok := agg.Arg.(*PS.Ident); ok {
		return agg.Name + "(" + ident.Name + ")"
	}
	return agg.Name
}

func evalAggregateOver(e PS.Expr, rows []Row, params []interface{}) (interface{}, error) {
	agg, ok := e.(*PS.AggregateFunc)
	if !ok {
		return nil, nil
	}
	switch agg.Name {
	case "COUNT":
		if agg.Distinct {
			seen := make(map[interface{}]bool)
			for _, r := range rows {
				v, err := Eval(agg.Arg, &r, params)
				if err != nil {
					return nil, err
				}
				if v == nil {
					continue
				}
				seen[v] = true
			}
			return int64(len(seen)), nil
		}
		return int64(len(rows)), nil
	case "SUM":
		if agg.Distinct {
			return sumDistinct(agg, rows, params)
		}
		var sumI int64
		var sumF float64
		var seenI, seenF bool
		for _, r := range rows {
			v, err := Eval(agg.Arg, &r, params)
			if err != nil {
				return nil, err
			}
			if v == nil {
				continue
			}
			if f, ok := v.(float64); ok {
				sumF += f
				seenF = true
				continue
			}
			if i, ok := v.(int64); ok {
				sumI += i
				seenI = true
			}
		}
		if seenF {
			return sumF + float64(sumI), nil
		}
		if seenI {
			return sumI, nil
		}
		return nil, nil
	case "AVG":
		if agg.Distinct {
			return avgDistinct(agg, rows, params)
		}
		var sumF float64
		var n int64
		for _, r := range rows {
			v, err := Eval(agg.Arg, &r, params)
			if err != nil {
				return nil, err
			}
			if v == nil {
				continue
			}
			if f, ok := v.(float64); ok {
				sumF += f
				n++
			} else if i, ok := v.(int64); ok {
				sumF += float64(i)
				n++
			}
		}
		if n == 0 {
			return nil, nil
		}
		return sumF / float64(n), nil
	case "MIN":
		if agg.Distinct {
			return minDistinct(agg, rows, params)
		}
		var best interface{}
		for _, r := range rows {
			v, err := Eval(agg.Arg, &r, params)
			if err != nil {
				return nil, err
			}
			if v == nil {
				continue
			}
			if best == nil || compare(v, best) < 0 {
				best = v
			}
		}
		return best, nil
	case "MAX":
		if agg.Distinct {
			return maxDistinct(agg, rows, params)
		}
		var best interface{}
		for _, r := range rows {
			v, err := Eval(agg.Arg, &r, params)
			if err != nil {
				return nil, err
			}
			if v == nil {
				continue
			}
			if best == nil || compare(v, best) > 0 {
				best = v
			}
		}
		return best, nil
	case "GROUP_CONCAT":
		sep := ","
		var parts []string
		seen := make(map[interface{}]bool)
		for _, r := range rows {
			v, err := Eval(agg.Arg, &r, params)
			if err != nil {
				return nil, err
			}
			if v == nil {
				continue
			}
			if agg.Distinct {
				if seen[v] {
					continue
				}
				seen[v] = true
			}
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		if len(parts) == 0 {
			return nil, nil
		}
		out := parts[0]
		for _, p := range parts[1:] {
			out += sep + p
		}
		return out, nil
	}
	return nil, nil
}

// sumDistinct computes SUM(DISTINCT col). NULL values are skipped;
// non-NULL values are deduplicated before summing. REQ000437 (iter-27).
func sumDistinct(agg *PS.AggregateFunc, rows []Row, params []interface{}) (interface{}, error) {
	seen := make(map[interface{}]bool)
	var sumI int64
	var sumF float64
	var seenI, seenF bool
	for _, r := range rows {
		v, err := Eval(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		if f, ok := v.(float64); ok {
			sumF += f
			seenF = true
			continue
		}
		if i, ok := v.(int64); ok {
			sumI += i
			seenI = true
		}
	}
	if seenF {
		return sumF + float64(sumI), nil
	}
	if seenI {
		return sumI, nil
	}
	return nil, nil
}

// avgDistinct computes AVG(DISTINCT col). NULL values are skipped;
// non-NULL values are deduplicated before averaging. REQ000437 (iter-27).
func avgDistinct(agg *PS.AggregateFunc, rows []Row, params []interface{}) (interface{}, error) {
	seen := make(map[interface{}]bool)
	var sumF float64
	var n int64
	for _, r := range rows {
		v, err := Eval(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		if f, ok := v.(float64); ok {
			sumF += f
			n++
		} else if i, ok := v.(int64); ok {
			sumF += float64(i)
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	return sumF / float64(n), nil
}

// minDistinct computes MIN(DISTINCT col). NULL values are skipped;
// the minimum of deduplicated non-NULL values is returned. REQ000437 (iter-27).
func minDistinct(agg *PS.AggregateFunc, rows []Row, params []interface{}) (interface{}, error) {
	seen := make(map[interface{}]bool)
	var best interface{}
	for _, r := range rows {
		v, err := Eval(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		if best == nil || compare(v, best) < 0 {
			best = v
		}
	}
	return best, nil
}

// maxDistinct computes MAX(DISTINCT col). NULL values are skipped;
// the maximum of deduplicated non-NULL values is returned. REQ000437 (iter-27).
func maxDistinct(agg *PS.AggregateFunc, rows []Row, params []interface{}) (interface{}, error) {
	seen := make(map[interface{}]bool)
	var best interface{}
	for _, r := range rows {
		v, err := Eval(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		if best == nil || compare(v, best) > 0 {
			best = v
		}
	}
	return best, nil
}
