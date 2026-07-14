package PF

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	OC "github.com/cyw0ng95/razordata/internal/SQO/OC"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type IndexSelectionPass struct{}

func (p *IndexSelectionPass) Name() string { return "index_selection" }

func (p *IndexSelectionPass) Apply(plan *OC.Plan, ctx *OC.Context) (*OC.Plan, error) {
	if plan == nil || plan.Root == nil || ctx.Catalog == nil {
		return plan, nil
	}
	plan.Root = WalkOp(plan.Root, func(op pl.Operator) pl.Operator {
		return tryReplaceWithIndexScan(op, ctx)
	})
	return plan, nil
}

// tryReplaceWithIndexScan checks if op is a SeqScan-like operator with a
// predicate that matches an available index, and if so, replaces it with
// an IndexScan.
func tryReplaceWithIndexScan(op pl.Operator, ctx *OC.Context) pl.Operator {
	pc, ok := op.(pl.PredicateCarrier)
	if !ok || !pc.HasPredicate() {
		return op
	}
	rs, ok := op.(pl.RelationSource)
	if !ok {
		return op
	}

	table := rs.Table()
	indexes := ctx.Catalog.Indexes(table)
	if len(indexes) == 0 {
		return op
	}

	pred, ok := pc.Predicate().(PS.Expr)
	if !ok || pred == nil {
		return op
	}

	// Try each index: check if any column referenced in the predicate
	// matches the index's leading column.
	for _, idx := range indexes {
		idxCols := ctx.Catalog.IndexColumns(table, idx)
		if len(idxCols) == 0 {
			continue
		}
		leadingCol := idxCols[0]
		if !predicateReferencesColumn(pred, leadingCol) {
			continue
		}
		// Found a matching index. Build schema and create IndexScan.
		schema := ctx.Tables[table].Columns

		// If the predicate is a simple equality, pass the value for
		// point-lookup. Otherwise, create a scan without a literal.
		newOp := ctx.Factory.NewIndexScan(table, idx, schema)
		if newOp == nil {
			return op
		}
		if pc2, ok := newOp.(pl.PredicateCarrier); ok {
			pc2.SetPredicate(pred)
		}
		// Copy child chain if single-child source
		if p, ok := op.(pl.Parent); ok {
			if p2, ok := newOp.(pl.Parent); ok {
				p2.SetChild(p.Child())
			}
		}
		return newOp
	}
	return op
}

// predicateReferencesColumn reports whether the predicate expression
// references the given column name.
func predicateReferencesColumn(pred PS.Expr, col string) bool {
	found := false
	CO.WalkExpr(pred, func(e PS.Expr) {
		if found {
			return
		}
		switch n := e.(type) {
		case *PS.Ident:
			if n.Name == col {
				found = true
			}
		case *PS.QualifiedName:
			if n.Name == col {
				found = true
			}
		}
	})
	return found
}