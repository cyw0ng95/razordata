package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// ── EXCLUDED (UPSERT) ───────────────────────────────────────────────

func TestParse_Upsert_ExcludedCol(t *testing.T) {
	input := "INSERT INTO users (id, name) VALUES (1, 'alice') ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	insert, ok := stmt.(*Insert)
	if !ok {
		t.Fatalf("expected *Insert, got %T", stmt)
	}
	if insert.OnConflict == nil {
		t.Fatal("expected ON CONFLICT clause")
	}
	if insert.OnConflict.DoNothing {
		t.Fatal("expected DO UPDATE, not DO NOTHING")
	}
	if len(insert.OnConflict.SetClauses) != 1 {
		t.Fatalf("expected 1 SET clause, got %d", len(insert.OnConflict.SetClauses))
	}
	pair := insert.OnConflict.SetClauses[0]
	if pair.Col != "name" {
		t.Errorf("SET col = %q, want %q", pair.Col, "name")
	}
	qn, ok := pair.Val.(*QualifiedName)
	if !ok {
		t.Fatalf("expected QualifiedName for EXCLUDED.name, got %T", pair.Val)
	}
	if qn.Table != "excluded" || qn.Name != "name" {
		t.Errorf("EXCLUDED.name = {%s, %s}, want {excluded, name}", qn.Table, qn.Name)
	}
}

func TestParse_Upsert_ExcludedColMultiple(t *testing.T) {
	input := "INSERT INTO t (a, b) VALUES (1, 2) ON CONFLICT (a) DO UPDATE SET a = EXCLUDED.a, b = EXCLUDED.b"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	insert := stmt.(*Insert)
	if len(insert.OnConflict.SetClauses) != 2 {
		t.Fatalf("expected 2 SET clauses, got %d", len(insert.OnConflict.SetClauses))
	}
	for i, pair := range insert.OnConflict.SetClauses {
		qn, ok := pair.Val.(*QualifiedName)
		if !ok {
			t.Errorf("clause %d: expected QualifiedName, got %T", i, pair.Val)
			continue
		}
		if qn.Table != "excluded" {
			t.Errorf("clause %d: Table = %q, want excluded", i, qn.Table)
		}
	}
}

// ── SET TRANSACTION ──────────────────────────────────────────────────

func TestParse_SetTransaction(t *testing.T) {
	tests := []struct {
		input string
		level string
	}{
		{"SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED", "READ UNCOMMITTED"},
		{"SET TRANSACTION ISOLATION LEVEL READ COMMITTED", "READ COMMITTED"},
		{"SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "REPEATABLE READ"},
		{"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", "SERIALIZABLE"},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			set, ok := stmt.(*SetTransactionStmt)
			if !ok {
				t.Fatalf("expected *SetTransactionStmt, got %T", stmt)
			}
			if set.Level != tt.level {
				t.Errorf("Level = %q, want %q", set.Level, tt.level)
			}
		})
	}
}

func TestParse_SetTransaction_Invalid(t *testing.T) {
	tests := []string{
		"SET TRANSACTION ISOLATION LEVEL INVALID",
		"SET TRANSACTION ISOLATION LEVEL",
		"SET TRANSACTION",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			p := NewParser(input)
			_, err := p.Parse()
			if err == nil {
				t.Errorf("expected error for %q", input)
			}
		})
	}
}

func TestParse_SetTransaction_Tokens(t *testing.T) {
	input := "SET TRANSACTION ISOLATION LEVEL READ COMMITTED"
	p := NewParser(input)
	p.advance()
	if p.current.Type != LX.T_SET {
		t.Errorf("first token = %v, want T_SET", p.current.Type)
	}
}

// ── SUBQUERY / CTE ───────────────────────────────────────────────────

func TestSubqueryFromParsing(t *testing.T) {
	cases := []struct {
		sql       string
		wantAlias string
	}{
		{"SELECT a FROM (SELECT 1 AS a) AS sub", "sub"},
		{"SELECT sub.a FROM (SELECT 1 AS a, 2 AS b) AS sub WHERE sub.a > 0", "sub"},
		{"SELECT * FROM (SELECT 1 UNION ALL SELECT 2) AS sub", "sub"},
	}
	for _, c := range cases {
		parser := NewParser(c.sql)
		stmt, err := parser.Parse()
		if err != nil {
			t.Errorf("SQL %q: parse error: %v", c.sql, err)
			continue
		}
		sel, ok := stmt.(*Select)
		if !ok {
			t.Errorf("SQL %q: expected *Select, got %T", c.sql, stmt)
			continue
		}
		if sel.SubqueryFrom == nil {
			t.Errorf("SQL %q: SubqueryFrom is nil", c.sql)
			continue
		}
		if sel.From != c.wantAlias {
			t.Errorf("SQL %q: From=%q, want %q", c.sql, sel.From, c.wantAlias)
		}
	}
}

func TestRecursiveCTEParsing(t *testing.T) {
	sql := "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x<5) SELECT x FROM cnt"
	parser := NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	with, ok := stmt.(*WithStmt)
	if !ok {
		t.Fatalf("expected *WithStmt, got %T", stmt)
	}
	if !with.Recursive {
		t.Error("expected Recursive=true")
	}
	if len(with.CTEs) != 1 {
		t.Fatalf("expected 1 CTE, got %d", len(with.CTEs))
	}
	if with.CTEs[0].Name != "cnt" {
		t.Errorf("CTE name: %q", with.CTEs[0].Name)
	}
}

// ── JOIN USING (REQ001361) ──────────────────────────────────────────────

func TestJoinUsing_Parses(t *testing.T) {
	input := "SELECT * FROM t1 JOIN t2 USING (id)"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel, ok := stmt.(*Select)
	if !ok {
		t.Fatalf("expected *Select, got %T", stmt)
	}
	if len(sel.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(sel.Joins))
	}
	j := sel.Joins[0]
	if len(j.Using) != 1 || j.Using[0] != "id" {
		t.Fatalf("Using = %v, want [id]", j.Using)
	}
	if j.On != nil {
		t.Errorf("On should be nil when USING is present, got %+v", j.On)
	}
}

func TestJoinUsing_MultiCol(t *testing.T) {
	input := "SELECT * FROM t1 INNER JOIN t2 USING (a, b, c)"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*Select)
	if len(sel.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(sel.Joins))
	}
	j := sel.Joins[0]
	if len(j.Using) != 3 || j.Using[0] != "a" || j.Using[1] != "b" || j.Using[2] != "c" {
		t.Fatalf("Using = %v, want [a b c]", j.Using)
	}
	if j.Kind != "INNER" {
		t.Errorf("Kind = %q, want INNER", j.Kind)
	}
}

func TestJoinUsing_LeftJoin(t *testing.T) {
	input := "SELECT a, b FROM t1 LEFT JOIN t2 USING (id)"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*Select)
	if len(sel.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(sel.Joins))
	}
	j := sel.Joins[0]
	if j.Kind != "LEFT" {
		t.Errorf("Kind = %q, want LEFT", j.Kind)
	}
	if len(j.Using) != 1 || j.Using[0] != "id" {
		t.Fatalf("Using = %v, want [id]", j.Using)
	}
}

func TestREQ000736_NullsFirstLast(t *testing.T) {
	tests := []struct {
		sql       string
		wantOrder int8
	}{
		{"SELECT * FROM t ORDER BY col NULLS FIRST", 1},
		{"SELECT * FROM t ORDER BY col NULLS LAST", -1},
		{"SELECT * FROM t ORDER BY col ASC NULLS FIRST", 1},
		{"SELECT * FROM t ORDER BY col DESC NULLS LAST", -1},
		{"SELECT * FROM t ORDER BY col ASC", 0},
		{"SELECT * FROM t ORDER BY col", 0},
		{"SELECT * FROM t ORDER BY col ASC NULLS LAST", -1},
	}
	for _, tc := range tests {
		_ = tc
		p := NewParser(tc.sql)
		stmt, err := p.Parse()
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.sql, err)
			continue
		}
		sel, ok := stmt.(*Select)
		if !ok {
			t.Errorf("%q: not a Select", tc.sql)
			_ = sel
			continue
		}
		if len(sel.OrderBy) != 1 {
			t.Errorf("%q: expected 1 OrderBy, got %d", tc.sql, len(sel.OrderBy))
			_ = sel
			_ = ok
			continue
		}
		if sel.OrderBy[0].NullsOrder != tc.wantOrder {
			t.Errorf("%q: NullsOrder=%d, want %d", tc.sql, sel.OrderBy[0].NullsOrder, tc.wantOrder)
		}
	}
}

func TestREQ000737_CreateVirtualTable(t *testing.T) {
	tests := []struct {
		sql        string
		wantName   string
		wantModule string
		wantArgs   int
	}{
		{"CREATE VIRTUAL TABLE t USING fts5(content)", "t", "fts5", 1},
		{"CREATE VIRTUAL TABLE search USING fts5(content, title)", "search", "fts5", 2},
		{"CREATE VIRTUAL TABLE geo USING rtree(id, x1, x2, y1, y2)", "geo", "rtree", 5},
	}
	for _, tc := range tests {
		_ = tc
		p := NewParser(tc.sql)
		stmt, err := p.Parse()
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.sql, err)
			continue
		}
		v, ok := stmt.(*CreateVirtualTableStmt)
		if !ok {
			t.Errorf("%q: got %T, want *CreateVirtualTableStmt", tc.sql, stmt)
			_ = v
			continue
		}
		if v.Name != tc.wantName {
			t.Errorf("%q: Name=%q, want %q", tc.sql, v.Name, tc.wantName)
		}
		if v.Module != tc.wantModule {
			t.Errorf("%q: Module=%q, want %q", tc.sql, v.Module, tc.wantModule)
		}
		if len(v.Args) != tc.wantArgs {
			t.Errorf("%q: %d args, want %d: %v", tc.sql, len(v.Args), tc.wantArgs, v.Args)
		}
	}
}

func TestReq001193_DivParsing(t *testing.T) {
	tests := []struct {
		sql      string
		wantOp   LX.TokenType
	}{
		{"SELECT 20 DIV 4", LX.T_DIV},
		{"SELECT 20 DIV - - 96", LX.T_DIV},
		{"SELECT 100 DIV 3", LX.T_DIV},
	}
	for _, tt := range tests {
		_ = tt
		t.Run(tt.sql, func(t *testing.T) {
			p := NewParser(tt.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse %q: %v", tt.sql, err)
			}
			sel, ok := stmt.(*Select)
			if !ok {
				t.Fatalf("expected Select, got %T", stmt)
			}
			if len(sel.Cols) != 1 {
				t.Fatalf("expected 1 col, got %d", len(sel.Cols))
			}
			be, ok := sel.Cols[0].(*BinaryExpr)
			if !ok {
				t.Fatalf("expected BinaryExpr, got %T", sel.Cols[0])
			}
			if be.Op != tt.wantOp {
				t.Errorf("op = %v, want %v", be.Op, tt.wantOp)
			}
		})
	}
}

// ── NATURAL JOIN (REQ001359) ─────────────────────────────────────────

func TestNaturalJoin_Inner_Parses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sql      string
		wantKind string
	}{
		{"natural_join", "SELECT * FROM t1 NATURAL JOIN t2", "INNER"},
		{"natural_left", "SELECT * FROM t1 NATURAL LEFT JOIN t2", "LEFT"},
		{"natural_right", "SELECT * FROM t1 NATURAL RIGHT JOIN t2", "RIGHT"},
		{"natural_left_outer", "SELECT * FROM t1 NATURAL LEFT OUTER JOIN t2", "LEFT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(tc.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			sel := stmt.(*Select)
			if len(sel.Joins) != 1 {
				t.Fatalf("expected 1 join, got %d", len(sel.Joins))
			}
			j := sel.Joins[0]
			if !j.Natural {
				t.Fatal("expected Natural=true")
			}
			if j.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", j.Kind, tc.wantKind)
			}
			if j.On != nil {
				t.Errorf("On should be nil (planner synthesizes), got %+v", j.On)
			}
		})
	}
}

func TestNaturalJoin_NoCommonCols_Cross(t *testing.T) {
	p := NewParser("SELECT * FROM t1 NATURAL JOIN t2")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := stmt.(*Select)
	j := sel.Joins[0]
	if !j.Natural {
		t.Fatal("expected Natural=true")
	}
}