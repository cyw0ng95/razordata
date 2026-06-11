package PS

import (
	"testing"
)

func TestParse_AlterTable_AddColumn(t *testing.T) {
	input := "ALTER TABLE users ADD COLUMN age INT DEFAULT 0"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	alter, ok := stmt.(*AlterTableStmt)
	if !ok {
		t.Fatalf("expected *AlterTableStmt, got %T", stmt)
	}
	if alter.Table != "users" {
		t.Errorf("Table = %q, want %q", alter.Table, "users")
	}
	if alter.Action != "ADD COLUMN" {
		t.Errorf("Action = %q, want %q", alter.Action, "ADD COLUMN")
	}
	if alter.Column != "age" {
		t.Errorf("Column = %q, want %q", alter.Column, "age")
	}
}

func TestParse_AlterTable_DropColumn(t *testing.T) {
	input := "ALTER TABLE users DROP COLUMN age"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	alter, ok := stmt.(*AlterTableStmt)
	if !ok {
		t.Fatalf("expected *AlterTableStmt, got %T", stmt)
	}
	if alter.Action != "DROP COLUMN" {
		t.Errorf("Action = %q, want %q", alter.Action, "DROP COLUMN")
	}
}

func TestParse_AlterTable_Rename(t *testing.T) {
	input := "ALTER TABLE users RENAME TO people"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	alter, ok := stmt.(*AlterTableStmt)
	if !ok {
		t.Fatalf("expected *AlterTableStmt, got %T", stmt)
	}
	if alter.Action != "RENAME" {
		t.Errorf("Action = %q, want %q", alter.Action, "RENAME")
	}
	if alter.Column != "people" {
		t.Errorf("Column (new name) = %q, want %q", alter.Column, "people")
	}
}
