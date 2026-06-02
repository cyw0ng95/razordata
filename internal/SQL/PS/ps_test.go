package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

func TestParseSelect(t *testing.T) {
	p := NewParser("SELECT * FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel, ok := stmt.(*Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	if sel.From != "t" {
		t.Errorf("expected From='t', got %q", sel.From)
	}
	if len(sel.Cols) != 1 {
		t.Errorf("expected 1 col, got %d", len(sel.Cols))
	}
}

func TestParseSelectWithColumns(t *testing.T) {
	p := NewParser("SELECT a, b FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	if len(sel.Cols) != 2 {
		t.Errorf("expected 2 cols, got %d", len(sel.Cols))
	}
}

func TestParseSelectWhere(t *testing.T) {
	p := NewParser("SELECT * FROM t WHERE a = 1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	if sel.Where == nil {
		t.Error("expected WHERE clause")
	}
}

func TestParseSelectOrderBy(t *testing.T) {
	p := NewParser("SELECT * FROM t ORDER BY a")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	if sel.OrderBy == nil {
		t.Error("expected ORDER BY clause")
	}
}

func TestParseSelectLimitOffset(t *testing.T) {
	p := NewParser("SELECT * FROM t LIMIT 10 OFFSET 5")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	if sel.Limit == nil {
		t.Error("expected LIMIT clause")
	}
	if sel.Offset == nil {
		t.Error("expected OFFSET clause")
	}
}

func TestParseInsert(t *testing.T) {
	p := NewParser("INSERT INTO t VALUES (1, 'hello')")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	ins, ok := stmt.(*Insert)
	if !ok {
		t.Fatalf("expected *Insert, got %T", stmt)
	}
	if ins.Table != "t" {
		t.Errorf("expected Table='t', got %q", ins.Table)
	}
	if len(ins.Values) != 1 {
		t.Errorf("expected 1 row, got %d", len(ins.Values))
	}
}

func TestParseInsertWithColumns(t *testing.T) {
	p := NewParser("INSERT INTO t (a, b) VALUES (1, 'hello')")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	ins := stmt.(*Insert)
	if len(ins.Cols) != 2 {
		t.Errorf("expected 2 cols, got %d", len(ins.Cols))
	}
}

func TestParseUpdate(t *testing.T) {
	p := NewParser("UPDATE t SET a = 1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	upd, ok := stmt.(*Update)
	if !ok {
		t.Fatalf("expected *Update, got %T", stmt)
	}
	if upd.Table != "t" {
		t.Errorf("expected Table='t', got %q", upd.Table)
	}
}

func TestParseUpdateWhere(t *testing.T) {
	p := NewParser("UPDATE t SET a = 1 WHERE b = 2")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	upd := stmt.(*Update)
	if upd.Where == nil {
		t.Error("expected WHERE clause")
	}
}

func TestParseDelete(t *testing.T) {
	p := NewParser("DELETE FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	del, ok := stmt.(*Delete)
	if !ok {
		t.Fatalf("expected *Delete, got %T", stmt)
	}
	if del.Table != "t" {
		t.Errorf("expected Table='t', got %q", del.Table)
	}
}

func TestParseDeleteWhere(t *testing.T) {
	p := NewParser("DELETE FROM t WHERE a = 1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	del := stmt.(*Delete)
	if del.Where == nil {
		t.Error("expected WHERE clause")
	}
}

func TestParseCreateTable(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT NOT NULL)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if ct.Name != "t" {
		t.Errorf("expected Name='t', got %q", ct.Name)
	}
	if len(ct.Cols) != 2 {
		t.Errorf("expected 2 cols, got %d", len(ct.Cols))
	}
}

func TestParseCreateTableWithPK(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER, b TEXT, PRIMARY KEY (a))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	ct := stmt.(*CreateTable)
	if ct.PK == nil || *ct.PK != "a" {
		t.Errorf("expected PK='a', got %v", ct.PK)
	}
}

func TestParseDropTable(t *testing.T) {
	p := NewParser("DROP TABLE t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	dt, ok := stmt.(*DropTable)
	if !ok {
		t.Fatalf("expected *DropTable, got %T", stmt)
	}
	if dt.Name != "t" {
		t.Errorf("expected Name='t', got %q", dt.Name)
	}
}

func TestParseExpression(t *testing.T) {
	p := NewParser("SELECT a + b FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	bin, ok := sel.Cols[0].(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Cols[0])
	}
	if bin.Op != int(LX.T_PLUS) {
		t.Errorf("expected PLUS op, got %d", bin.Op)
	}
}

func TestParseExpressionPrecedence(t *testing.T) {
	p := NewParser("SELECT a + b * c FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	bin, ok := sel.Cols[0].(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Cols[0])
	}
	if bin.Op != int(LX.T_PLUS) {
		t.Errorf("expected PLUS op, got %d", bin.Op)
	}
}

func TestParseMultipleRows(t *testing.T) {
	p := NewParser("INSERT INTO t VALUES (1, 'a'), (2, 'b')")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	ins := stmt.(*Insert)
	if len(ins.Values) != 2 {
		t.Errorf("expected 2 rows, got %d", len(ins.Values))
	}
}
