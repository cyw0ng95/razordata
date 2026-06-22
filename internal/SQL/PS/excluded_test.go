package PS

import (
	"testing"
)

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
