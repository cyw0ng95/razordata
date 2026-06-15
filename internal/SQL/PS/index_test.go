package PS

import (
	"testing"
)

// TestParseCreateIndex_Basic covers the simplest CREATE INDEX.
func TestParseCreateIndex_Basic(t *testing.T) {
	p := NewParser("CREATE INDEX idx_email ON users (email)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ci, ok := stmt.(*CreateIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *CreateIndexStmt", stmt)
	}
	if ci.Name != "idx_email" {
		t.Errorf("Name = %q, want idx_email", ci.Name)
	}
	if ci.Table != "users" {
		t.Errorf("Table = %q, want users", ci.Table)
	}
	if len(ci.Columns) != 1 || ci.Columns[0] != "email" {
		t.Errorf("Columns = %v, want [email]", ci.Columns)
	}
	if ci.Unique {
		t.Errorf("Unique should be false")
	}
}

// TestParseCreateIndex_Unique covers the UNIQUE modifier.
func TestParseCreateIndex_Unique(t *testing.T) {
	p := NewParser("CREATE UNIQUE INDEX idx_email ON users (email)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ci, ok := stmt.(*CreateIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *CreateIndexStmt", stmt)
	}
	if !ci.Unique {
		t.Errorf("Unique should be true")
	}
}

// TestParseCreateIndex_MultiColumn covers multi-column indexes.
func TestParseCreateIndex_MultiColumn(t *testing.T) {
	p := NewParser("CREATE INDEX idx_name ON users (last, first, middle)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ci, ok := stmt.(*CreateIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *CreateIndexStmt", stmt)
	}
	if len(ci.Columns) != 3 {
		t.Errorf("Columns = %v, want 3 entries", ci.Columns)
	}
	want := []string{"last", "first", "middle"}
	for i, c := range ci.Columns {
		if c != want[i] {
			t.Errorf("Columns[%d] = %q, want %q", i, c, want[i])
		}
	}
}

// TestParseCreateIndex_MissingParen rejects missing opening paren.
func TestParseCreateIndex_MissingParen(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON users email)")
	_, err := p.Parse()
	if err == nil {
		t.Errorf("expected error for missing paren")
	}
}

// TestParseCreateIndex_MissingIdent rejects missing column name.
func TestParseCreateIndex_MissingIdent(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON users (")
	_, err := p.Parse()
	if err == nil {
		t.Errorf("expected error for missing column name")
	}
}

// TestParseDropIndex_Basic covers simple DROP INDEX.
func TestParseDropIndex_Basic(t *testing.T) {
	p := NewParser("DROP INDEX idx_email")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	di, ok := stmt.(*DropIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *DropIndexStmt", stmt)
	}
	if di.Name != "idx_email" {
		t.Errorf("Name = %q, want idx_email", di.Name)
	}
}

// TestParseDropIndex_MissingName rejects DROP INDEX without name.
func TestParseDropIndex_MissingName(t *testing.T) {
	p := NewParser("DROP INDEX")
	_, err := p.Parse()
	if err == nil {
		t.Errorf("expected error for missing index name")
	}
}

// TestParseCreateTable_StillWorks ensures CREATE TABLE isn't broken
// by the disambiguation peek.
func TestParseCreateTable_StillWorks(t *testing.T) {
	p := NewParser("CREATE TABLE users (id INT)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := stmt.(*CreateTable); !ok {
		t.Errorf("got %T, want *CreateTable", stmt)
	}
}

// TestParseDropTable_StillWorks ensures DROP TABLE isn't broken.
func TestParseDropTable_StillWorks(t *testing.T) {
	p := NewParser("DROP TABLE users")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := stmt.(*DropTable); !ok {
		t.Errorf("got %T, want *DropTable", stmt)
	}
}
