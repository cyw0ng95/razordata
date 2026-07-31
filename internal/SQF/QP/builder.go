package QP

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// BuildQueryPlan walks a planner Operator tree and constructs a 1:1
// QueryPlan DAG. This is a pure data transformation — no semantics change.
// REQ002254.
func BuildQueryPlan(root DT.Operator) *QueryPlan {
	b := &builder{nodes: make([]PlanNode, 0, 16)}
	idx := b.buildNode(root)
	return &QueryPlan{Nodes: b.nodes, RootIdx: idx}
}

type builder struct {
	nodes []PlanNode
}

func (b *builder) buildNode(op DT.Operator) int {
	switch o := op.(type) {
	case *OP.SeqScan:
		b.nodes = append(b.nodes, planNodeSeqScan(o))
	case *OP.IndexScan:
		b.nodes = append(b.nodes, planNodeIndexScan(o))
	case *OP.Filter:
		b.nodes = append(b.nodes, planNodeFilter(o, b))
	case *OP.Project:
		b.nodes = append(b.nodes, planNodeProject(o, b))
	case *OP.Sort:
		b.nodes = append(b.nodes, planNodeSort(o, b))
	case *OP.Limit:
		b.nodes = append(b.nodes, planNodeLimit(o, b))
	case *OP.Offset:
		b.nodes = append(b.nodes, planNodeOffset(o, b))
	case *OP.Distinct:
		b.nodes = append(b.nodes, planNodeDistinct(o, b))
	case *OP.HashJoin:
		b.nodes = append(b.nodes, planNodeHashJoin(o, b))
	case *OP.HashCrossJoin:
		b.nodes = append(b.nodes, planNodeHashCrossJoin(o, b))
	case *OP.NestedLoopJoin:
		b.nodes = append(b.nodes, planNodeNestedLoopJoin(o, b))
	case *AG.Aggregate:
		b.nodes = append(b.nodes, planNodeAggregate(o, b))
	case *AG.WindowOperator:
		b.nodes = append(b.nodes, planNodeWindow(o, b))
	case *OP.CompoundOp:
		b.nodes = append(b.nodes, planNodeCompoundOp(o, b))
	case *WT.Insert:
		b.nodes = append(b.nodes, PlanNode{Type: NodeInsert})
	case *WT.Update:
		b.nodes = append(b.nodes, PlanNode{Type: NodeUpdate})
	case *WT.Delete:
		b.nodes = append(b.nodes, PlanNode{Type: NodeDelete})
	case *OP.ConstRow:
		b.nodes = append(b.nodes, planNodeConstRow(o))
	case *OP.FusedScan:
		b.nodes = append(b.nodes, planNodeFusedScan(o))
	case *OP.Values:
		b.nodes = append(b.nodes, planNodeValues(o))
	case *OP.ValuesRows:
		b.nodes = append(b.nodes, planNodeValuesRows(o))
	case *OP.FilterProject:
		b.nodes = append(b.nodes, planNodeFilterProject(o, b))
	case *OP.MergeJoin:
		b.nodes = append(b.nodes, PlanNode{Type: NodeMergeJoin})
	case *OP.BitmapHeapScan:
		b.nodes = append(b.nodes, planNodeBitmapHeapScan(o))
	default:
		b.nodes = append(b.nodes, PlanNode{Type: NodeSource})
	}
	return len(b.nodes) - 1
}

func planNodeSeqScan(s *OP.SeqScan) PlanNode {
	n := PlanNode{Type: NodeSeqScan, TableName: s.Table()}
	if sch := s.Schema(); sch != nil && len(sch.Cols) > 0 {
		names := make([]string, len(sch.Cols))
		types := make([]LX.TokenType, len(sch.Cols))
		copy(names, sch.Cols)
		if len(sch.ColTypes) > 0 {
			copy(types, sch.ColTypes)
		}
		n.Schema = &NodeSchema{Cols: names, ColTypes: types}
	}
	return n
}

func planNodeIndexScan(s *OP.IndexScan) PlanNode {
	n := PlanNode{Type: NodeIndexScan, TableName: s.Table()}
	if sch := s.Schema(); sch != nil && len(sch.Cols) > 0 {
		names := make([]string, len(sch.Cols))
		types := make([]LX.TokenType, len(sch.Cols))
		copy(names, sch.Cols)
		if len(sch.ColTypes) > 0 {
			copy(types, sch.ColTypes)
		}
		n.Schema = &NodeSchema{Cols: names, ColTypes: types}
	}
	return n
}

func planNodeFusedScan(f *OP.FusedScan) PlanNode {
	n := PlanNode{Type: NodeFusedScan, TableName: f.Table()}
	if ss, ok := DT.SchemaFor(f.Table()); ok && len(ss.Cols) > 0 {
		names := make([]string, len(ss.Cols))
		types := make([]LX.TokenType, len(ss.Cols))
		copy(names, ss.Cols)
		if len(ss.ColTypes) > 0 {
			copy(types, ss.ColTypes)
		}
		n.Schema = &NodeSchema{Cols: names, ColTypes: types}
	}
	return n
}

func planNodeBitmapHeapScan(b *OP.BitmapHeapScan) PlanNode {
	n := PlanNode{Type: NodeBitmapHeapScan, TableName: b.Table()}
	if sch := b.Schema(); sch != nil && len(sch.Cols) > 0 {
		names := make([]string, len(sch.Cols))
		types := make([]LX.TokenType, len(sch.Cols))
		copy(names, sch.Cols)
		if len(sch.ColTypes) > 0 {
			copy(types, sch.ColTypes)
		}
		n.Schema = &NodeSchema{Cols: names, ColTypes: types}
	}
	return n
}

func planNodeFilter(f *OP.Filter, b *builder) PlanNode {
	childIdx := b.buildNode(f.Child())
	n := PlanNode{Type: NodeFilter, Children: []int{childIdx}}
	if pred := f.Predicate(); pred != nil {
		n.Exprs = []PS.Expr{pred}
	}
	n.Schema = copySchema(b.nodes[childIdx].Schema)
	return n
}

func planNodeProject(p *OP.Project, b *builder) PlanNode {
	childIdx := b.buildNode(p.Child())
	exprs := p.Cols()
	names := make([]string, len(exprs))
	for i, e := range exprs {
		names[i] = exprName(e)
	}
	n := PlanNode{
		Type:     NodeProject,
		Children: []int{childIdx},
		Exprs:    exprs,
		Schema:   &NodeSchema{Cols: names, ColTypes: make([]LX.TokenType, len(names))},
	}
	return n
}

func planNodeSort(s *OP.Sort, b *builder) PlanNode {
	childIdx := b.buildNode(s.Child())
	keys := s.Keys()
	exprs := make([]PS.Expr, len(keys))
	for i, k := range keys {
		exprs[i] = k.Expr
	}
	n := PlanNode{Type: NodeSort, Children: []int{childIdx}, Exprs: exprs}
	n.Schema = copySchema(b.nodes[childIdx].Schema)
	return n
}

func planNodeLimit(l *OP.Limit, b *builder) PlanNode {
	childIdx := b.buildNode(l.Child())
	n := PlanNode{Type: NodeLimit, Children: []int{childIdx}}
	n.Schema = copySchema(b.nodes[childIdx].Schema)
	return n
}

func planNodeOffset(o *OP.Offset, b *builder) PlanNode {
	childIdx := b.buildNode(o.Child())
	n := PlanNode{Type: NodeOffset, Children: []int{childIdx}}
	n.Schema = copySchema(b.nodes[childIdx].Schema)
	return n
}

func planNodeDistinct(d *OP.Distinct, b *builder) PlanNode {
	childIdx := b.buildNode(d.Child())
	n := PlanNode{Type: NodeDistinct, Children: []int{childIdx}}
	n.Schema = copySchema(b.nodes[childIdx].Schema)
	return n
}

func planNodeHashJoin(j *OP.HashJoin, b *builder) PlanNode {
	leftIdx := b.buildNode(j.LeftChild())
	rightIdx := b.buildNode(j.RightChild())
	keys := mergeKeys(j.LeftKeys(), j.RightKeys())
	n := PlanNode{
		Type:     NodeHashJoin,
		Children: []int{leftIdx, rightIdx},
		Exprs:    keys,
	}
	n.Schema = joinSchema(b.nodes[leftIdx].Schema, b.nodes[rightIdx].Schema)
	return n
}

func planNodeHashCrossJoin(j *OP.HashCrossJoin, b *builder) PlanNode {
	leftIdx := b.buildNode(j.LeftChild())
	rightIdx := b.buildNode(j.RightChild())
	keys := mergeKeys([]string{j.LeftKeyName()}, []string{j.RightKeyName()})
	n := PlanNode{
		Type:     NodeHashCrossJoin,
		Children: []int{leftIdx, rightIdx},
		Exprs:    keys,
	}
	n.Schema = joinSchema(b.nodes[leftIdx].Schema, b.nodes[rightIdx].Schema)
	return n
}

func planNodeNestedLoopJoin(j *OP.NestedLoopJoin, b *builder) PlanNode {
	leftIdx := b.buildNode(j.LeftChild())
	rightIdx := b.buildNode(j.RightChild())
	n := PlanNode{
		Type:     NodeNestedLoopJoin,
		Children: []int{leftIdx, rightIdx},
	}
	n.Schema = joinSchema(b.nodes[leftIdx].Schema, b.nodes[rightIdx].Schema)
	return n
}

func planNodeAggregate(a *AG.Aggregate, b *builder) PlanNode {
	childIdx := b.buildNode(a.Child())
	groupExprs := a.GroupCols()
	aggExprs := a.Aggs()
	allExprs := make([]PS.Expr, 0, len(groupExprs)+len(aggExprs))
	allExprs = append(allExprs, groupExprs...)
	allExprs = append(allExprs, aggExprs...)
	n := PlanNode{
		Type:     NodeAggregate,
		Children: []int{childIdx},
		Exprs:    allExprs,
	}
	n.Schema = aggSchema(groupExprs, aggExprs, b.nodes[childIdx].Schema)
	return n
}

func planNodeWindow(w *AG.WindowOperator, b *builder) PlanNode {
	childIdx := b.buildNode(w.Input())
	childOut := b.nodes[childIdx].Schema
	names := make([]string, len(childOut.Cols)+1)
	types := make([]LX.TokenType, len(childOut.Cols)+1)
	copy(names, childOut.Cols)
	copy(types, childOut.ColTypes)
	names[len(names)-1] = w.FuncName()
	types[len(types)-1] = LX.T_INT_KW
	n := PlanNode{
		Type:     NodeWindow,
		Children: []int{childIdx},
		Schema:   &NodeSchema{Cols: names, ColTypes: types},
	}
	return n
}

func planNodeCompoundOp(c *OP.CompoundOp, b *builder) PlanNode {
	leftIdx := b.buildNode(c.LeftChild())
	rightIdx := b.buildNode(c.RightChild())
	n := PlanNode{
		Type:     NodeCompoundOp,
		Children: []int{leftIdx, rightIdx},
	}
	n.Schema = copySchema(b.nodes[leftIdx].Schema)
	return n
}

func planNodeConstRow(c *OP.ConstRow) PlanNode {
	n := PlanNode{Type: NodeConstRow}
	if cols := c.Cols(); len(cols) > 0 {
		types := make([]LX.TokenType, len(cols))
		copy(types, c.Types())
		n.Schema = &NodeSchema{Cols: cols, ColTypes: types}
	}
	return n
}

func planNodeValues(v *OP.Values) PlanNode {
	n := PlanNode{Type: NodeValues, Exprs: v.Cols()}
	if names := v.ColNames(); len(names) > 0 {
		types := make([]LX.TokenType, len(names))
		n.Schema = &NodeSchema{Cols: names, ColTypes: types}
	}
	return n
}

func planNodeValuesRows(v *OP.ValuesRows) PlanNode {
	if len(v.Rows()) == 0 {
		return PlanNode{Type: NodeValues}
	}
	firstRow := v.Rows()[0]
	names := make([]string, len(firstRow))
	for i, e := range firstRow {
		names[i] = exprName(e)
	}
	return PlanNode{Type: NodeValues, Schema: &NodeSchema{Cols: names}}
}

func planNodeFilterProject(fp *OP.FilterProject, b *builder) PlanNode {
	childIdx := b.buildNode(fp.Child())
	pred := fp.Predicate()
	exprs := fp.Cols()
	var allExprs []PS.Expr
	if pred != nil {
		allExprs = append(allExprs, pred)
	}
	allExprs = append(allExprs, exprs...)
	n := PlanNode{
		Type:     NodeFilterProject,
		Children: []int{childIdx},
		Exprs:    allExprs,
	}
	if len(exprs) > 0 {
		names := make([]string, len(exprs))
		for i, e := range exprs {
			names[i] = exprName(e)
		}
		types := make([]LX.TokenType, len(names))
		n.Schema = &NodeSchema{Cols: names, ColTypes: types}
	} else {
		n.Schema = copySchema(b.nodes[childIdx].Schema)
	}
	return n
}

// --- helpers ---

func copySchema(s *NodeSchema) *NodeSchema {
	if s == nil {
		return nil
	}
	return &NodeSchema{
		Cols:     append([]string(nil), s.Cols...),
		ColTypes: append([]LX.TokenType(nil), s.ColTypes...),
	}
}

func joinSchema(left, right *NodeSchema) *NodeSchema {
	if left == nil && right == nil {
		return nil
	}
	var names []string
	var types []LX.TokenType
	if left != nil {
		names = append(names, left.Cols...)
		types = append(types, left.ColTypes...)
	}
	if right != nil {
		names = append(names, right.Cols...)
		types = append(types, right.ColTypes...)
	}
	return &NodeSchema{Cols: names, ColTypes: types}
}

func aggSchema(groupExprs, aggExprs []PS.Expr, child *NodeSchema) *NodeSchema {
	names := make([]string, 0, len(groupExprs)+len(aggExprs))
	types := make([]LX.TokenType, 0, len(groupExprs)+len(aggExprs))
	for _, e := range groupExprs {
		names = append(names, exprName(e))
		if child != nil {
			for i, cn := range child.Cols {
				if id, ok := e.(*PS.Ident); ok && id.Name == cn {
					t := LX.T_TEXT
					if i < len(child.ColTypes) {
						t = child.ColTypes[i]
					}
					types = append(types, t)
					break
				}
			}
		}
	}
	for _, e := range aggExprs {
		names = append(names, exprName(e))
		types = append(types, LX.T_INT_KW)
	}
	return &NodeSchema{Cols: names, ColTypes: types}
}

func mergeKeys(left, right []string) []PS.Expr {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	exprs := make([]PS.Expr, 0, len(left))
	for _, name := range left {
		exprs = append(exprs, &PS.Ident{Name: name})
	}
	return exprs
}

func exprName(e PS.Expr) string {
	if e == nil {
		return ""
	}
	switch expr := e.(type) {
	case *PS.Ident:
		return expr.Name
	case *PS.AliasedExpr:
		return expr.Alias
	case *PS.StarExpr:
		return "*"
	default:
		return "expr"
	}
}
