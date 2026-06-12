package PS

import (
	"testing"
)

func TestParse_CreateTable_InlineFK(t *testing.T) {
	input := "CREATE TABLE orders (id INT, user_id INT REFERENCES users(id))"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if len(ct.Cols) != 2 {
		t.Fatalf("expected 2 cols, got %d", len(ct.Cols))
	}
	col := ct.Cols[1]
	if col.Name != "user_id" {
		t.Errorf("col name = %q, want %q", col.Name, "user_id")
	}
	if col.ReferencesTable != "users" {
		t.Errorf("ReferencesTable = %q, want %q", col.ReferencesTable, "users")
	}
	if col.ReferencesColumn != "id" {
		t.Errorf("ReferencesColumn = %q, want %q", col.ReferencesColumn, "id")
	}
}

func TestParse_CreateTable_InlineFK_OnDeleteCascade(t *testing.T) {
	input := "CREATE TABLE orders (id INT, user_id INT REFERENCES users(id) ON DELETE CASCADE)"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ct := stmt.(*CreateTable)
	col := ct.Cols[1]
	if col.OnDelete != "CASCADE" {
		t.Errorf("OnDelete = %q, want %q", col.OnDelete, "CASCADE")
	}
}

func TestParse_CreateTable_TableLevelFK(t *testing.T) {
	input := "CREATE TABLE orders (id INT, user_id INT, FOREIGN KEY (user_id) REFERENCES users(id))"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if len(ct.ForeignKeys) != 1 {
		t.Fatalf("expected 1 FK, got %d", len(ct.ForeignKeys))
	}
	fk := ct.ForeignKeys[0]
	if len(fk.Columns) != 1 || fk.Columns[0] != "user_id" {
		t.Errorf("FK columns = %v, want [user_id]", fk.Columns)
	}
	if fk.RefTable != "users" {
		t.Errorf("RefTable = %q, want %q", fk.RefTable, "users")
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0] != "id" {
		t.Errorf("RefColumns = %v, want [id]", fk.RefColumns)
	}
}

func TestParse_CreateTable_TableLevelFK_OnDeleteUpdate(t *testing.T) {
	input := "CREATE TABLE orders (id INT, user_id INT, FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE SET NULL)"
	p := NewParser(input)
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ct := stmt.(*CreateTable)
	fk := ct.ForeignKeys[0]
	if fk.OnDelete != "CASCADE" {
		t.Errorf("OnDelete = %q, want %q", fk.OnDelete, "CASCADE")
	}
	if fk.OnUpdate != "SET NULL" {
		t.Errorf("OnUpdate = %q, want %q", fk.OnUpdate, "SET NULL")
	}
}
