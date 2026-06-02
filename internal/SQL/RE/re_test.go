package RE

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestRewriteSelect(t *testing.T) {
	p := PS.NewParser("SELECT * FROM t WHERE a = 1 ORDER BY b LIMIT 10 OFFSET 5")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite() failed: %v", err)
	}

	expected := "CREATE TABLE t (a INTEGER PRIMARY KEY NOT NULL, b TEXT NOT NULL)"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestRewriteDropTable(t *testing.T) {
	p := PS.NewParser("DROP TABLE t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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

	sql, err := Rewrite(stmt)
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
		op      LX.TokenType
		want    string
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
		got := opString(int(tt.op))
		if got != tt.want {
			t.Errorf("opString(%v) = %q, want %q", tt.op, got, tt.want)
		}
	}
}
