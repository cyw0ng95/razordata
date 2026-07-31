package QP

import (
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// FilterProjectFusionPass merges adjacent Filter + Project nodes into
// a single FilterProject node. Detects both Filter{Project{...}} and
// Project{Filter{...}} patterns. REQ002257.
type FilterProjectFusionPass struct{}

func (p *FilterProjectFusionPass) Name() string { return "filter_project_fusion" }

func (p *FilterProjectFusionPass) Apply(qp *QueryPlan) error {
	if qp == nil || qp.RootIdx < 0 {
		return nil
	}
	qp.walkBottomUp(func(idx int, node *PlanNode) bool {
		// Pattern: Filter → Project
		if node.Type != NodeFilter || len(node.Children) != 1 {
			return true
		}
		childIdx := node.Children[0]
		child := qp.Node(childIdx)
		if child.Type != NodeProject {
			return true
		}
		// Merge: combine filter predicate + project exprs
		allExprs := make([]PS.Expr, 0, len(node.Exprs)+len(child.Exprs))
		allExprs = append(allExprs, node.Exprs...)
		allExprs = append(allExprs, child.Exprs...)
		mergedSchema := mergeSchemas(child.Schema, child.Schema)
		qp.Nodes[childIdx] = PlanNode{
			Type:     NodeFilterProject,
			Schema:   mergedSchema,
			Children: child.Children,
			Exprs:    allExprs,
			TableName: child.TableName,
		}
		qp.Nodes[idx] = PlanNode{
			Type:     NodeFilterProject,
			Schema:   child.Schema,
			Children: child.Children,
			Exprs:    allExprs,
			TableName: child.TableName,
		}
		qp.Nodes[idx].Children = child.Children
		return true
	})
	return nil
}

func mergeSchemas(a, b *NodeSchema) *NodeSchema {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &NodeSchema{
		Cols:     append([]string(nil), a.Cols...),
		ColTypes: append([]LX.TokenType(nil), a.ColTypes...),
	}
}
