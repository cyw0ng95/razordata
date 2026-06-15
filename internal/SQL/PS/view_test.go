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
	if view.Temporary {
		t.Error("Temporary should be false for CREATE VIEW")
	}
}

func TestParse_CreateTempView(t *testing.T) {
	inputs := []struct {
		name  string
		input string
	}{
		{"TEMP", "CREATE TEMP VIEW v AS SELECT 1"},
		{"TEMPORARY", "CREATE TEMPORARY VIEW v AS SELECT 1"},
	}
	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(tc.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			view, ok := stmt.(*CreateViewStmt)
			if !ok {
				t.Fatalf("expected *CreateViewStmt, got %T", stmt)
			}
			if view.Name != "v" {
				t.Errorf("Name = %q, want %q", view.Name, "v")
			}
			if !view.Temporary {
				t.Error("Temporary should be true for CREATE TEMP VIEW")
			}
			if view.As == nil {
				t.Fatal("expected non-nil As (SELECT statement)")
			}
		})
	}
}

func TestParse_CreateTempView_TableNotView(t *testing.T) {
	// CREATE TEMP TABLE should not parse as CREATE VIEW
	p := NewParser("CREATE TEMP TABLE t (id INT)")
	_, err := p.Parse()
	if err == nil {
		t.Fatal("expected error for CREATE TEMP TABLE (TEMP TABLE not supported)")
	}
}
