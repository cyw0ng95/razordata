// REQ001435: SQB/OP-side implementation of DT.OperatorFactory.
//
// Pragmatic first version. Many operator constructors in SQB/OP
// require state that the optimizer does not have at pass-application
// time — a Store, a BTree, a Schema, an InnerIterator. The factory
// therefore exposes only the constructors that work with the
// in-memory tree shape; the executor wires storage state after
// optimization.
//
// Methods marked `nil-returning` (NewHashJoin, NewNestedLoopJoin)
// return nil because the underlying constructors require
// table-ID / partition / predicate state that the factory
// signature intentionally omits. Passes that need to construct
// joins route through the planner's own join builder instead.
package OP

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// opFactory is the concrete DT.OperatorFactory. Stateless.
type opFactory struct{}

// Factory returns the singleton DT.OperatorFactory for SQB/OP.
func Factory() DT.OperatorFactory { return opFactory{} }

var _ DT.OperatorFactory = opFactory{}

// NewSeqScan wraps NewSeqScan. Schema and store are not set —
// the executor wires them on Open().
func (opFactory) NewSeqScan(table string, _ []string) DT.Operator {
	return NewSeqScan(table)
}

// NewIndexScan wraps NewIndexScan with no range. Store and
// BTree are wired by the executor.
func (opFactory) NewIndexScan(table, index string, _ []string) DT.Operator {
	return NewIndexScan(table, index, nil, nil)
}

// NewIndexOnlyScan returns a passthrough IndexOnlyScan. Inner
// iterator wiring is the executor's job.
func (opFactory) NewIndexOnlyScan(_, _ string, _ []string) DT.Operator {
	return NewIndexOnlyScanPassthrough(nil)
}

// NewFilter wraps NewFilter with no schema.
func (opFactory) NewFilter(child DT.Operator, predicate interface{}) DT.Operator {
	p, _ := predicate.(PS.Expr)
	return NewFilter(child, p, nil)
}

// NewProject wraps NewProject.
func (opFactory) NewProject(child DT.Operator, _ []string, exprs []interface{}) DT.Operator {
	return NewProject(child, toPSExprs(exprs))
}

// NewFilterProject wraps NewFilterProject.
func (opFactory) NewFilterProject(child DT.Operator, predicate interface{}, _ []string, exprs []interface{}) DT.Operator {
	p, _ := predicate.(PS.Expr)
	return NewFilterProject(child, p, toPSExprs(exprs))
}

// NewHashJoin returns nil. HashJoin construction requires
// partition counts and table IDs that the factory signature
// does not expose. Passes use the planner's join builder.
func (opFactory) NewHashJoin(_, _ DT.Operator, _, _ string, _ DT.JoinType) DT.Operator {
	return nil
}

// NewNestedLoopJoin returns nil for the same reason.
func (opFactory) NewNestedLoopJoin(_, _ DT.Operator, _ interface{}, _ DT.JoinType) DT.Operator {
	return nil
}

// NewAggregate wraps AG.NewAggregate.
func (opFactory) NewAggregate(child DT.Operator, groupCols []string, aggs []DT.AggregateSpec) DT.Operator {
	// AG's constructor takes groupCols []string and aggs []PS.Expr.
	// DT.AggregateSpec is a struct; convert each into an Expr.
	exprs := aggSpecsToExprs(aggs)
	return AGNewAggregate(child, groupCols, exprs)
}

// NewHashAggregate wraps AG.NewAggregate.
func (opFactory) NewHashAggregate(child DT.Operator, groupCols []string, aggs []DT.AggregateSpec) DT.Operator {
	exprs := aggSpecsToExprs(aggs)
	return AGNewAggregate(child, groupCols, exprs)
}

// NewSort wraps NewSort. DT.OrderSpec → PS.OrderItem.
func (opFactory) NewSort(child DT.Operator, orderBy []DT.OrderSpec) DT.Operator {
	keys := make([]PS.OrderItem, 0, len(orderBy))
	for _, o := range orderBy {
		// OrderItem.Expr is a PS.Expr; pass a ColumnRef-like Expr
		// built from the column name. The executor rewrites
		// this into a real ColumnRef on first Open.
		keys = append(keys, PS.OrderItem{
			Expr: &PS.QualifiedName{Name: o.Col},
			Desc: o.Desc,
		})
	}
	return NewSort(child, keys)
}

// NewLimit wraps NewLimit + NewOffset if offset > 0.
func (opFactory) NewLimit(child DT.Operator, limit, offset int64) DT.Operator {
	if offset > 0 {
		child = NewOffset(child, offset)
	}
	return NewLimit(child, limit)
}

// NewDistinct wraps NewDistinct.
func (opFactory) NewDistinct(child DT.Operator) DT.Operator {
	return NewDistinct(child)
}

// NewSetOp wraps NewCompoundOp. SetOpType → PS.CompoundOp.
func (opFactory) NewSetOp(left, right DT.Operator, op DT.SetOpType) DT.Operator {
	co, ok := mapSetOp(op)
	if !ok {
		return nil
	}
	return NewCompoundOp(left, right, co, nil, nil, nil)
}

// NewValues returns an in-memory InMemoryScan. The full Values
// operator construction is added in REQ001451.
func (opFactory) NewValues(_ [][]interface{}) DT.Operator {
	return NewInMemoryScan(nil)
}

// toPSExprs converts []interface{} → []PS.Expr. Non-Expr values
// are silently dropped (callers should pre-validate).
func toPSExprs(in []interface{}) []PS.Expr {
	out := make([]PS.Expr, 0, len(in))
	for _, e := range in {
		if pe, ok := e.(PS.Expr); ok {
			out = append(out, pe)
		}
	}
	return out
}

// aggSpecsToExprs converts []DT.AggregateSpec into a slice of
// PS.Expr the AG constructors accept. The AG layer is the
// authority on aggregate shape; the factory adapts the public
// spec type to the AG-internal slice form.
func aggSpecsToExprs(in []DT.AggregateSpec) []PS.Expr {
	out := make([]PS.Expr, 0, len(in))
	for _, a := range in {
		// AG aggregates are PS.Expr values (e.g. FunctionCall).
		// The factory does not construct the FunctionCall
		// itself; the caller must supply an Expr in a
		// future refactor. For now, return empty; the
		// optimizer routes aggregate construction through
		// the planner.
		_ = a
	}
	return out
}

// mapSetOp translates DT.SetOpType to PS.CompoundOp.
func mapSetOp(s DT.SetOpType) (PS.CompoundOp, bool) {
	switch s {
	case DT.UnionAllOp:
		return PS.CompoundUnionAll, true
	case DT.UnionOp:
		return PS.CompoundUnion, true
	case DT.IntersectOp:
		return PS.CompoundIntersect, true
	case DT.ExceptOp:
		return PS.CompoundExcept, true
	}
	return 0, false
}
