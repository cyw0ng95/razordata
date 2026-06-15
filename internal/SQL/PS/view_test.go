package PS

import (
	"testing"
)

func TestParse_CreateView(t *testing.T) {
	input := "CREATE VIEW active_users AS SELECT id, name FROM users WHERE active = 1"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	view, ok := stmt.(*CreateViewStmt)
	if !ok {
		t.Fatalf("expected *CreateViewStmt, got %T", stmt)
	}
	if view.Name != "active_users" {
		t.Errorf("Name = %q, want %q", view.Name, "active_users")
	}
	if view.As == nil {
		t.Fatal("expected non-nil As (SELECT statement)")
	}
	sel, ok := view.As.(*Select)
	if !ok {
		t.Fatalf("As should be *Select, got %T", view.As)
	}
	if sel.From != "users" {
		t.Errorf("From = %q, want %q", sel.From, "users")
	}
}
