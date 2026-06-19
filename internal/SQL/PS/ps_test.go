package PS

import (
	"errors"
	"strings"
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

func TestParseTrueFalse(t *testing.T) {
	cases := []struct {
		sql     string
		want    bool
		negated bool
	}{
		{"SELECT TRUE FROM t", true, false},
		{"SELECT FALSE FROM t", false, false},
		{"SELECT NOT TRUE FROM t", true, true},
		{"SELECT NOT FALSE FROM t", false, true},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			p := NewParser(c.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse() failed: %v", err)
			}
			sel := stmt.(*Select)
			var u *UnaryExpr
			var b *BoolLiteral
			if u, _ = sel.Cols[0].(*UnaryExpr); u != nil {
				b, _ = u.Operand.(*BoolLiteral)
			} else {
				b, _ = sel.Cols[0].(*BoolLiteral)
			}
			if b == nil {
				t.Fatalf("expected BoolLiteral under Cols[0], got %T", sel.Cols[0])
			}
			hasNot := u != nil
			if hasNot != c.negated {
				t.Errorf("NOT wrapper mismatch: negated=%v got=%v", c.negated, hasNot)
			}
			if b.Val != c.want {
				t.Errorf("got %v, want %v", b.Val, c.want)
			}
		})
	}
}

func TestParseSelectAliasNonIdent(t *testing.T) {
	p := NewParser("SELECT a + 1 AS x FROM t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel := stmt.(*Select)
	a, ok := sel.Cols[0].(*AliasedExpr)
	if !ok {
		t.Fatalf("expected AliasedExpr, got %T", sel.Cols[0])
	}
	if a.Alias != "x" {
		t.Errorf("expected alias x, got %q", a.Alias)
	}
	if _, ok := a.Expr.(*BinaryExpr); !ok {
		t.Errorf("expected wrapped BinaryExpr, got %T", a.Expr)
	}
}

func TestParseSelectAliasRejectsKeyword(t *testing.T) {
	p := NewParser("SELECT 1 AS 5 FROM t")
	if _, err := p.Parse(); err == nil {
		t.Error("expected error for numeric alias, got nil")
	}
}

func TestParseBetweenRequiresAnd(t *testing.T) {
	p := NewParser("SELECT * FROM t WHERE a BETWEEN 1 AND 10")
	if _, err := p.Parse(); err != nil {
		t.Errorf("expected parse ok, got %v", err)
	}
}

func TestParseBetweenWithExpression(t *testing.T) {
	p := NewParser("SELECT * FROM t WHERE a BETWEEN 1 + 1 AND 10 * 2")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel := stmt.(*Select)
	b, ok := sel.Where.(*BetweenExpr)
	if !ok {
		t.Fatalf("expected BetweenExpr, got %T", sel.Where)
	}
	if _, ok := b.Low.(*BinaryExpr); !ok {
		t.Errorf("expected BinaryExpr in Low, got %T", b.Low)
	}
}

func TestParseParamIndex(t *testing.T) {
	cases := []struct {
		sql  string
		want []int
	}{
		{"SELECT ? FROM t", []int{0}},
		{"SELECT ?, ? FROM t", []int{0, 1}},
		{"SELECT ?, ?, ? FROM t", []int{0, 1, 2}},
		{"SELECT a + ?, b * ? FROM t", []int{0, 1}},
		{"INSERT INTO t VALUES (?, ?, ?)", []int{0, 1, 2}},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			p := NewParser(c.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse() failed: %v", err)
			}
			var got []int
			switch s := stmt.(type) {
			case *Select:
				for _, e := range s.Cols {
					collectParams(e, &got)
				}
			case *Insert:
				for _, row := range s.Values {
					for _, e := range row {
						collectParams(e, &got)
					}
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("idx %d: got %d, want %d", i, got[i], c.want[i])
				}
			}
		})
	}
}

func collectParams(e Expr, out *[]int) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *Param:
		*out = append(*out, v.Index)
	case *BinaryExpr:
		collectParams(v.Left, out)
		collectParams(v.Right, out)
	case *UnaryExpr:
		collectParams(v.Operand, out)
	case *AliasedExpr:
		collectParams(v.Expr, out)
	}
}

func TestParseSyntaxErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{"bad_keyword", "SELECT * FROB t", "got identifier"},
		{"unterminated_at_eof", "SELECT", "got EOF"},
		{"missing_rparen", "SELECT COUNT( FROM t", "got FROM"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewParser(c.sql)
			_, err := p.Parse()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrSyntax) {
				t.Errorf("expected ErrSyntax, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error message missing %q: %s", c.want, err.Error())
			}
			if !strings.Contains(err.Error(), "^") {
				t.Errorf("error message missing caret: %s", err.Error())
			}
		})
	}
}

func TestParseCast(t *testing.T) {
	cases := []struct {
		sql      string
		wantType int
	}{
		{"SELECT CAST(x AS INTEGER) FROM t", int(LX.T_INT_KW)},
		{"SELECT CAST(x AS FLOAT) FROM t", int(LX.T_FLOAT_KW)},
		{"SELECT CAST(x AS TEXT) FROM t", int(LX.T_TEXT)},
		{"SELECT CAST(x AS BOOLEAN) FROM t", int(LX.T_BOOL)},
		{"SELECT CAST(x AS BIGINT) FROM t", int(LX.T_BIGINT)},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			p := NewParser(c.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse() failed: %v", err)
			}
			sel := stmt.(*Select)
			cast, ok := sel.Cols[0].(*CastExpr)
			if !ok {
				t.Fatalf("expected CastExpr, got %T", sel.Cols[0])
			}
			if cast.Type == nil {
				t.Fatal("Type should not be nil")
			}
			if cast.Type.Type != c.wantType {
				t.Errorf("got type %d, want %d", cast.Type.Type, c.wantType)
			}
		})
	}
}

func TestParseInList(t *testing.T) {
	p := NewParser("SELECT * FROM t WHERE a IN (1, 2, 3)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel := stmt.(*Select)
	in, ok := sel.Where.(*InExpr)
	if !ok {
		t.Fatalf("expected InExpr, got %T", sel.Where)
	}
	if len(in.List) != 3 {
		t.Errorf("expected 3 items, got %d", len(in.List))
	}
}

func TestParseInSubquery(t *testing.T) {
	p := NewParser("SELECT * FROM t WHERE id IN (SELECT id FROM users WHERE age > 30)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel := stmt.(*Select)
	in, ok := sel.Where.(*InExpr)
	if !ok {
		t.Fatalf("expected InExpr, got %T", sel.Where)
	}
	if in.Subquery == nil {
		t.Fatal("expected Subquery to be set")
	}
	sub, ok := in.Subquery.(*Select)
	if !ok {
		t.Fatalf("expected subquery *Select, got %T", in.Subquery)
	}
	if sub.From != "users" {
		t.Errorf("expected subquery from 'users', got %q", sub.From)
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

func TestParseInsertOrReplace(t *testing.T) {
	p := NewParser("INSERT OR REPLACE INTO t VALUES (1, 'hello')")
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
	if ins.ConflictAction != ConflictActionReplace {
		t.Errorf("expected ConflictAction=Replace, got %d", ins.ConflictAction)
	}
}

func TestParseInsertOrIgnore(t *testing.T) {
	p := NewParser("INSERT OR IGNORE INTO t VALUES (1)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionIgnore {
		t.Errorf("expected ConflictAction=Ignore, got %d", ins.ConflictAction)
	}
}

func TestParseInsertOrAbort(t *testing.T) {
	p := NewParser("INSERT OR ABORT INTO t VALUES (1)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionAbort {
		t.Errorf("expected ConflictAction=Abort, got %d", ins.ConflictAction)
	}
}

func TestParseInsertOrRollback(t *testing.T) {
	p := NewParser("INSERT OR ROLLBACK INTO t VALUES (1)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionRollback {
		t.Errorf("expected ConflictAction=Rollback, got %d", ins.ConflictAction)
	}
}

func TestParseInsertOrFail(t *testing.T) {
	p := NewParser("INSERT OR FAIL INTO t VALUES (1)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionFail {
		t.Errorf("expected ConflictAction=Fail, got %d", ins.ConflictAction)
	}
}

func TestParseInsertOrInvalidAction(t *testing.T) {
	p := NewParser("INSERT OR INVALID INTO t VALUES (1)")
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error for invalid INSERT OR action")
	}
}

func TestParseReplaceInto(t *testing.T) {
	p := NewParser("REPLACE INTO t VALUES (1, 'hello')")
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
	if ins.ConflictAction != ConflictActionReplace {
		t.Errorf("expected ConflictAction=Replace, got %d", ins.ConflictAction)
	}
	if len(ins.Values) != 1 {
		t.Errorf("expected 1 row, got %d", len(ins.Values))
	}
}

func TestParseReplaceIntoWithCols(t *testing.T) {
	p := NewParser("REPLACE INTO t (a, b) VALUES (1, 'hello')")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ins := stmt.(*Insert)
	if len(ins.Cols) != 2 {
		t.Errorf("expected 2 cols, got %d", len(ins.Cols))
	}
	if ins.ConflictAction != ConflictActionReplace {
		t.Errorf("expected ConflictAction=Replace, got %d", ins.ConflictAction)
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

// TestParseCreateTableDefaultLiteral covers DEFAULT <int literal>.
func TestParseCreateTableDefaultLiteral(t *testing.T) {
	p := NewParser("CREATE TABLE t (id INTEGER NOT NULL, n INTEGER DEFAULT 0)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 2 {
		t.Fatalf("expected 2 cols, got %d", len(ct.Cols))
	}
	if ct.Cols[0].Default != nil {
		t.Errorf("col[0] should have no DEFAULT, got %v", ct.Cols[0].Default)
	}
	if ct.Cols[1].Default == nil {
		t.Fatal("col[1] should have a DEFAULT expression")
	}
	num, ok := ct.Cols[1].Default.(*NumberLiteral)
	if !ok {
		t.Fatalf("expected NumberLiteral, got %T", ct.Cols[1].Default)
	}
	if num.Val != 0 {
		t.Errorf("expected DEFAULT 0, got %d", num.Val)
	}
}

// TestParseCreateTableDefaultString covers DEFAULT <string literal>.
func TestParseCreateTableDefaultString(t *testing.T) {
	p := NewParser(`CREATE TABLE t (id INTEGER NOT NULL, name TEXT DEFAULT 'unknown')`)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if ct.Cols[1].Default == nil {
		t.Fatal("col[1] should have a DEFAULT expression")
	}
	str, ok := ct.Cols[1].Default.(*StringLiteral)
	if !ok {
		t.Fatalf("expected StringLiteral, got %T", ct.Cols[1].Default)
	}
	if str.Val != "unknown" {
		t.Errorf("expected DEFAULT 'unknown', got %q", str.Val)
	}
}

// TestParseCreateTableDefaultNull covers DEFAULT NULL on a nullable column.
func TestParseCreateTableDefaultNull(t *testing.T) {
	p := NewParser("CREATE TABLE t (id INTEGER NOT NULL, v INTEGER DEFAULT NULL)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if ct.Cols[1].Default == nil {
		t.Fatal("col[1] should have a DEFAULT expression")
	}
	if _, ok := ct.Cols[1].Default.(*NullLiteral); !ok {
		t.Errorf("expected NullLiteral, got %T", ct.Cols[1].Default)
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
	if len(ct.UniqueConstraints) != 1 {
		t.Fatalf("expected 1 unique constraint, got %d", len(ct.UniqueConstraints))
	}
	if len(ct.UniqueConstraints[0].Cols) != 1 || ct.UniqueConstraints[0].Cols[0] != "a" {
		t.Errorf("expected UNIQUE (a), got %v", ct.UniqueConstraints[0].Cols)
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
	if len(ct.UniqueConstraints) != 1 || ct.UniqueConstraints[0].Cols[0] != "a" {
		t.Errorf("UNIQUE KEY should produce a constraint on 'a'")
	}
}

// TestParseCreateTableCompositeUnique: UNIQUE (a, b) parses as a
// 2-column unique constraint.
func TestParseCreateTableCompositeUnique(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER, b TEXT, c INTEGER, UNIQUE (a, b))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.UniqueConstraints) != 1 {
		t.Fatalf("expected 1 unique constraint, got %d", len(ct.UniqueConstraints))
	}
	cols := ct.UniqueConstraints[0].Cols
	if len(cols) != 2 || cols[0] != "a" || cols[1] != "b" {
		t.Errorf("expected UNIQUE (a, b), got %v", cols)
	}
}

// TestParseCreateTableMultipleUniques: two UNIQUE clauses produce
// two constraints.
func TestParseCreateTableMultipleUniques(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INTEGER, b TEXT, c INTEGER, UNIQUE (a), UNIQUE (b, c))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.UniqueConstraints) != 2 {
		t.Fatalf("expected 2 unique constraints, got %d", len(ct.UniqueConstraints))
	}
	if len(ct.UniqueConstraints[0].Cols) != 1 || ct.UniqueConstraints[0].Cols[0] != "a" {
		t.Errorf("first should be UNIQUE (a), got %v", ct.UniqueConstraints[0].Cols)
	}
	if len(ct.UniqueConstraints[1].Cols) != 2 {
		t.Errorf("second should be UNIQUE (b, c), got %v", ct.UniqueConstraints[1].Cols)
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

func TestParseAnalyze(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  *AnalyzeStmt
	}{
		{"no_table", "ANALYZE", &AnalyzeStmt{Table: ""}},
		{"with_table", "ANALYZE users", &AnalyzeStmt{Table: "users"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewParser(c.input).Parse()
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", c.input, err)
			}
			stmt, ok := got.(*AnalyzeStmt)
			if !ok {
				t.Fatalf("expected *AnalyzeStmt, got %T", got)
			}
			if stmt.Table != c.want.Table {
				t.Errorf("Table: got %q, want %q", stmt.Table, c.want.Table)
			}
		})
	}
}

func TestParseVacuum(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  *VacuumStmt
	}{
		{"no_table", "VACUUM", &VacuumStmt{Table: ""}},
		{"with_table", "VACUUM users", &VacuumStmt{Table: "users"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewParser(c.input).Parse()
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", c.input, err)
			}
			stmt, ok := got.(*VacuumStmt)
			if !ok {
				t.Fatalf("expected *VacuumStmt, got %T", got)
			}
			if stmt.Table != c.want.Table {
				t.Errorf("Table: got %q, want %q", stmt.Table, c.want.Table)
			}
		})
	}
}

func TestParsePragma(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  *PragmaStmt
	}{
		{"read", "PRAGMA integrity_check", &PragmaStmt{Name: "integrity_check", Value: ""}},
		{"write", "PRAGMA cache_size = 1000", &PragmaStmt{Name: "cache_size", Value: "1000"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewParser(c.input).Parse()
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", c.input, err)
			}
			stmt, ok := got.(*PragmaStmt)
			if !ok {
				t.Fatalf("expected *PragmaStmt, got %T", got)
			}
			if stmt.Name != c.want.Name {
				t.Errorf("Name: got %q, want %q", stmt.Name, c.want.Name)
			}
			if stmt.Value != c.want.Value {
				t.Errorf("Value: got %q, want %q", stmt.Value, c.want.Value)
			}
		})
	}
}

func TestParseInsertDefaultValues(t *testing.T) {
	p := NewParser("INSERT INTO t DEFAULT VALUES")
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
	if !ins.DefaultValues {
		t.Error("expected DefaultValues=true")
	}
	if len(ins.Values) != 0 {
		t.Errorf("expected 0 rows, got %d", len(ins.Values))
	}
}

func TestParseInsertDefaultValuesReturning(t *testing.T) {
	p := NewParser("INSERT INTO t DEFAULT VALUES RETURNING *")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ins, ok := stmt.(*Insert)
	if !ok {
		t.Fatalf("expected *Insert, got %T", stmt)
	}
	if !ins.DefaultValues {
		t.Error("expected DefaultValues=true")
	}
	if len(ins.Returning) == 0 {
		t.Error("expected RETURNING clauses")
	}
}

// REQ000567: LIKE ... ESCAPE
func TestParseLikeEscape(t *testing.T) {
	p := NewParser("SELECT * FROM t WHERE v LIKE '100%' ESCAPE '\\'")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel, ok := stmt.(*Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	be, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	if be.Op != int(LX.T_LIKE) {
		t.Errorf("expected LIKE op, got %d", be.Op)
	}
	if be.Escape == nil {
		t.Fatal("expected Escape expr")
	}
	lit, ok := be.Escape.(*StringLiteral)
	if !ok {
		t.Fatalf("expected *StringLiteral, got %T", be.Escape)
	}
	if lit.Val != "\\" {
		t.Errorf("expected escape char '\\', got %q", lit.Val)
	}
}

func TestParseLikeEscapeNot(t *testing.T) {
	p := NewParser("SELECT * FROM t WHERE v NOT LIKE '100%' ESCAPE '\\'")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel, ok := stmt.(*Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	ue, ok := sel.Where.(*UnaryExpr)
	if !ok {
		t.Fatalf("expected *UnaryExpr, got %T", sel.Where)
	}
	be, ok := ue.Operand.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ue.Operand)
	}
	if be.Op != int(LX.T_LIKE) {
		t.Errorf("expected LIKE op, got %d", be.Op)
	}
	if be.Escape == nil {
		t.Fatal("expected Escape expr")
	}
}

// REQ000570: COMMIT / END [TRANSACTION] parser
func TestParseCommit(t *testing.T) {
	p := NewParser("COMMIT")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	if _, ok := stmt.(*CommitTX); !ok {
		t.Fatalf("expected *CommitTX, got %T", stmt)
	}
}

func TestParseCommitTransaction(t *testing.T) {
	p := NewParser("COMMIT TRANSACTION")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	if _, ok := stmt.(*CommitTX); !ok {
		t.Fatalf("expected *CommitTX, got %T", stmt)
	}
}

func TestParseEndAsCommit(t *testing.T) {
	p := NewParser("END")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	if _, ok := stmt.(*CommitTX); !ok {
		t.Fatalf("expected *CommitTX, got %T", stmt)
	}
}

func TestParseEndTransactionAsCommit(t *testing.T) {
	p := NewParser("END TRANSACTION")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	if _, ok := stmt.(*CommitTX); !ok {
		t.Fatalf("expected *CommitTX, got %T", stmt)
	}
}

// REQ000529: INDEXED BY / NOT INDEXED in SELECT
func TestParseSelectIndexedBy(t *testing.T) {
	p := NewParser("SELECT * FROM t INDEXED BY idx1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel, ok := stmt.(*Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	if sel.IndexHint == nil {
		t.Fatal("expected IndexHint")
	}
	if sel.IndexHint.IndexedBy != "idx1" {
		t.Errorf("expected IndexedBy='idx1', got %q", sel.IndexHint.IndexedBy)
	}
}

func TestParseSelectNotIndexed(t *testing.T) {
	p := NewParser("SELECT * FROM t NOT INDEXED")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	sel, ok := stmt.(*Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	if sel.IndexHint == nil {
		t.Fatal("expected IndexHint")
	}
	if sel.IndexHint.IndexedBy != "" {
		t.Errorf("expected empty IndexedBy for NOT INDEXED, got %q", sel.IndexHint.IndexedBy)
	}
}

// REQ000569: INDEXED BY in UPDATE/DELETE
func TestParseUpdateIndexedBy(t *testing.T) {
	p := NewParser("UPDATE t INDEXED BY idx1 SET v = 1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	upd, ok := stmt.(*Update)
	if !ok {
		t.Fatalf("expected *Update, got %T", stmt)
	}
	if upd.IndexHint == nil {
		t.Fatal("expected IndexHint on Update")
	}
	if upd.IndexHint.IndexedBy != "idx1" {
		t.Errorf("expected IndexedBy='idx1', got %q", upd.IndexHint.IndexedBy)
	}
}

func TestParseDeleteNotIndexed(t *testing.T) {
	p := NewParser("DELETE FROM t NOT INDEXED WHERE id = 1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	del, ok := stmt.(*Delete)
	if !ok {
		t.Fatalf("expected *Delete, got %T", stmt)
	}
	if del.IndexHint == nil {
		t.Fatal("expected IndexHint on Delete")
	}
	if del.IndexHint.IndexedBy != "" {
		t.Errorf("expected empty IndexedBy for NOT INDEXED, got %q", del.IndexHint.IndexedBy)
	}
}

// REQ000561: FK MATCH / DEFERRABLE parser tests
func TestParseFKMatch(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INT, FOREIGN KEY (a) REFERENCES r (b) MATCH FULL)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if len(ct.ForeignKeys) != 1 {
		t.Fatalf("expected 1 FK, got %d", len(ct.ForeignKeys))
	}
	if ct.ForeignKeys[0].Match != "FULL" {
		t.Errorf("expected Match='FULL', got %q", ct.ForeignKeys[0].Match)
	}
}

func TestParseFKDeferrable(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INT, FOREIGN KEY (a) REFERENCES r (b) DEFERRABLE INITIALLY DEFERRED)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if len(ct.ForeignKeys) != 1 {
		t.Fatalf("expected 1 FK, got %d", len(ct.ForeignKeys))
	}
	if ct.ForeignKeys[0].Deferrable != "DEFERRABLE" {
		t.Errorf("expected Deferrable='DEFERRABLE', got %q", ct.ForeignKeys[0].Deferrable)
	}
	if ct.ForeignKeys[0].Initially != "DEFERRED" {
		t.Errorf("expected Initially='DEFERRED', got %q", ct.ForeignKeys[0].Initially)
	}
}

func TestParseFKNotDeferrable(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INT, FOREIGN KEY (a) REFERENCES r (b) NOT DEFERRABLE)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if ct.ForeignKeys[0].Deferrable != "NOT DEFERRABLE" {
		t.Errorf("expected Deferrable='NOT DEFERRABLE', got %q", ct.ForeignKeys[0].Deferrable)
	}
}

func TestParseFKColumnLevelMatch(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INT REFERENCES r (b) MATCH PARTIAL)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if len(ct.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(ct.Cols))
	}
	if ct.Cols[0].Match != "PARTIAL" {
		t.Errorf("expected Match='PARTIAL', got %q", ct.Cols[0].Match)
	}
}

func TestParseFKColumnLevelDeferrable(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INT REFERENCES r (b) DEFERRABLE INITIALLY IMMEDIATE)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if ct.Cols[0].Deferrable != "DEFERRABLE" {
		t.Errorf("expected Deferrable='DEFERRABLE', got %q", ct.Cols[0].Deferrable)
	}
	if ct.Cols[0].Initially != "IMMEDIATE" {
		t.Errorf("expected Initially='IMMEDIATE', got %q", ct.Cols[0].Initially)
	}
}

func TestParseFKMatchSimple(t *testing.T) {
	p := NewParser("CREATE TABLE t (a INT, FOREIGN KEY (a) REFERENCES r (b) MATCH SIMPLE ON DELETE CASCADE)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if ct.ForeignKeys[0].Match != "SIMPLE" {
		t.Errorf("expected Match='SIMPLE', got %q", ct.ForeignKeys[0].Match)
	}
	if ct.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Errorf("expected OnDelete='CASCADE', got %q", ct.ForeignKeys[0].OnDelete)
	}
}
