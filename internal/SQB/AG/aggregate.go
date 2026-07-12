// Package EX's aggregate.go hosts the Aggregate and HashAggregate
// operators (COUNT/SUM/AVG/MIN/MAX plus grouped variants). These are
// working end-to-end against the executor's in-memory and engine-backed
// paths but are not part of the v1 MVP scope per docs/design/ARCH.md
// ("Out of Scope (v1)" — joins, aggregates, subqueries, DISTINCT).
// They are retained in v1.1 for upcoming releases.
package AG

import (
	"context"
	"slices"
	"strconv"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type Aggregate struct {
	child      Operator
	groupCols  []PS.Expr
	aggs       []PS.Expr
	buf        []Row
	pos        int
	params     []any
	expandStar bool
	execCtx    *PL.ExecContext // REQ001527: exec context for RowArena
}

// SetExecCtx stores the exec context for arena allocation. REQ001527.
func (a *Aggregate) SetExecCtx(ec *PL.ExecContext) { a.execCtx = ec }

// allocData returns a []Value of length n, either from the arena or
// via make() as fallback. REQ001527.
func (a *Aggregate) allocData(n int) []Value {
	if arena := a.arena(); arena != nil {
		return arena.AllocData(n)
	}
	return make([]Value, n)
}

// arena returns the RowArena from the exec context, or nil. REQ001527.
func (a *Aggregate) arena() *DT.RowArena {
	if a.execCtx != nil {
		if ar, ok := a.execCtx.RowArena.(*DT.RowArena); ok {
			return ar
		}
	}
	return nil
}

func NewAggregate(child Operator, groupCols, aggs []PS.Expr) *Aggregate {
	return &Aggregate{child: child, groupCols: groupCols, aggs: aggs}
}

// Child returns the input operator feeding this aggregate.
func (a *Aggregate) Child() Operator { return a.child }

// GroupCols returns the group-by columns.
func (a *Aggregate) GroupCols() []PS.Expr { return a.groupCols }

// Aggs returns the aggregate expressions.
func (a *Aggregate) Aggs() []PS.Expr { return a.aggs }

// SetExpandStar enables full-row output for SELECT * with GROUP BY.
// Non-GROUP BY and non-aggregate columns take their value from the
// first row in each group.
func (a *Aggregate) SetExpandStar() { a.expandStar = true }

// WithParams propagates the bound `?` placeholders to this
// operator and its child (R16-1..2).
func (a *Aggregate) WithParams(p []any) Operator {
	a.params = p
	if a.child != nil {
		if w, ok := a.child.(interface{ WithParams([]any) Operator }); ok {
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
	key  []Value
	rows []Row
}

func (a *Aggregate) materialize(ctx context.Context) error {
	var groups []groupBucket
	groupIndex := make(map[string]int)
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
		key, err := evalGroupKey(a.groupCols, &row, a.params, a.arena())
		if err != nil {
			return err
		}
		ks := groupKeyString(key)
		if idx, ok := groupIndex[ks]; ok {
			groups[idx].rows = append(groups[idx].rows, row)
		} else {
			groupIndex[ks] = len(groups)
			groups = append(groups, groupBucket{key: key, rows: []Row{row}})
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
	slices.SortStableFunc(groups, func(a, b groupBucket) int {
		return keysLessCmpValue(a.key, b.key)
	})
	for _, g := range groups {
		var out Row
		var off int
		if a.expandStar && len(g.rows) > 0 {
			firstRow := g.rows[0]
			nData := len(firstRow.Data) + len(a.aggs)
			out = Row{
				Cols: make([]string, len(firstRow.Cols), len(firstRow.Cols)+len(a.aggs)),
				Data: a.allocData(nData),
			}
			copy(out.Cols, firstRow.Cols)
			if nData > 0 {
				copy(out.Data, firstRow.Data)
			}
			for i, gc := range a.groupCols {
				name := groupColName(gc)
				for j, c := range out.Cols {
					if c == name {
						out.Data[j] = g.key[i]
						break
					}
				}
			}
			off = len(firstRow.Data)
		} else {
			nData := len(a.groupCols) + len(a.aggs)
			out = Row{
				Cols: make([]string, 0, nData),
				Data: a.allocData(nData),
			}
			for i, gc := range a.groupCols {
				out.Cols = append(out.Cols, groupColName(gc))
				out.Data[i] = g.key[i]
			}
			off = len(a.groupCols)
		}
		for _, ag := range a.aggs {
			v, err := EvalAggregateOver(ag, g.rows, a.params)
			if err != nil {
				return err
			}
			name := aggregateColName(ag)
			out.Cols = append(out.Cols, name)
			out.Data[off] = DT.ValueFromAny(v)
			off++
		}
		a.buf = append(a.buf, out)
	}
	return nil
}

func evalGroupKey(cols []PS.Expr, row *Row, params []any, arena *DT.RowArena) ([]Value, error) {
	if len(cols) == 0 {
		return nil, nil
	}
	var out []Value
	if arena != nil {
		out = arena.AllocData(len(cols))
	} else {
		out = make([]Value, len(cols))
	}
	for i, c := range cols {
		v, err := EV.EvalValue(c, row, params)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func keysEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == nil && b[i] == nil {
			continue
		}
		if a[i] == nil || b[i] == nil {
			return false
		}
		switch x := a[i].(type) {
		case int64:
			if y, ok := b[i].(int64); ok && x == y {
				continue
			}
		case float64:
			if y, ok := b[i].(float64); ok && x == y {
				continue
			}
		case string:
			if y, ok := b[i].(string); ok && x == y {
				continue
			}
		case bool:
			if y, ok := b[i].(bool); ok && x == y {
				continue
			}
		default:
			if a[i] == b[i] {
				continue
			}
		}
		return false
	}
	return true
}

// REQ000765: groupKeyString serializes a group key []any to a
// deterministic string for O(1) hash lookup, avoiding O(N) linear
// scan over all groups.
func groupKeyString(key []Value) string {
	if len(key) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range key {
		if i > 0 {
			b.WriteByte('\x00')
		}
		switch v.Kind {
		case KindNull:
			b.WriteString("\\N")
		case KindInt:
			b.WriteString("I:")
			b.Write(strconv.AppendInt(nil, v.I64, 10))
		case KindFloat:
			b.WriteString("F:")
			_ = b
			b.Write(strconv.AppendFloat(nil, v.F64, 'g', -1, 64))
		case KindText:
			b.WriteString("S:")
			b.WriteString(v.S)
		case KindBool:
			if v.Bo {
				b.WriteString("B:true")
			} else {
				b.WriteString("B:false")
			}
		case KindBlob:
			b.WriteString("X:")
			b.Write(v.B)
		default:
			b.WriteString("?:")
			b.WriteString(DT.ValueToString(v))
		}
	}
	return b.String()
}

func keysLess(a, b []any) bool {
	return keysLessCmp(a, b) < 0
}

func keysLessCmp(a, b []any) int {
	for i := range a {
		if i >= len(b) {
			return 0
		}
		c := DT.Compare(a[i], b[i])
		if c != 0 {
			return c
		}
	}
	if len(a) < len(b) {
		return -1
	}
	return 0
}

func keysLessCmpValue(a, b []Value) int {
	for i := range a {
		if i >= len(b) {
			return 0
		}
		c := PL.CompareValue(a[i], b[i])
		if c != 0 {
			return c
		}
	}
	if len(a) < len(b) {
		return -1
	}
	return 0
}

func groupColName(e PS.Expr) string {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name
	case *PS.QualifiedName:
		// REQ000720: render as "table.col" so output columns
		// match the qualified name in the GROUP BY clause.
		return v.Table + "." + v.Name
	case *PS.AliasedExpr:
		if a, ok := v.Expr.(*PS.Ident); ok {
			return a.Name
		}
		if q, ok := v.Expr.(*PS.QualifiedName); ok {
			return q.Table + "." + q.Name
		}
		return v.Alias
	}
	return ""
}

func aggregateColName(e PS.Expr) string {
	// If wrapped in AliasedExpr, use the alias
	if ae, ok := e.(*PS.AliasedExpr); ok {
		if ae.Alias != "" {
			return ae.Alias
		}
		return aggregateColName(ae.Expr)
	}
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

// buildAggregateVirtualRow walks e for embedded AggregateFunc
// nodes, evaluates each against rows, and returns a virtual Row
// whose Cols/Data hold the aggregate results. This lets Eval
// resolve AggregateFunc lookups via the normal row mechanism.
func buildAggregateVirtualRow(e PS.Expr, rows []Row, params []any) (Row, error) {
	var vrow Row
	var collect func(expr PS.Expr) error
	collect = func(expr PS.Expr) error {
		if expr == nil {
			return nil
		}
		switch v := expr.(type) {
		case *PS.AggregateFunc:
			name := aggregateColName(v)
			if name == "" {
				return nil
			}
			// Check if already collected
			for _, c := range vrow.Cols {
				if c == name {
					return nil
				}
			}
			val, err := EvalAggregateOver(v, rows, params)
			if err != nil {
				return err
			}
			vrow.Cols = append(vrow.Cols, name)
			vrow.Data = append(vrow.Data, DT.ValueFromAny(val))
		case *PS.UnaryExpr:
			return collect(v.Operand)
		case *PS.BinaryExpr:
			if err := collect(v.Left); err != nil {
				return err
			}
			return collect(v.Right)
		case *PS.AliasedExpr:
			return collect(v.Expr)
		case *PS.CastExpr:
			return collect(v.Expr)
		case *PS.FunctionCall:
			// REQ000832: scalar functions wrapping aggregates (e.g.
			// COALESCE(NULL, MIN(x) * 10)) must recurse into their
			// arguments so the inner aggregate is collected into the
			// virtual row. Without this, COALESCE can't find the
			// aggregate value and returns NULL.
			for _, a := range v.Args {
				if err := collect(a); err != nil {
					return err
				}
			}
		case *PS.BetweenExpr:
			if err := collect(v.Expr); err != nil {
				return err
			}
			if err := collect(v.Low); err != nil {
				return err
			}
			return collect(v.High)
		case *PS.InExpr:
			if err := collect(v.Expr); err != nil {
				return err
			}
			for _, a := range v.List {
				if err := collect(a); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(e); err != nil {
		return vrow, err
	}
	return vrow, nil
}

func EvalAggregateOver(e PS.Expr, rows []Row, params []any) (any, error) {
	// Unwrap AliasedExpr to get the inner aggregate
	if ae, ok := e.(*PS.AliasedExpr); ok {
		return EvalAggregateOver(ae.Expr, rows, params)
	}
	// REQ000700+: handle expressions wrapping aggregates
	// (e.g. -COUNT(*), SUM(x)+1, CAST(SUM(x) AS TEXT)).
	// Evaluate the expression against a virtual row that
	// contains the aggregate results.
	agg, ok := e.(*PS.AggregateFunc)
	if !ok {
		// Not a bare aggregate — may be an expression wrapping
		// one or more aggregates. Build a virtual row with the
		// aggregate values and evaluate the full expression.
		if DT.ContainsAggregate(e) {
			vrow, err := buildAggregateVirtualRow(e, rows, params)
			if err != nil {
				return nil, err
			}
			v, err := EV.EvalValue(e, &vrow, params)
			if err != nil {
				return nil, err
			}
			return v.ToAny(), nil
		}
		// REQ001194: expression does not contain any aggregate
		// (e.g. pure constant or column reference in aggregate
		// list). Evaluate once against the first input row (or
		// nil for scalar aggregates with no input rows).
		var evalRow *Row
		if len(rows) > 0 {
			evalRow = &rows[0]
		}
		v, err := EV.EvalValue(e, evalRow, params)
		if err != nil {
			return nil, err
		}
		return v.ToAny(), nil
	}
	// REQ000978: registry-based aggregate dispatch. Adding a new
	// aggregate is a one-line registration in
	// aggregate_registry.go's init(), not an edit to a switch
	// block here.
	if impl, ok := AggregateFuncRegistry[agg.Name]; ok {
		return impl(agg, rows, params)
	}
	return nil, nil
}

// sumDistinct computes SUM(DISTINCT col). NULL values are skipped;
// non-NULL values are deduplicated before summing. REQ000437 (iter-27).
func sumDistinct(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	seen := make(map[any]bool)
	var sumI int64
	var sumF float64
	var seenI, seenF bool
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if seen[v.ToAny()] {
			continue
		}
		seen[v.ToAny()] = true
		if v.Kind == KindFloat {
			sumF += v.F64
			seenF = true
			continue
		}
		if v.Kind == KindInt {
			sumI += v.I64
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
func avgDistinct(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	seen := make(map[any]bool)
	var sumF float64
	var n int64
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if seen[v.ToAny()] {
			continue
		}
		seen[v.ToAny()] = true
		if v.Kind == KindFloat {
			sumF += v.F64
			n++
		} else if v.Kind == KindInt {
			sumF += float64(v.I64)
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
func minDistinct(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	seen := make(map[any]bool)
	var best Value
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if seen[v.ToAny()] {
			continue
		}
		seen[v.ToAny()] = true
		if best.Kind == KindNull || PL.CompareValue(v, best) < 0 {
			best = v
		}
	}
	return best.ToAny(), nil
}

// maxDistinct computes MAX(DISTINCT col). NULL values are skipped;
// the maximum of deduplicated non-NULL values is returned. REQ000437 (iter-27).
func maxDistinct(agg *PS.AggregateFunc, rows []Row, params []any) (any, error) {
	seen := make(map[any]bool)
	var best Value
	for _, r := range rows {
		v, err := EV.EvalValue(agg.Arg, &r, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		if seen[v.ToAny()] {
			continue
		}
		seen[v.ToAny()] = true
		if best.Kind == KindNull || PL.CompareValue(v, best) > 0 {
			best = v
		}
	}
	return best.ToAny(), nil
}
