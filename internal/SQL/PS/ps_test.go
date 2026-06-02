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
	if len(sel.OrderBy) == 0 {
		t.Error("expected ORDER BY clause")
	}
}

func TestParseSelectOrderByDesc(t *testing.T) {
	p := NewParser("SELECT * FROM t ORDER BY a DESC")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel := stmt.(*Select)
	if len(sel.OrderBy) != 1 {
		t.Fatalf("expected 1 order item, got %d", len(sel.OrderBy))
	}
	if !sel.OrderBy[0].Desc {
		t.Error("expected DESC flag")
	}
}

func TestParseSelectOrderByMultiKey(t *testing.T) {
	p := NewParser("SELECT * FROM t ORDER BY a, b DESC")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel := stmt.(*Select)
	if len(sel.OrderBy) != 2 {
		t.Fatalf("expected 2 order items, got %d", len(sel.OrderBy))
	}
	if sel.OrderBy[0].Desc {
		t.Error("expected first key ASC")
	}
	if !sel.OrderBy[1].Desc {
		t.Error("expected second key DESC")
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

func TestParseCreateTableColumnUnique(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER UNIQUE NOT NULL)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(ct.Cols))
	}
	if !ct.Cols[0].Unique {
		t.Errorf("expected col[0].Unique=true")
	}
	if ct.Cols[0].Nullable {
		t.Errorf("expected col[0].Nullable=false")
	}
}

func TestParseCreateTableUniqueNotNull(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER NOT NULL UNIQUE)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(ct.Cols))
	}
	if !ct.Cols[0].Unique {
		t.Errorf("expected col[0].Unique=true")
	}
	if ct.Cols[0].Nullable {
		t.Errorf("expected col[0].Nullable=false")
	}
}

func TestParseCreateTableTableLevelUnique(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER, b TEXT, UNIQUE (a))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 2 {
		t.Fatalf("expected 2 cols, got %d", len(ct.Cols))
	}
}

func TestParseCreateTableTableLevelUniqueKey(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER, b TEXT, UNIQUE KEY (a))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 2 {
		t.Fatalf("expected 2 cols, got %d", len(ct.Cols))
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

func TestParseFloatLiteral(t *testing.T) {
	p := NewParser("SELECT 3.14 FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	lit, ok := sel.Cols[0].(*FloatLiteral)
	if !ok {
		t.Fatalf("expected *FloatLiteral, got %T", sel.Cols[0])
	}
	if lit.Val != 3.14 {
		t.Errorf("expected 3.14, got %f", lit.Val)
	}
}

func TestParseNegativeFloat(t *testing.T) {
	p := NewParser("SELECT -2.5 FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	unary, ok := sel.Cols[0].(*UnaryExpr)
	if !ok {
		t.Fatalf("expected *UnaryExpr, got %T", sel.Cols[0])
	}
	lit, ok := unary.Operand.(*FloatLiteral)
	if !ok {
		t.Fatalf("expected *FloatLiteral, got %T", unary.Operand)
	}
	if lit.Val != 2.5 {
		t.Errorf("expected 2.5, got %f", lit.Val)
	}
}

func TestParseUnaryMinus(t *testing.T) {
	p := NewParser("SELECT -a FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	unary, ok := sel.Cols[0].(*UnaryExpr)
	if !ok {
		t.Fatalf("expected *UnaryExpr, got %T", sel.Cols[0])
	}
	if unary.Op != int(LX.T_MINUS) {
		t.Errorf("expected MINUS op, got %d", unary.Op)
	}
}

func TestParseUnaryPlus(t *testing.T) {
	p := NewParser("SELECT +a FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	unary, ok := sel.Cols[0].(*UnaryExpr)
	if !ok {
		t.Fatalf("expected *UnaryExpr, got %T", sel.Cols[0])
	}
	if unary.Op != int(LX.T_PLUS) {
		t.Errorf("expected PLUS op, got %d", unary.Op)
	}
}

func TestParseNotExpression(t *testing.T) {
	p := NewParser("SELECT NOT a FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	unary, ok := sel.Cols[0].(*UnaryExpr)
	if !ok {
		t.Fatalf("expected *UnaryExpr, got %T", sel.Cols[0])
	}
	if unary.Op != int(LX.T_NOT) {
		t.Errorf("expected NOT op, got %d", unary.Op)
	}
}

func TestParseStringLiteral(t *testing.T) {
	p := NewParser("SELECT 'hello' FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	lit, ok := sel.Cols[0].(*StringLiteral)
	if !ok {
		t.Fatalf("expected *StringLiteral, got %T", sel.Cols[0])
	}
	if lit.Val != "hello" {
		t.Errorf("expected 'hello', got %q", lit.Val)
	}
}

func TestParseNullLiteral(t *testing.T) {
	p := NewParser("SELECT NULL FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	_, ok := sel.Cols[0].(*NullLiteral)
	if !ok {
		t.Fatalf("expected *NullLiteral, got %T", sel.Cols[0])
	}
}

func TestParseParam(t *testing.T) {
	p := NewParser("SELECT ? FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	_, ok := sel.Cols[0].(*Param)
	if !ok {
		t.Fatalf("expected *Param, got %T", sel.Cols[0])
	}
}

func TestParseBinaryExpr(t *testing.T) {
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
		t.Errorf("expected PLUS, got %d", bin.Op)
	}
}

func TestParseComplexExpression(t *testing.T) {
	p := NewParser("SELECT a + b * c - d / e FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	sel := stmt.(*Select)
	bin, ok := sel.Cols[0].(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Cols[0])
	}
	if bin.Op != int(LX.T_MINUS) {
		t.Errorf("expected MINUS at top level, got %d", bin.Op)
	}
}
