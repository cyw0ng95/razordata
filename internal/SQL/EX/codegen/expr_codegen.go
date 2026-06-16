package codegen

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type ExprCompiler struct {
	imports map[string]bool
	buf     strings.Builder
	indent  int
}

func NewExprCompiler() *ExprCompiler {
	return &ExprCompiler{
		imports: make(map[string]bool),
	}
}

func (c *ExprCompiler) Compile(expr PS.Expr, rowVar string, paramsVar string) string {
	c.buf.Reset()
	c.indent = 0
	c.compileExpr(expr, rowVar, paramsVar)
	return c.buf.String()
}

func (c *ExprCompiler) compileExpr(expr PS.Expr, rowVar, paramsVar string) {
	if expr == nil {
		c.buf.WriteString("nil")
		return
	}
	switch e := expr.(type) {
	case *PS.NumberLiteral:
		fmt.Fprintf(&c.buf, "int64(%d)", e.Val)
	case *PS.FloatLiteral:
		fmt.Fprintf(&c.buf, "%g", e.Val)
	case *PS.StringLiteral:
		fmt.Fprintf(&c.buf, "%q", e.Val)
	case *PS.BoolLiteral:
		if e.Val {
			c.buf.WriteString("true")
		} else {
			c.buf.WriteString("false")
		}
	case *PS.NullLiteral:
		c.buf.WriteString("nil")
	case *PS.Ident:
		fmt.Fprintf(&c.buf, "colVal(%s, %q)", rowVar, e.Name)
	case *PS.QualifiedName:
		fmt.Fprintf(&c.buf, "colVal(%s, %q)", rowVar, e.Table+"."+e.Name)
	case *PS.Param:
		fmt.Fprintf(&c.buf, "%s[%d]", paramsVar, e.Index)
	case *PS.BinaryExpr:
		c.compileBinary(e, rowVar, paramsVar)
	case *PS.UnaryExpr:
		c.compileUnary(e, rowVar, paramsVar)
	case *PS.FunctionCall:
		c.compileFuncCall(e, rowVar, paramsVar)
	case *PS.CastExpr:
		c.compileCast(e, rowVar, paramsVar)
	case *PS.CaseExpr:
		c.compileCase(e, rowVar, paramsVar)
	default:
		c.buf.WriteString("nil")
	}
}

func (c *ExprCompiler) compileBinary(e *PS.BinaryExpr, rowVar, paramsVar string) {
	leftIsCol, rightIsLit := false, false
	if _, ok := e.Left.(*PS.Ident); ok {
		leftIsCol = true
	}
	if _, ok := e.Left.(*PS.QualifiedName); ok {
		leftIsCol = true
	}
	if _, ok := e.Right.(*PS.NumberLiteral); ok {
		rightIsLit = true
	}
	if _, ok := e.Right.(*PS.FloatLiteral); ok {
		rightIsLit = true
	}
	if _, ok := e.Right.(*PS.StringLiteral); ok {
		rightIsLit = true
	}

	opStr := opGo(e.Op)

	c.imports["github.com/cyw0ng95/razordata/internal/SQL/EX"] = true

	if leftIsCol && rightIsLit {
		fmt.Fprintf(&c.buf, "ex.CompareColLit(%s, ", rowVar)
		c.compileExpr(e.Left, rowVar, paramsVar)
		fmt.Fprintf(&c.buf, ", ")
		c.compileExpr(e.Right, rowVar, paramsVar)
		fmt.Fprintf(&c.buf, ", %d)", e.Op)
	} else {
		c.buf.WriteString("func() interface{} { ")
		c.buf.WriteString("l := ")
		c.compileExpr(e.Left, rowVar, paramsVar)
		c.buf.WriteString("; r := ")
		c.compileExpr(e.Right, rowVar, paramsVar)
		fmt.Fprintf(&c.buf, "; return ex.BinaryOp(l, r, %d) }()", e.Op)
	}
	_ = opStr
}

func (c *ExprCompiler) compileUnary(e *PS.UnaryExpr, rowVar, paramsVar string) {
	opStr := opGo(e.Op)
	c.buf.WriteString(opStr)
	c.buf.WriteString("(")
	c.compileExpr(e.Operand, rowVar, paramsVar)
	c.buf.WriteString(")")
}

func (c *ExprCompiler) compileFuncCall(e *PS.FunctionCall, rowVar, paramsVar string) {
	fmt.Fprintf(&c.buf, "callFn(%q, ", e.Name)
	c.buf.WriteString("[]interface{}{")
	for i, arg := range e.Args {
		if i > 0 {
			c.buf.WriteString(", ")
		}
		c.compileExpr(arg, rowVar, paramsVar)
	}
	c.buf.WriteString("})")
}

func (c *ExprCompiler) compileCast(e *PS.CastExpr, rowVar, paramsVar string) {
	fmt.Fprintf(&c.buf, "castTo(")
	c.compileExpr(e.Expr, rowVar, paramsVar)
	fmt.Fprintf(&c.buf, ", %d)", e.Type.Type)
}

func (c *ExprCompiler) compileCase(e *PS.CaseExpr, rowVar, paramsVar string) {
	c.buf.WriteString("evalCase(")
	if e.Expr != nil {
		c.compileExpr(e.Expr, rowVar, paramsVar)
	} else {
		c.buf.WriteString("nil")
	}
	c.buf.WriteString(", []ex.WhenClause{")
	for _, w := range e.WhenList {
		c.buf.WriteString("{Cond: ")
		c.compileExpr(w.Cond, rowVar, paramsVar)
		c.buf.WriteString(", Then: ")
		c.compileExpr(w.Then, rowVar, paramsVar)
		c.buf.WriteString("},")
	}
	c.buf.WriteString("}")
	if e.Else != nil {
		c.buf.WriteString(", ")
		c.compileExpr(e.Else, rowVar, paramsVar)
	} else {
		c.buf.WriteString(", nil")
	}
	c.buf.WriteString(")")
}

func opGo(op int) string {
	switch op {
	case int(LX.T_PLUS):
		return "add"
	case int(LX.T_MINUS):
		return "sub"
	case int(LX.T_STAR):
		return "mul"
	case int(LX.T_SLASH):
		return "div"
	case int(LX.T_EQ):
		return "eq"
	case int(LX.T_NE):
		return "ne"
	case int(LX.T_LT):
		return "lt"
	case int(LX.T_GT):
		return "gt"
	case int(LX.T_LE):
		return "le"
	case int(LX.T_GE):
		return "ge"
	case int(LX.T_AND):
		return "and"
	case int(LX.T_OR):
		return "or"
	case int(LX.T_NOT):
		return "not"
	default:
		return fmt.Sprintf("op%d", op)
	}
}