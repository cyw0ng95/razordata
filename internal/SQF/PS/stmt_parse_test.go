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