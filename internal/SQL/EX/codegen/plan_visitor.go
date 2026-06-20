package codegen

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type CodegenOp struct {
	Type       OpType
	TypeName   string
	Exprs      []CodegenExpr
	Children   []CodegenOp
	ChildTypes []OpType
	ColNames   []string
	ColTypes   []LX.TokenType
	HasPred    bool
	HasOrderBy bool
	HasGroupBy bool
	Cost       float64
}

type CodegenExpr struct {
	Shape     ExprShape
	Op        int
	LeftCol   string
	RightCol  string
	LitVal    any
	LitType   LX.TokenType
	IsLiteral bool
}

type CodegenPlan struct {
	Root          CodegenOp
	SchemaVersion uint64
	PlanHash      string
	NumOps        int
}

type PlanVisitor struct {
	hashSeed  string
	schemaVer uint64
}

func NewPlanVisitor() *PlanVisitor {
	return &PlanVisitor{}
}

func (v *PlanVisitor) Visit(op EX.Operator) CodegenPlan {
	root := v.visitOp(op)
	h := sha256.Sum256([]byte(v.hashSeed + v.serializeOp(root)))
	return CodegenPlan{
		Root:          root,
		SchemaVersion: v.schemaVer,
		PlanHash:      string(h[:]),
		NumOps:        countOps(root),
	}
}

func (v *PlanVisitor) visitOp(op EX.Operator) CodegenOp {
	if op == nil {
		return CodegenOp{Type: OpUnknown}
	}
	base := v.baseOp(op)
	base.Children = v.visitChildren(op)
	return base
}

func (v *PlanVisitor) baseOp(op EX.Operator) CodegenOp {
	switch o := op.(type) {
	case *EX.SeqScan:
		return CodegenOp{
			Type:     OpSeqScan,
			TypeName: "SeqScan",
		}
	case *EX.IndexScan:
		return CodegenOp{
			Type:     OpIndexScan,
			TypeName: "IndexScan",
		}
	case *EX.Filter:
		cg := CodegenOp{
			Type:     OpFilter,
			TypeName: "Filter",
			HasPred:  o.Predicate() != nil,
		}
		if o.Predicate() != nil {
			cg.Exprs = v.visitExpr(o.Predicate())
		}
		return cg
	case *EX.Project:
		return CodegenOp{
			Type:     OpProject,
			TypeName: "Project",
		}
	case *EX.Sort:
		return CodegenOp{
			Type:       OpSort,
			TypeName:   "Sort",
			HasOrderBy: true,
		}
	case *EX.Limit:
		return CodegenOp{
			Type:     OpLimit,
			TypeName: "Limit",
		}
	case *EX.NestedLoopJoin:
		return CodegenOp{
			Type:     OpNestedLoopJoin,
			TypeName: "NestedLoopJoin",
		}
	case *EX.HashJoin:
		return CodegenOp{
			Type:     OpHashJoin,
			TypeName: "HashJoin",
		}
	case *EX.HashAggregate:
		return CodegenOp{
			Type:       OpHashAggregate,
			TypeName:   "HashAggregate",
			HasGroupBy: true,
		}
	case *EX.Aggregate:
		return CodegenOp{
			Type:       OpAggregate,
			TypeName:   "Aggregate",
			HasGroupBy: true,
		}
	case *EX.Distinct:
		return CodegenOp{
			Type:     OpDistinct,
			TypeName: "Distinct",
		}
	case *EX.CompoundOp:
		return CodegenOp{
			Type:     OpCompound,
			TypeName: "CompoundOp",
		}
	case *EX.WindowOperator:
		return CodegenOp{
			Type:     OpWindow,
			TypeName: "WindowOperator",
		}
	case *EX.ExplainStmtOp:
		return CodegenOp{
			Type:     OpExplain,
			TypeName: "ExplainStmtOp",
		}
	case *EX.CreateViewOperator:
		return CodegenOp{
			Type:     OpCreateView,
			TypeName: "CreateViewOperator",
		}
	default:
		return CodegenOp{Type: OpUnknown, TypeName: fmt.Sprintf("%T", op)}
	}
}

func (v *PlanVisitor) visitChildren(op EX.Operator) []CodegenOp {
	var children []CodegenOp
	switch o := op.(type) {
	case interface{ Child() EX.Operator }:
		if c := o.Child(); c != nil {
			children = append(children, v.visitOp(c))
		}
	}
	switch o := op.(type) {
	case *EX.NestedLoopJoin:
		if o.LeftChild() != nil {
			children = append(children, v.visitOp(o.LeftChild()))
		}
		if o.RightChild() != nil {
			children = append(children, v.visitOp(o.RightChild()))
		}
	case *EX.HashJoin:
		if o.LeftChild() != nil {
			children = append(children, v.visitOp(o.LeftChild()))
		}
		if o.RightChild() != nil {
			children = append(children, v.visitOp(o.RightChild()))
		}
	}
	return children
}

func (v *PlanVisitor) visitExpr(expr PS.Expr) []CodegenExpr {
	var result []CodegenExpr
	v.collectExprs(expr, &result)
	return result
}

func (v *PlanVisitor) collectExprs(expr PS.Expr, out *[]CodegenExpr) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *PS.BinaryExpr:
		shape := ExprShapeFromPS(e)
		cg := CodegenExpr{
			Shape: shape,
			Op:    e.Op,
		}
		if id, ok := e.Left.(*PS.Ident); ok {
			cg.LeftCol = id.Name
		}
		if id, ok := e.Left.(*PS.QualifiedName); ok {
			cg.LeftCol = id.Table + "." + id.Name
		}
		if id, ok := e.Right.(*PS.Ident); ok {
			cg.RightCol = id.Name
		}
		if id, ok := e.Right.(*PS.QualifiedName); ok {
			cg.RightCol = id.Table + "." + id.Name
		}
		if n, ok := e.Right.(*PS.NumberLiteral); ok {
			cg.LitVal = n.Val
			cg.LitType = LX.T_INT_KW
			cg.IsLiteral = true
		}
		if f, ok := e.Right.(*PS.FloatLiteral); ok {
			cg.LitVal = f.Val
			cg.LitType = LX.T_FLOAT_KW
			cg.IsLiteral = true
		}
		if s, ok := e.Right.(*PS.StringLiteral); ok {
			cg.LitVal = s.Val
			cg.LitType = LX.T_TEXT
			cg.IsLiteral = true
		}
		*out = append(*out, cg)
		v.collectExprs(e.Left, out)
		v.collectExprs(e.Right, out)
	case *PS.UnaryExpr:
		*out = append(*out, CodegenExpr{
			Shape: ExprNot,
			Op:    e.Op,
		})
		v.collectExprs(e.Operand, out)
	}
}

func (v *PlanVisitor) serializeOp(op CodegenOp) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s(", op.TypeName))
	for _, c := range op.Children {
		b.WriteString(v.serializeOp(c))
		b.WriteString(",")
	}
	b.WriteString(")")
	return b.String()
}

func countOps(op CodegenOp) int {
	n := 1
	for _, c := range op.Children {
		n += countOps(c)
	}
	return n
}

func init() {
	_ = context.Background
}

type childProvider interface {
	Child() EX.Operator
}

type leftRightProvider interface {
	LeftChild() EX.Operator
	RightChild() EX.Operator
}
