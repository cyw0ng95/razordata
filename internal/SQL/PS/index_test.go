package PS

import (
	"testing"
)

func colNames(cols []IndexedColumn) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

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
	if len(ci.IndexedColumns) != 1 || ci.IndexedColumns[0].Name != "email" {
		t.Errorf("IndexedColumns = %v, want [email]", colNames(ci.IndexedColumns))
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
	if len(ci.IndexedColumns) != 3 {
		t.Errorf("IndexedColumns = %v, want 3 entries", colNames(ci.IndexedColumns))
	}
	want := []string{"last", "first", "middle"}
	for i, ic := range ci.IndexedColumns {
		if ic.Name != want[i] {
			t.Errorf("IndexedColumns[%d].Name = %q, want %q", i, ic.Name, want[i])
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

// TestParseCreateIndex_Collate covers CREATE INDEX with COLLATE.
func TestParseCreateIndex_Collate(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON t (a COLLATE nocase, b COLLATE binary)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ci, ok := stmt.(*CreateIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *CreateIndexStmt", stmt)
	}
	if len(ci.IndexedColumns) != 2 {
		t.Fatalf("got %d cols, want 2", len(ci.IndexedColumns))
	}
	if ci.IndexedColumns[0].Collation != "nocase" {
		t.Errorf("col 0 Collation = %q, want nocase", ci.IndexedColumns[0].Collation)
	}
	if ci.IndexedColumns[1].Collation != "binary" {
		t.Errorf("col 1 Collation = %q, want binary", ci.IndexedColumns[1].Collation)
	}
}

// TestParseCreateIndex_CollateFirstOnly covers COLLATE on only the first column.
func TestParseCreateIndex_CollateFirstOnly(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON t (a COLLATE nocase, b)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ci, ok := stmt.(*CreateIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *CreateIndexStmt", stmt)
	}
	if ci.IndexedColumns[0].Collation != "nocase" {
		t.Errorf("col 0 Collation = %q, want nocase", ci.IndexedColumns[0].Collation)
	}
	if ci.IndexedColumns[1].Collation != "" {
		t.Errorf("col 1 Collation = %q, want empty", ci.IndexedColumns[1].Collation)
	}
}
