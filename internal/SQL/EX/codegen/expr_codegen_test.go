package codegen

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestExprCompiler_NumberLiteral(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.NumberLiteral{Val: 42}
	code := c.Compile(expr, "row", "params")
	if code != "int64(42)" {
		t.Errorf("expected int64(42), got %s", code)
	}
}

func TestExprCompiler_FloatLiteral(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.FloatLiteral{Val: 3.14}
	code := c.Compile(expr, "row", "params")
	if code != "3.14" {
		t.Errorf("expected 3.14, got %s", code)
	}
}

func TestExprCompiler_StringLiteral(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.StringLiteral{Val: "hello"}
	code := c.Compile(expr, "row", "params")
	if code != `"hello"` {
		t.Errorf("expected \"hello\", got %s", code)
	}
}

func TestExprCompiler_BoolLiteralTrue(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BoolLiteral{Val: true}
	code := c.Compile(expr, "row", "params")
	if code != "true" {
		t.Errorf("expected true, got %s", code)
	}
}

func TestExprCompiler_BoolLiteralFalse(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BoolLiteral{Val: false}
	code := c.Compile(expr, "row", "params")
	if code != "false" {
		t.Errorf("expected false, got %s", code)
	}
}

func TestExprCompiler_NullLiteral(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.NullLiteral{}
	code := c.Compile(expr, "row", "params")
	if code != "nil" {
		t.Errorf("expected nil, got %s", code)
	}
}

func TestExprCompiler_Ident(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.Ident{Name: "x"}
	code := c.Compile(expr, "row", "params")
	expected := `colVal(row, "x")`
	if code != expected {
		t.Errorf("expected %s, got %s", expected, code)
	}
}

func TestExprCompiler_QualifiedName(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.QualifiedName{Table: "t1", Name: "col"}
	code := c.Compile(expr, "row", "params")
	expected := `colVal(row, "t1.col")`
	if code != expected {
		t.Errorf("expected %s, got %s", expected, code)
	}
}

func TestExprCompiler_Param(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.Param{Index: 5}
	code := c.Compile(expr, "row", "params")
	if code != "params[5]" {
		t.Errorf("expected params[5], got %s", code)
	}
}

func TestExprCompiler_BinaryColEqLit(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "age"},
		Right: &PS.NumberLiteral{Val: 18},
		Op:    int(LX.T_GE),
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_BinaryExpr(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BinaryExpr{
		Left:  &PS.NumberLiteral{Val: 10},
		Right: &PS.NumberLiteral{Val: 20},
		Op:    int(LX.T_PLUS),
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_UnaryNot(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.UnaryExpr{
		Op:      int(LX.T_NOT),
		Operand: &PS.Ident{Name: "active"},
	}
	code := c.Compile(expr, "row", "params")
	expected := `not(colVal(row, "active"))`
	if code != expected {
		t.Errorf("expected %s, got %s", expected, code)
	}
}

func TestExprCompiler_UnaryMinus(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.UnaryExpr{
		Op:      int(LX.T_MINUS),
		Operand: &PS.Ident{Name: "x"},
	}
	code := c.Compile(expr, "row", "params")
	expected := `sub(colVal(row, "x"))`
	if code != expected {
		t.Errorf("expected %s, got %s", expected, code)
	}
}

func TestExprCompiler_FunctionCall(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.FunctionCall{
		Name: "abs",
		Args: []PS.Expr{&PS.Ident{Name: "x"}},
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_FunctionCallMultiArg(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.FunctionCall{
		Name: "coalesce",
		Args: []PS.Expr{
			&PS.Ident{Name: "a"},
			&PS.Ident{Name: "b"},
			&PS.NumberLiteral{Val: 0},
		},
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_Cast(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.CastExpr{
		Expr: &PS.Ident{Name: "x"},
		Type: &PS.TypeInfo{Type: int(LX.T_INT_KW)},
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_CaseSimple(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.CaseExpr{
		Expr: &PS.Ident{Name: "x"},
		WhenList: []PS.WhenClause{
			{Cond: &PS.NumberLiteral{Val: 1}, Then: &PS.StringLiteral{Val: "one"}},
		},
		Else: &PS.StringLiteral{Val: "other"},
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_CaseNoElse(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.CaseExpr{
		WhenList: []PS.WhenClause{
			{Cond: &PS.BinaryExpr{Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 0}, Op: int(LX.T_GT)}, Then: &PS.NumberLiteral{Val: 1}},
		},
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_ColEqStringLit(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BinaryExpr{
		Left:  &PS.QualifiedName{Table: "t", Name: "name"},
		Right: &PS.StringLiteral{Val: "alice"},
		Op:    int(LX.T_EQ),
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_NilExpr(t *testing.T) {
	c := NewExprCompiler()
	code := c.Compile(nil, "row", "params")
	if code != "nil" {
		t.Errorf("expected nil, got %s", code)
	}
}

func TestExprCompiler_IdentRowVar(t *testing.T) {
	c := NewExprCompiler()
	code := c.Compile(&PS.Ident{Name: "val"}, "r", "p")
	if code != `colVal(r, "val")` {
		t.Errorf("expected colVal(r, \"val\"), got %s", code)
	}
}

func TestExprCompiler_IdentCustomVar(t *testing.T) {
	c := NewExprCompiler()
	code := c.Compile(&PS.Ident{Name: "score"}, "myRow", "myParams")
	if code != `colVal(myRow, "score")` {
		t.Errorf("expected colVal(myRow, \"score\"), got %s", code)
	}
}

func TestExprCompiler_ColEqFloat(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "ratio"},
		Right: &PS.FloatLiteral{Val: 0.5},
		Op:    int(LX.T_GT),
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_ColEqCol(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.Ident{Name: "y"},
		Op:    int(LX.T_EQ),
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_AndOrChain(t *testing.T) {
	c := NewExprCompiler()
	left := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
		Op:    int(LX.T_EQ),
	}
	right := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "b"},
		Right: &PS.NumberLiteral{Val: 2},
		Op:    int(LX.T_EQ),
	}
	and := &PS.BinaryExpr{
		Left:  left,
		Right: right,
		Op:    int(LX.T_AND),
	}
	code := c.Compile(and, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_DeeplyNested(t *testing.T) {
	c := NewExprCompiler()
	// ((a + b) * (c - d)) / (e + f)
	aPlusB := &PS.BinaryExpr{Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}, Op: int(LX.T_PLUS)}
	cMinusD := &PS.BinaryExpr{Left: &PS.Ident{Name: "c"}, Right: &PS.Ident{Name: "d"}, Op: int(LX.T_MINUS)}
	ePlusF := &PS.BinaryExpr{Left: &PS.Ident{Name: "e"}, Right: &PS.Ident{Name: "f"}, Op: int(LX.T_PLUS)}
	mul := &PS.BinaryExpr{Left: aPlusB, Right: cMinusD, Op: int(LX.T_STAR)}
	div := &PS.BinaryExpr{Left: mul, Right: ePlusF, Op: int(LX.T_SLASH)}
	code := c.Compile(div, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_Imports(t *testing.T) {
	c := NewExprCompiler()
	_ = c.Compile(&PS.Ident{Name: "x"}, "row", "params")
	if len(c.imports) > 0 {
		t.Logf("imports tracked: %d", len(c.imports))
	}
}

func TestOpGo_AllOps(t *testing.T) {
	ops := map[int]string{
		int(LX.T_PLUS):  "add",
		int(LX.T_MINUS): "sub",
		int(LX.T_STAR):  "mul",
		int(LX.T_SLASH): "div",
		int(LX.T_EQ):    "eq",
		int(LX.T_NE):    "ne",
		int(LX.T_LT):    "lt",
		int(LX.T_GT):    "gt",
		int(LX.T_LE):    "le",
		int(LX.T_GE):    "ge",
		int(LX.T_AND):   "and",
		int(LX.T_OR):    "or",
		int(LX.T_NOT):   "not",
	}
	for op, expected := range ops {
		got := opGo(op)
		if got != expected {
			t.Errorf("opGo(%d) = %s, want %s", op, got, expected)
		}
	}
}

func TestOpGo_Unknown(t *testing.T) {
	got := opGo(9999)
	if got != "op9999" {
		t.Errorf("expected op9999, got %s", got)
	}
}

func TestExprCompiler_Reuse(t *testing.T) {
	c := NewExprCompiler()
	code1 := c.Compile(&PS.NumberLiteral{Val: 1}, "r", "p")
	code2 := c.Compile(&PS.NumberLiteral{Val: 2}, "r", "p")
	if code1 != "int64(1)" || code2 != "int64(2)" {
		t.Errorf("code1=%s code2=%s", code1, code2)
	}
}

func TestExprCompiler_ColEqFloatLit(t *testing.T) {
	c := NewExprCompiler()
	expr := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "price"},
		Right: &PS.FloatLiteral{Val: 9.99},
		Op:    int(LX.T_LE),
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}

func TestExprCompiler_NamedParam(t *testing.T) {
	c := NewExprCompiler()
	code := c.Compile(&PS.Param{Index: 0}, "row", "params")
	if code != "params[0]" {
		t.Errorf("expected params[0], got %s", code)
	}
}

func TestExprCompiler_ParamLastIndex(t *testing.T) {
	c := NewExprCompiler()
	code := c.Compile(&PS.Param{Index: 99}, "row", "params")
	if code != "params[99]" {
		t.Errorf("expected params[99], got %s", code)
	}
}

func TestExprCompiler_CaseMultipleWhen(t *testing.T) {
	c := NewExprCompiler()
	when1 := PS.WhenClause{
		Cond: &PS.BinaryExpr{Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 1}, Op: int(LX.T_EQ)},
		Then: &PS.StringLiteral{Val: "one"},
	}
	when2 := PS.WhenClause{
		Cond: &PS.BinaryExpr{Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 2}, Op: int(LX.T_EQ)},
		Then: &PS.StringLiteral{Val: "two"},
	}
	expr := &PS.CaseExpr{
		WhenList: []PS.WhenClause{when1, when2},
		Else:     &PS.NumberLiteral{Val: 0},
	}
	code := c.Compile(expr, "row", "params")
	if code == "" {
		t.Error("expected non-empty code")
	}
}
