package PS

import (
	"testing"
)

// TestSubqueryFromParsing verifies that parenthesized subqueries
// in the FROM clause parse correctly with alias support.
// REQ000436 + REQ000084.
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

// TestRecursiveCTEParsing verifies that WITH RECURSIVE parses
// the recursive flag and the compound body. REQ000436.
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
