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
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type Aggregate struct {
	child     Operator
	groupCols []PS.Expr
	aggs      []PS.Expr
	// REQ001711: constant non-aggregate SELECT columns that must be
	// emitted alongside the aggregates (e.g. `SELECT 62, COUNT(*) FROM t`
	// — 62 is a constant projection that does not change the group
	// partition but must appear in every output row).
	constCols []PS.Expr
	// REQ001710: full SELECT list in original order. When set, the
	// materialize function emits columns in this order instead of
	// constCols-then-aggs. This preserves the SELECT list column
	// ordering when aliases conflict with table column names.
	fullCols   []PS.Expr
	buf        []Row
	pos        int
	params     []any
	expandStar bool
	// REQ001636: scalar aggregate (no GROUP BY) uses fast path.
	scalar bool
	// REQ001697: reusable buffer for scalar aggregate materialization.
	scalarRowBuf []Row
	// REQ001967: when true, aggregate output columns are named by
	// AggregateLookupKey instead of by alias. This allows HAVING and
	// downstream Project operators to find aggregates by their
	// canonical lookup key.
	aggColsByLookupKey bool
	// REQ001967: HAVING expression. When set, groups are filtered by
	// this expression before the final output is produced. The HAVING
	// expression is evaluated against a row containing group keys and
	// all aggregates (named by lookup key).
	having PS.Expr
	// REQ002027: flat buffer for group key values to avoid per-group
	// make([]Value, len(cols)) allocations. Each group key is a
	// sub-slice carved from this buffer.
	groupKeyBuf []Value
	// REQ002021: reusable flat Value buffer for batch.ToRowsShared
	// to avoid per-row make([]Value, N) in scalar and GROUP BY paths.
	toRowsBuf []Value
}

func NewAggregate(child Operator, groupCols, aggs []PS.Expr) *Aggregate {
	return &Aggregate{child: child, groupCols: groupCols, aggs: aggs, scalar: len(groupCols) == 0}
}

// SetConstCols attaches constant projections that must appear in
// every output row without affecting the group partition. REQ001711.
func (a *Aggregate) SetConstCols(cols []PS.Expr) { a.constCols = cols }

// SetFullCols attaches the full SELECT list in original order.
// When set, the materialize function emits columns in this order
// instead of constCols-then-aggs. REQ001710.
func (a *Aggregate) SetFullCols(cols []PS.Expr) { a.fullCols = cols }

// SetAggColsByLookupKey controls whether aggregate output columns are
// named by AggregateLookupKey (true) or by alias/evalColName (false).
// When true, HAVING and downstream Project operators can find
// aggregates by their canonical lookup key. REQ001967.
func (a *Aggregate) SetAggColsByLookupKey(v bool) { a.aggColsByLookupKey = v }

// SetHaving attaches a HAVING expression that filters groups before
// the final output is emitted. REQ001967.
func (a *Aggregate) SetHaving(e PS.Expr) { a.having = e }

// ConstCols returns the constant projections attached via SetConstCols.
func (a *Aggregate) ConstCols() []PS.Expr { return a.constCols }

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
	a.scalarRowBuf = nil
	return a.child.Close()
}

type groupBucket struct {
	key  []Value
	rows []Row
}

// scalarAggAccum holds streaming accumulator state for one aggregate
// function during scalar aggregate materialization. Avoids allocating
// []Row via batch.ToRows by accumulating directly from batch columns.
// REQ002083.
type scalarAggAccum struct {
	aggKind string // "COUNT", "SUM", "AVG", "MIN", "MAX"
	colIdx  int    // column index in batch, -1 for StarExpr (COUNT(*))
	isStar  bool   // true for COUNT(*)

	// Running state
	count    int64   // total rows (COUNT(*)) or non-null rows (COUNT/SUM/AVG)
	sumI     int64   // integer sum (SUM/AVG)
	sumF     float64 // float sum (SUM/AVG)
	seenI    bool    // have seen any int value
	seenF    bool    // have seen any float value
	hasValue bool    // have seen any non-null value (MIN/MAX)
	best     Value   // current MIN or MAX
}

// computeResult returns the final aggregate result from the accumulator.
// REQ002083.
func (acc *scalarAggAccum) computeResult() any {
	switch acc.aggKind {
	case "COUNT":
		return acc.count
	case "SUM":
		if acc.seenF {
			return acc.sumF + float64(acc.sumI)
		}
		if acc.seenI {
			return acc.sumI
		}
		return nil
	case "AVG":
		if acc.count == 0 {
			return nil
		}
		return (acc.sumF + float64(acc.sumI)) / float64(acc.count)
	case "MIN", "MAX":
		if !acc.hasValue {
			return nil
		}
		return acc.best.ToAny()
	}
	return nil
}

// canStreamScalarAgg checks if an AggregateFunc can be accumulated
// directly from batch columns without ToRows(). Returns the aggregate
// kind and true if streamable, or ("", false) otherwise. REQ002083.
func canStreamScalarAgg(af *PS.AggregateFunc) (string, bool) {
	if af.Distinct {
		return "", false // DISTINCT needs dedup, fall back
	}
	switch af.Name {
	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		switch af.Arg.(type) {
		case *PS.StarExpr:
			if af.Name != "COUNT" {
				return "", false // SUM(*) etc. are invalid
			}
		case *PS.Ident, *PS.QualifiedName:
			// Simple column reference — streamable
		default:
			return "", false // Complex expression — fall back
		}
		return af.Name, true
	default:
		return "", false // GROUP_CONCAT, STRING_AGG, unknown — fall back
	}
}

// resolveAccumColIdx resolves an aggregate argument expression to a
// column index in the batch. Returns -1 if the column cannot be found.
// REQ002083.
func resolveAccumColIdx(arg PS.Expr, colMap map[string]int) int {
	switch a := arg.(type) {
	case *PS.Ident:
		if idx, ok := colMap[a.Name]; ok {
			return idx
		}
	case *PS.QualifiedName:
		// Try "table.col" first, then bare "col"
		qualified := a.Table + "." + a.Name
		if idx, ok := colMap[qualified]; ok {
			return idx
		}
		if idx, ok := colMap[a.Name]; ok {
			return idx
		}
	}
	return -1
}

// collectScalarAggFuncs walks an expression tree and collects all
// unique AggregateFunc nodes, deduplicating by AggregateLookupKey.
// Returns nil if any aggregate is not streamable. REQ002083.
func collectScalarAggFuncs(expr PS.Expr, seen map[string]bool, out *[]*PS.AggregateFunc) bool {
	if expr == nil {
		return true
	}
	switch v := expr.(type) {
	case *PS.AggregateFunc:
		key := DT.AggregateLookupKey(v)
		if !seen[key] {
			seen[key] = true
			if _, ok := canStreamScalarAgg(v); !ok {
				return false
			}
			*out = append(*out, v)
		}
	case *PS.UnaryExpr:
		return collectScalarAggFuncs(v.Operand, seen, out)
	case *PS.BinaryExpr:
		if !collectScalarAggFuncs(v.Left, seen, out) {
			return false
		}
		return collectScalarAggFuncs(v.Right, seen, out)
	case *PS.AliasedExpr:
		return collectScalarAggFuncs(v.Expr, seen, out)
	case *PS.CastExpr:
		return collectScalarAggFuncs(v.Expr, seen, out)
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			if !collectScalarAggFuncs(arg, seen, out) {
				return false
			}
		}
	case *PS.CaseExpr:
		if !collectScalarAggFuncs(v.Expr, seen, out) {
			return false
		}
		for _, w := range v.WhenList {
			if !collectScalarAggFuncs(w.Cond, seen, out) {
				return false
			}
			if !collectScalarAggFuncs(w.Then, seen, out) {
				return false
			}
		}
		return collectScalarAggFuncs(v.Else, seen, out)
	case *PS.BetweenExpr:
		if !collectScalarAggFuncs(v.Expr, seen, out) {
			return false
		}
		if !collectScalarAggFuncs(v.Low, seen, out) {
			return false
		}
		return collectScalarAggFuncs(v.High, seen, out)
	case *PS.InExpr:
		if !collectScalarAggFuncs(v.Expr, seen, out) {
			return false
		}
		for _, item := range v.List {
			if !collectScalarAggFuncs(item, seen, out) {
				return false
			}
		}
	}
	return true
}

// tryStreamingScalarAggregate attempts to accumulate scalar aggregate
// state directly from batch columns, avoiding the batch.ToRows()
// allocation. Returns (true, outputRow, nil) on success, or
// (false, Row{}, nil) if streaming is not possible (caller should
// fall back to the ToRows path). Returns (false, Row{}, err) on
// error. REQ002083.
func (a *Aggregate) tryStreamingScalarAggregate(ctx context.Context) (bool, Row, error) {
	// Check if child supports batch mode
	var bp UT.BatchProducer
	if b, ok := a.child.(UT.BatchProducer); ok {
		useBatch := true
		if checker, ok2 := b.(UT.BatchSupportChecker); ok2 {
			useBatch = checker.BatchSupported()
		}
		if !useBatch {
			return false, Row{}, nil
		}
		bp = b
	} else {
		return false, Row{}, nil
	}

	// Determine emit order
	emitOrder := a.fullCols
	if emitOrder == nil {
		emitOrder = append(append([]PS.Expr(nil), a.constCols...), a.aggs...)
	}

	// Collect unique streamable AggregateFunc nodes from emit expressions.
	// If any aggregate is not streamable (complex arg, DISTINCT, etc.),
	// fall back to the ToRows path.
	seenKeys := make(map[string]bool)
	var aggFuncs []*PS.AggregateFunc
	for _, e := range emitOrder {
		if DT.ContainsAggregate(e) {
			if !collectScalarAggFuncs(e, seenKeys, &aggFuncs) {
				return false, Row{}, nil
			}
		}
	}

	// Build accumulators for each unique aggregate function.
	type accumEntry struct {
		af   *PS.AggregateFunc
		key  string
		kind string
		acc  scalarAggAccum
	}
	entries := make([]accumEntry, len(aggFuncs))
	for i, af := range aggFuncs {
		kind, _ := canStreamScalarAgg(af)
		key := DT.AggregateLookupKey(af)
		isStar := false
		if _, ok := af.Arg.(*PS.StarExpr); ok {
			isStar = true
		}
		entries[i] = accumEntry{
			af:   af,
			key:  key,
			kind: kind,
			acc: scalarAggAccum{
				aggKind: kind,
				colIdx:  -1,
				isStar:  isStar,
			},
		}
	}

	// Drain batches and accumulate.
	colMap := make(map[string]int)
	resolved := false

	for {
		if err := ctx.Err(); err != nil {
			return false, Row{}, err
		}
		batch, err := bp.NextBatch(ctx)
		if err != nil {
			return false, Row{}, err
		}
		if batch == nil {
			break
		}

		// Resolve column indices from the first batch.
		if !resolved {
			names := batch.ColNames()
			for i, name := range names {
				colMap[name] = i
			}
			for i := range entries {
				if entries[i].acc.isStar {
					continue
				}
				idx := resolveAccumColIdx(entries[i].af.Arg, colMap)
				if idx < 0 {
					// Can't resolve column — fall back to ToRows path.
					// Put the batch back by NOT draining from the child;
					// instead, seed scalarRowBuf with rows from this
					// batch and remaining batches. The caller's fallback
					// path will then use the pre-populated scalarRowBuf
					// instead of re-draining from the child.
					var toRows []PL.Row
					toRows, a.toRowsBuf = batch.ToRowsShared(a.toRowsBuf)
					a.scalarRowBuf = append(a.scalarRowBuf[:0], toRows...)
					if batch.Pooled {
						batch.Put()
					}
					// Continue draining remaining batches into
					// scalarRowBuf so the fallback path has all data.
					for {
						if err := ctx.Err(); err != nil {
							return false, Row{}, err
						}
						batch2, err := bp.NextBatch(ctx)
						if err != nil {
							return false, Row{}, err
						}
						if batch2 == nil {
							break
						}
						rows2 := batch2.ToRows()
						a.scalarRowBuf = append(a.scalarRowBuf, rows2...)
						if batch2.Pooled {
							batch2.Put()
						}
					}
					return false, Row{}, nil
				}
				entries[i].acc.colIdx = idx
			}
			resolved = true
		}

		// Accumulate from batch columns.
		n := batch.LogicalSize()
		for row := 0; row < n; row++ {
			phys := row
			if batch.Sel != nil {
				phys = int(batch.Sel[row])
			}
			for j := range entries {
				acc := &entries[j].acc
				if acc.isStar {
					acc.count++
					continue
				}
				if acc.colIdx < 0 || acc.colIdx >= len(batch.Cols) {
					continue
				}
				v := UT.ToValue(batch.Cols[acc.colIdx], phys)
				if v.Kind == KindNull {
					continue
				}
				switch acc.aggKind {
				case "COUNT":
					acc.count++
				case "SUM", "AVG":
					acc.count++
					if v.Kind == KindInt {
						acc.sumI += v.I64
						acc.seenI = true
					} else if v.Kind == KindFloat {
						acc.sumF += v.F64
						acc.seenF = true
					}
				case "MIN":
					if !acc.hasValue || PL.CompareValue(v, acc.best) < 0 {
						acc.best = v
						acc.hasValue = true
					}
				case "MAX":
					if !acc.hasValue || PL.CompareValue(v, acc.best) > 0 {
						acc.best = v
						acc.hasValue = true
					}
				}
			}
		}

		if batch.Pooled {
			batch.Put()
		}
	}

	// Build virtual row from accumulated results. The virtual row
	// is used by EvalValue to resolve AggregateFunc nodes in
	// expressions like -SUM(x).
	vrow := Row{
		Cols: make([]string, 0, len(entries)),
		Data: make([]Value, 0, len(entries)),
	}
	for i := range entries {
		name := aggregateColName(entries[i].af)
		if name == "" {
			continue
		}
		// Deduplicate (shouldn't happen since we deduped by key).
		dup := false
		for _, c := range vrow.Cols {
			if c == name {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		val := entries[i].acc.computeResult()
		vrow.Cols = append(vrow.Cols, name)
		vrow.Data = append(vrow.Data, DT.ValueFromAny(val))
	}

	// Build output row by evaluating each emit expression.
	total := len(a.fullCols)
	if total == 0 {
		total = len(a.constCols) + len(a.aggs)
	}
	out := Row{Cols: make([]string, 0, total), Data: make([]Value, 0, total)}
	for _, e := range emitOrder {
		var v Value
		if DT.ContainsAggregate(e) {
			// Evaluate against the virtual row containing accumulated
			// aggregate results. This handles wrappers like -SUM(x),
			// CAST(SUM(x) AS TEXT), etc.
			val, err := EV.EvalValue(e, &vrow, a.params)
			if err != nil {
				return true, Row{}, err
			}
			v = val
		} else {
			var err error
			v, err = EV.EvalValue(e, &Row{}, a.params)
			if err != nil {
				return true, Row{}, err
			}
		}
		name := evalColName(e)
		out.Cols = append(out.Cols, name)
		out.Data = append(out.Data, v)
	}

	return true, out, nil
}

func (a *Aggregate) materialize(ctx context.Context) error {
	// REQ001636: scalar aggregate fast path — no GROUP BY.
	// Skip group key computation, groupIndex map, and sort.
	// Accumulate aggregate state directly from input rows.
	if a.scalar {
		// REQ002083: try streaming scalar aggregate accumulation
		// when all aggregates have simple column arguments. This
		// avoids the batch.ToRows() allocation that would otherwise
		// materialize every row in the input.
		if ok, out, err := a.tryStreamingScalarAggregate(ctx); ok {
			if err != nil {
				return err
			}
			a.buf = []Row{out}
			return nil
		}
		// Fallback: existing ToRows/row-based path for complex
		// aggregate arguments, DISTINCT, or non-batch children.
		// When tryStreamingScalarAggregate returns false, it may
		// have already drained all batches into scalarRowBuf
		// (column resolution failure mid-stream). In that case,
		// skip the drain loop. Otherwise, clear and drain fresh.
		if len(a.scalarRowBuf) == 0 {
			a.scalarRowBuf = a.scalarRowBuf[:0]
		}
		// REQ001989: if child implements BatchProducer and batch
		// mode is supported, drain via NextBatch + ToRows.
		useBatch := false
		var bp UT.BatchProducer
		if b, ok := a.child.(UT.BatchProducer); ok {
			useBatch = true
			if checker, ok2 := b.(UT.BatchSupportChecker); ok2 {
				useBatch = checker.BatchSupported()
			}
			if useBatch {
				bp = b
			}
		}
		if useBatch {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				batch, err := bp.NextBatch(ctx)
				if err != nil {
					return err
				}
				if batch == nil {
					break
				}
				var toRows []PL.Row
				toRows, a.toRowsBuf = batch.ToRowsShared(a.toRowsBuf)
				a.scalarRowBuf = append(a.scalarRowBuf, toRows...)
				if batch.Pooled {
					batch.Put()
				}
			}
		} else {
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
				a.scalarRowBuf = append(a.scalarRowBuf, row)
			}
		}
		if len(a.scalarRowBuf) == 0 {
			a.scalarRowBuf = []Row{} // ensure non-nil for EvalAggregateOver
		}
		// Build output row directly from accumulated state.
		total := len(a.fullCols)
		if total == 0 {
			total = len(a.constCols) + len(a.aggs)
		}
		out := Row{Cols: make([]string, 0, total), Data: make([]Value, 0, total)}
		// REQ001710: when fullCols is set, evaluate all items in
		// SELECT list order to preserve column ordering even when
		// aliases conflict with table column names.
		emitOrder := a.fullCols
		if emitOrder == nil {
			// Fallback: constCols first, then aggs (legacy order).
			emitOrder = append(append([]PS.Expr(nil), a.constCols...), a.aggs...)
		}
		for _, e := range emitOrder {
			// Aggregate expressions must use EvalAggregateOver for
			// proper aggregate evaluation; non-aggregate expressions
			// use EvalValue since they have no input rows to aggregate.
			var v Value
			if DT.ContainsAggregate(e) {
				val, err := EvalAggregateOver(e, a.scalarRowBuf, a.params)
				if err != nil {
					return err
				}
				v = DT.ValueFromAny(val)
			} else {
				var err error
				v, err = EV.EvalValue(e, &Row{}, a.params)
				if err != nil {
					return err
				}
			}
			name := evalColName(e)
			out.Cols = append(out.Cols, name)
			out.Data = append(out.Data, v)
		}
		a.buf = []Row{out}
		return nil
	}

	var groups []groupBucket
	groupIndex := make(map[string]int)
	// REQ001989: if child implements BatchProducer and batch
	// mode is supported, drain via NextBatch + ToRows.
	useBatch := false
	var bp UT.BatchProducer
	if b, ok := a.child.(UT.BatchProducer); ok {
		useBatch = true
		if checker, ok2 := b.(UT.BatchSupportChecker); ok2 {
			useBatch = checker.BatchSupported()
		}
		if useBatch {
			bp = b
		}
	}
	if useBatch {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			batch, err := bp.NextBatch(ctx)
			if err != nil {
				return err
			}
			if batch == nil {
				break
			}
			var toRows []PL.Row
			toRows, a.toRowsBuf = batch.ToRowsShared(a.toRowsBuf)
			for i := range toRows {
				key, err := evalGroupKey(&a.groupKeyBuf, a.groupCols, &toRows[i], a.params)
				if err != nil {
					if batch.Pooled {
						batch.Put()
					}
					return err
				}
				ks := groupKeyString(key)
				if idx, ok := groupIndex[ks]; ok {
					groups[idx].rows = append(groups[idx].rows, toRows[i])
				} else {
					groupIndex[ks] = len(groups)
					groups = append(groups, groupBucket{key: key, rows: []Row{toRows[i]}})
				}
			}
			if batch.Pooled {
				batch.Put()
			}
		}
	} else {
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
			key, err := evalGroupKey(&a.groupKeyBuf, a.groupCols, &row, a.params)
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
		// REQ001967: build a HAVING row with group keys and all aggregates
		// (named by lookup key). This is used for HAVING evaluation.
		// Group keys are added with both qualified and unqualified names
		// so HAVING can reference them either way (standard SQL behavior).
		var havingRow Row
		if a.having != nil {
			havingRow = Row{
				Cols: make([]string, 0, len(a.groupCols)*2+len(a.aggs)),
				Data: make([]Value, 0, len(a.groupCols)*2+len(a.aggs)),
			}
			for i, gc := range a.groupCols {
				name := groupColName(gc)
				havingRow.Cols = append(havingRow.Cols, name)
				havingRow.Data = append(havingRow.Data, g.key[i])
				// Add unqualified name alias for QualifiedName group keys
				// so HAVING can use unqualified references.
				if qn, ok := gc.(*PS.QualifiedName); ok {
					havingRow.Cols = append(havingRow.Cols, qn.Name)
					havingRow.Data = append(havingRow.Data, g.key[i])
				}
			}
			for _, agg := range a.aggs {
				val, err := EvalAggregateOver(agg, g.rows, a.params)
				if err != nil {
					return err
				}
				havingRow.Cols = append(havingRow.Cols, aggregateLookupKey(agg))
				havingRow.Data = append(havingRow.Data, DT.ValueFromAny(val))
			}
			// Evaluate HAVING
			hResult, err := EV.EvalValue(a.having, &havingRow, a.params)
			if err != nil {
				return err
			}
			if !DT.IsValueTruthy(hResult) {
				continue
			}
		}
		var out Row
		if a.expandStar && len(g.rows) > 0 {
			firstRow := g.rows[0]
			extraLen := len(a.fullCols)
			if extraLen == 0 {
				extraLen = len(a.constCols) + len(a.aggs)
			}
			out = Row{
				Cols: make([]string, len(firstRow.Cols), len(firstRow.Cols)+extraLen),
				Data: make([]Value, len(firstRow.Data), len(firstRow.Data)+extraLen),
			}
			copy(out.Cols, firstRow.Cols)
			copy(out.Data, firstRow.Data)
			for i, gc := range a.groupCols {
				name := groupColName(gc)
				for j, c := range out.Cols {
					if c == name {
						out.Data[j] = g.key[i]
						break
					}
				}
			}
		} else if a.fullCols != nil && len(a.groupCols) > 0 {
			// REQ001967: GROUP BY with fullCols — emit in SELECT list order.
			// Non-aggregate expressions are evaluated against the first
			// row of the group (group key columns are constant per group).
			out = Row{
				Cols: make([]string, 0, len(a.fullCols)),
				Data: make([]Value, 0, len(a.fullCols)),
			}
			var evalRow *Row
			if len(g.rows) > 0 {
				evalRow = &g.rows[0]
			} else {
				evalRow = &Row{}
			}
			for _, e := range a.fullCols {
				var v Value
				if DT.ContainsAggregate(e) {
					val, err := EvalAggregateOver(e, g.rows, a.params)
					if err != nil {
						return err
					}
					v = DT.ValueFromAny(val)
				} else {
					var err error
					v, err = EV.EvalValue(e, evalRow, a.params)
					if err != nil {
						return err
					}
				}
				out.Cols = append(out.Cols, evalColName(e))
				out.Data = append(out.Data, v)
			}
		} else {
			out = Row{Cols: make([]string, 0, len(a.groupCols)+len(a.fullCols)), Data: make([]Value, 0, len(a.groupCols)+len(a.fullCols))}
			for i, gc := range a.groupCols {
				out.Cols = append(out.Cols, groupColName(gc))
				out.Data = append(out.Data, g.key[i])
			}
		}
		// REQ001710: evaluate non-group-by items in SELECT list order.
		// Skip if we already emitted fullCols for GROUP BY above.
		if !(a.fullCols != nil && len(a.groupCols) > 0 && !a.expandStar) {
			emitOrder := a.fullCols
			if emitOrder == nil {
				emitOrder = append(append([]PS.Expr(nil), a.constCols...), a.aggs...)
			}
			for _, e := range emitOrder {
				var v Value
				if DT.ContainsAggregate(e) {
					val, err := EvalAggregateOver(e, g.rows, a.params)
					if err != nil {
						return err
					}
					v = DT.ValueFromAny(val)
				} else {
					var err error
					v, err = EV.EvalValue(e, &Row{}, a.params)
					if err != nil {
						return err
					}
				}
				var name string
				if a.aggColsByLookupKey && DT.ContainsAggregate(e) {
					name = aggregateLookupKey(e)
				} else {
					name = evalColName(e)
				}
				out.Cols = append(out.Cols, name)
				out.Data = append(out.Data, v)
			}
		}
		a.buf = append(a.buf, out)
	}
	return nil
}

func evalGroupKey(flatBuf *[]Value, cols []PS.Expr, row *Row, params []any) ([]Value, error) {
	if len(cols) == 0 {
		return nil, nil
	}
	n := len(cols)
	// REQ002027: bump-allocate from flat buffer. Each key is a
	// sub-slice backed by the shared flat buffer, so we avoid
	// per-key make([]Value, n) while keeping keys independent.
	var off int
	if cap(*flatBuf)-len(*flatBuf) < n {
		*flatBuf = make([]Value, max(n*64, 128))
		off = 0
	} else {
		off = len(*flatBuf)
	}
	*flatBuf = (*flatBuf)[:off+n]
	out := (*flatBuf)[off : off+n : off+n]
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
	return DT.AggregateLookupKey(agg)
}

// evalColName returns the column name for an expression, using the
// same logic as NewProject. REQ001710.
func evalColName(e PS.Expr) string {
	if ae, ok := e.(*PS.AliasedExpr); ok && ae.Alias != "" {
		return ae.Alias
	}
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name
	case *PS.QualifiedName:
		return v.Table + "." + v.Name
	case *PS.AliasedExpr:
		if inner, ok := v.Expr.(*PS.Ident); ok {
			return inner.Name
		}
		return v.Alias
	case *PS.UnaryExpr:
		if inner, ok := v.Operand.(*PS.Ident); ok {
			return inner.Name
		}
	case *PS.AggregateFunc:
		return DT.AggregateLookupKey(v)
	}
	return ""
}

// aggregateLookupKey returns the canonical AggregateLookupKey for the
// first aggregate function found in e. Used when aggColsByLookupKey is
// true to name output columns by their canonical key, enabling HAVING
// and downstream Project operators to find them. REQ001967.
func aggregateLookupKey(e PS.Expr) string {
	switch v := e.(type) {
	case *PS.AggregateFunc:
		return DT.AggregateLookupKey(v)
	case *PS.AliasedExpr:
		return aggregateLookupKey(v.Expr)
	case *PS.UnaryExpr:
		return aggregateLookupKey(v.Operand)
	case *PS.CastExpr:
		return aggregateLookupKey(v.Expr)
	}
	return ""
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
		case *PS.CaseExpr:
			if err := collect(v.Expr); err != nil {
				return err
			}
			for _, w := range v.WhenList {
				if err := collect(w.Cond); err != nil {
					return err
				}
				if err := collect(w.Then); err != nil {
					return err
				}
			}
			return collect(v.Else)
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
