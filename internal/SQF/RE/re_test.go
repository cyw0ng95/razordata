package RE

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestRewriteSelect(t *testing.T) {
	p := PS.NewParser("SELECT * FROM t WHERE a = 1 ORDER BY b LIMIT 10 OFFSET 5")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "SELECT * FROM t WHERE (a = 1) ORDER BY b LIMIT 10 OFFSET 5"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteSelectColumns(t *testing.T) {
	p := PS.NewParser("SELECT a, b, c FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "SELECT a, b, c FROM t"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteInsert(t *testing.T) {
	p := PS.NewParser("INSERT INTO t VALUES (1, 'hello')")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "INSERT INTO t VALUES (1, 'hello')"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteInsertWithColumns(t *testing.T) {
	p := PS.NewParser("INSERT INTO t (a, b) VALUES (1, 'hello')")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "INSERT INTO t (a, b) VALUES (1, 'hello')"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteUpdate(t *testing.T) {
	p := PS.NewParser("UPDATE t SET a = 1 WHERE b = 2")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "UPDATE t SET a = 1 WHERE (b = 2)"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteDelete(t *testing.T) {
	p := PS.NewParser("DELETE FROM t WHERE a = 1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "DELETE FROM t WHERE (a = 1)"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteCreateTable(t *testing.T) {
	p := PS.NewParser("CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT NOT NULL)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT NOT NULL)"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteCreateTableBigint(t *testing.T) {
	p := PS.NewParser("CREATE TABLE t (a BIGINT)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}
	if sql != "CREATE TABLE t (a BIGINT)" {
		t.Errorf("expected BIGINT, got %q", sql)
	}
}

func TestRewriteCreateTableVarchar(t *testing.T) {
	p := PS.NewParser("CREATE TABLE t (a VARCHAR)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}
	if sql != "CREATE TABLE t (a VARCHAR)" {
		t.Errorf("expected VARCHAR, got %q", sql)
	}
}

func TestRewriteCreateTableTimestamp(t *testing.T) {
	p := PS.NewParser("CREATE TABLE t (a TIMESTAMP)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}
	if sql != "CREATE TABLE t (a TIMESTAMP)" {
		t.Errorf("expected TIMESTAMP, got %q", sql)
	}
}

func TestRewriteDropTable(t *testing.T) {
	p := PS.NewParser("DROP TABLE t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "DROP TABLE t"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteExpression(t *testing.T) {
	p := PS.NewParser("SELECT a + b * c FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "SELECT (a + (b * c)) FROM t"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteBinaryExpr(t *testing.T) {
	p := PS.NewParser("SELECT a AND b OR c FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "SELECT ((a AND b) OR c) FROM t"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteParam(t *testing.T) {
	p := PS.NewParser("SELECT ? FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "SELECT ? FROM t"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteStringLiteral(t *testing.T) {
	p := PS.NewParser("SELECT 'hello' FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "SELECT 'hello' FROM t"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestOpString(t *testing.T) {
	tests := []struct {
		op   LX.TokenType
		want string
	}{
		{LX.T_EQ, "="},
		{LX.T_NE, "<>"},
		{LX.T_LT, "<"},
		{LX.T_LE, "<="},
		{LX.T_GT, ">"},
		{LX.T_GE, ">="},
		{LX.T_PLUS, "+"},
		{LX.T_MINUS, "-"},
		{LX.T_STAR, "*"},
		{LX.T_SLASH, "/"},
		{LX.T_AND, "AND"},
		{LX.T_OR, "OR"},
		{LX.T_NOT, "NOT"},
		{LX.T_IN, "IN"},
		{LX.T_BETWEEN, "BETWEEN"},
		{LX.T_LIKE, "LIKE"},
		{LX.T_IS, "IS"},
	}

	for _, tt := range tests {
		got := opString(tt.op)
		if got != tt.want {
			t.Errorf("opString(%v) = %q, want %q", tt.op, got, tt.want)
		}
	}
}

func TestRewriteNull(t *testing.T) {
	p := PS.NewParser("SELECT NULL FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}
	if sql != "SELECT NULL FROM t" {
		t.Errorf("expected NULL, got %q", sql)
	}
}

func TestRewriteBool(t *testing.T) {
	p := PS.NewParser("SELECT TRUE, FALSE FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}
	if sql != "SELECT TRUE, FALSE FROM t" {
		t.Errorf("expected TRUE, FALSE, got %q", sql)
	}
}

func TestRewriteUnaryMinus(t *testing.T) {
	p := PS.NewParser("SELECT -a FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}
	if sql != "SELECT -a FROM t" {
		t.Errorf("expected -a, got %q", sql)
	}
}

func TestRewriteUnaryNot(t *testing.T) {
	p := PS.NewParser("SELECT NOT a FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sql, err := Format(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}
	if sql != "SELECT NOT a FROM t" {
		t.Errorf("expected NOT a, got %q", sql)
	}
}

// TestRewriteCoverageREQ000163 pins REQ000163 branches.
func TestRewriteCoverageREQ000163(t *testing.T) {
	// rewriteInsert
	ins := &PS.Insert{Table: "t", Cols: []string{"a"}, Values: [][]PS.Expr{{&PS.NumberLiteral{Val: 1}}}}
	_, err := Rewrite(ins)
	if err != nil {
		t.Errorf("Rewrite(Insert) failed: %v", err)
	}
	// rewriteUpdate
	upd := &PS.Update{Table: "t", Set: []PS.Pair{{Col: "x", Val: &PS.NumberLiteral{Val: 1}}}}
	_, err = Rewrite(upd)
	if err != nil {
		t.Errorf("Rewrite(Update) failed: %v", err)
	}
	// rewriteDelete
	del := &PS.Delete{Table: "t"}
	_, err = Rewrite(del)
	if err != nil {
		t.Errorf("Rewrite(Delete) failed: %v", err)
	}
	// rewriteCreateTable
	ct := &PS.CreateTable{Name: "t", Cols: []PS.ColDef{{Name: "id", Type: LX.T_INT_KW}}}
	_, err = Rewrite(ct)
	if err != nil {
		t.Errorf("Rewrite(CreateTable) failed: %v", err)
	}
	// rewriteDropTable
	dt := &PS.DropTable{Name: "t"}
	_, err = Rewrite(dt)
	if err != nil {
		t.Errorf("Rewrite(DropTable) failed: %v", err)
	}
}

func TestExprStringCoverageREQ000163(t *testing.T) {
	tests := []struct {
		name string
		expr PS.Expr
	}{
		{"float", &PS.FloatLiteral{Val: 3.14}},
		{"alias", &PS.AliasedExpr{Expr: &PS.Ident{Name: "x"}, Alias: "y"}},
		{"star", &PS.StarExpr{}},
		{"func", &PS.FunctionCall{Name: "ABS", Args: []PS.Expr{&PS.NumberLiteral{Val: 1}}}},
		{"agg", &PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}}},
		{"cast", &PS.CastExpr{Expr: &PS.Ident{Name: "x"}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}}},
		{"list", &PS.ListExpr{Items: []PS.Expr{&PS.NumberLiteral{Val: 1}}}},
		{"between", &PS.BetweenExpr{Expr: &PS.Ident{Name: "x"}, Low: &PS.NumberLiteral{Val: 1}, High: &PS.NumberLiteral{Val: 10}}},
		{"case", &PS.CaseExpr{WhenList: []PS.WhenClause{{Cond: &PS.NumberLiteral{Val: 1}, Then: &PS.StringLiteral{Val: "a"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = exprString(tt.expr)
		})
	}
}

func TestFormatSelectCoverageREQ000163(t *testing.T) {
	sel := &PS.Select{Distinct: true, From: "t", FromAlias: "a", OrderBy: []PS.OrderItem{{Expr: &PS.Ident{Name: "x"}, Desc: true}}, Limit: &PS.NumberLiteral{Val: 10}, Offset: &PS.NumberLiteral{Val: 5}}
	_, err := Format(sel)
	if err != nil {
		t.Errorf("Format(Select) failed: %v", err)
	}
}

func TestRewriteExprCoverageREQ000163(t *testing.T) {
	_, _ = RewriteExpr(nil)
	_, _ = RewriteExpr(&PS.FunctionCall{Name: "F", Args: []PS.Expr{&PS.NumberLiteral{Val: 1}}})
	_, _ = RewriteExpr(&PS.CastExpr{Expr: &PS.NumberLiteral{Val: 1}, Type: &PS.TypeInfo{Type: LX.T_INT_KW}})
}

func TestSplitOrInferredType(t *testing.T) {
	_ = SplitOr(&PS.BinaryExpr{Left: &PS.BinaryExpr{Left: &PS.Ident{Name: "a"}, Op: LX.T_OR, Right: &PS.Ident{Name: "b"}}, Op: LX.T_OR, Right: &PS.Ident{Name: "c"}})
	_ = InferredType(&PS.NumberLiteral{Val: 1})
	_ = InferredType(&PS.FloatLiteral{Val: 1.0})
	_ = InferredType(&PS.StringLiteral{Val: "x"})
	_ = InferredType(&PS.BoolLiteral{Val: true})
}
