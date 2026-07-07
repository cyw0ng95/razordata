package EX

import (
	AD "github.com/cyw0ng95/razordata/internal/SQB/AD"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// addSubqueryPlanNodes walks the SELECT list for scalar subquery and
// EXISTS expressions, plans each inner query via the planner, builds a
// PlanNode tree for each, and attaches them as children of root.
//
// This is called from planExplain; it only fires when we are in an EXPLAIN
// context, so the extra planning overhead is justified for diagnostic output.
//
// REQ001344: subquery plans render indented under their parent operator
// in EXPLAIN output.
func (p *Planner) addSubqueryPlanNodes(root *AD.PlanNode, stmt PS.Stmt) {
	if p == nil || root == nil {
		return
	}
	sel, ok := stmt.(*PS.Select)
	if !ok || sel == nil {
		return
	}
	for _, col := range sel.Cols {
		subExprs := extractSubqueryExprs(col)
		for _, sub := range subExprs {
			subPlan, err := p.Plan(sub.Subquery)
			if err != nil || subPlan == nil || subPlan.Root == nil {
				continue
			}
			childPlanNode := buildPlanNodeTree(subPlan.Root, p)
			if childPlanNode == nil {
				continue
			}
			// Mark as subquery plan with a descriptive type.
			childPlanNode.Type = "ScalarSubquery"
			root.Add(childPlanNode)
		}
		existsExprs := extractExistsExprs(col)
		for _, ex := range existsExprs {
			subPlan, err := p.Plan(ex.Subquery)
			if err != nil || subPlan == nil || subPlan.Root == nil {
				continue
			}
			childPlanNode := buildPlanNodeTree(subPlan.Root, p)
			if childPlanNode == nil {
				continue
			}
			childPlanNode.Type = "ExistsSubquery"
			root.Add(childPlanNode)
		}
	}
	// Also check WHERE for EXISTS expressions.
	if sel.Where != nil {
		for _, ex := range extractExistsExprs(sel.Where) {
			subPlan, err := p.Plan(ex.Subquery)
			if err != nil || subPlan == nil || subPlan.Root == nil {
				continue
			}
			childPlanNode := buildPlanNodeTree(subPlan.Root, p)
			if childPlanNode == nil {
				continue
			}
			childPlanNode.Type = "ExistsSubquery"
			root.Add(childPlanNode)
		}
	}
}

// extractSubqueryExprs walks an expression tree and returns all
// *PS.SubqueryExpr nodes found. Depth-first, order unspecified.
func extractSubqueryExprs(e PS.Expr) []*PS.SubqueryExpr {
	var out []*PS.SubqueryExpr
	if e == nil {
		return out
	}
	switch v := e.(type) {
	case *PS.SubqueryExpr:
		out = append(out, v)
	case *PS.BinaryExpr:
		out = append(out, extractSubqueryExprs(v.Left)...)
		out = append(out, extractSubqueryExprs(v.Right)...)
	case *PS.UnaryExpr:
		out = append(out, extractSubqueryExprs(v.Operand)...)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			out = append(out, extractSubqueryExprs(a)...)
		}
	case *PS.AggregateFunc:
		out = append(out, extractSubqueryExprs(v.Arg)...)
	case *PS.AliasedExpr:
		out = append(out, extractSubqueryExprs(v.Expr)...)
	case *PS.BetweenExpr:
		out = append(out, extractSubqueryExprs(v.Expr)...)
		out = append(out, extractSubqueryExprs(v.Low)...)
		out = append(out, extractSubqueryExprs(v.High)...)
	case *PS.InExpr:
		out = append(out, extractSubqueryExprs(v.Expr)...)
		for _, a := range v.List {
			out = append(out, extractSubqueryExprs(a)...)
		}
	case *PS.CaseExpr:
		if v.Expr != nil {
			out = append(out, extractSubqueryExprs(v.Expr)...)
		}
		for _, w := range v.WhenList {
			out = append(out, extractSubqueryExprs(w.Cond)...)
		}
		for _, w := range v.WhenList {
			out = append(out, extractSubqueryExprs(w.Then)...)
		}
		if v.Else != nil {
			out = append(out, extractSubqueryExprs(v.Else)...)
		}
	case *PS.CastExpr:
		out = append(out, extractSubqueryExprs(v.Expr)...)
	}
	return out
}

// extractExistsExprs walks an expression tree and returns all
// *PS.ExistsExpr nodes found.
func extractExistsExprs(e PS.Expr) []*PS.ExistsExpr {
	var out []*PS.ExistsExpr
	if e == nil {
		return out
	}
	switch v := e.(type) {
	case *PS.ExistsExpr:
		out = append(out, v)
	case *PS.BinaryExpr:
		out = append(out, extractExistsExprs(v.Left)...)
		out = append(out, extractExistsExprs(v.Right)...)
	case *PS.UnaryExpr:
		out = append(out, extractExistsExprs(v.Operand)...)
	case *PS.FunctionCall:
		for _, a := range v.Args {
			out = append(out, extractExistsExprs(a)...)
		}
	case *PS.AliasedExpr:
		out = append(out, extractExistsExprs(v.Expr)...)
	}
	return out
}