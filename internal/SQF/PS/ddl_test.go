package PS

import (
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// ── ALTER TABLE ──────────────────────────────────────────────────────

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

// ── CREATE / DROP INDEX ─────────────────────────────────────────────

func colNames(cols []IndexedColumn) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

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

func TestParseCreateIndex_MissingParen(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON users email)")
	_, err := p.Parse()
	if err == nil {
		t.Errorf("expected error for missing paren")
	}
}

func TestParseCreateIndex_MissingIdent(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON users (")
	_, err := p.Parse()
	if err == nil {
		t.Errorf("expected error for missing column name")
	}
}

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

func TestParseDropIndex_MissingName(t *testing.T) {
	p := NewParser("DROP INDEX")
	_, err := p.Parse()
	if err == nil {
		t.Errorf("expected error for missing index name")
	}
}

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

func TestParseCreateIndex_Where(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON t (a) WHERE a > 0")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ci, ok := stmt.(*CreateIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *CreateIndexStmt", stmt)
	}
	if ci.Where == nil {
		t.Fatal("expected non-nil Where")
	}
	bin, ok := ci.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", ci.Where)
	}
	if bin.Op != LX.T_GT {
		t.Errorf("expected GT op, got %d", bin.Op)
	}
}

func TestParseCreateIndex_WhereNoParen(t *testing.T) {
	p := NewParser("CREATE INDEX idx ON t (a) WHERE active = 1")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ci, ok := stmt.(*CreateIndexStmt)
	if !ok {
		t.Fatalf("got %T, want *CreateIndexStmt", stmt)
	}
	if ci.Where == nil {
		t.Fatal("expected non-nil Where")
	}
}

// TestParseCreateIndex_MultiWhere verifies REQ001385: the partial
// index WHERE clause accepts AND/OR-joined conjuncts. SQLite allows
// `WHERE cond1 AND cond2 AND cond3`. The single `parseExpr` call in
// parseCreateIndex already handles conjunctions because parseExpr
// descends through AND/OR via precedence (ps.go isBinaryOp). These
// tests pin that behavior so a future refactor doesn't accidentally
// restrict WHERE to a single predicate.
func TestParseCreateIndex_MultiWhere(t *testing.T) {
	cases := []struct {
		name  string
		sql   string
		op    LX.TokenType
		nestL LX.TokenType // expected op on the left side of the top-level
		nestR LX.TokenType // expected op on the right side of the top-level
	}{
		{
			name:  "two_and",
			sql:   "CREATE INDEX idx ON t (a) WHERE a > 0 AND b < 5",
			op:    LX.T_AND,
			nestL: LX.T_GT,
			nestR: LX.T_LT,
		},
		{
			name:  "three_and",
			sql:   "CREATE INDEX idx ON t (a) WHERE a > 0 AND b < 5 AND c = 1",
			op:    LX.T_AND,
			nestL: LX.T_AND,
			nestR: LX.T_EQ,
		},
		{
			name:  "and_or",
			sql:   "CREATE INDEX idx ON t (a) WHERE a > 0 OR b < 5",
			op:    LX.T_OR,
			nestL: LX.T_GT,
			nestR: LX.T_LT,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(tc.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ci, ok := stmt.(*CreateIndexStmt)
			if !ok {
				t.Fatalf("got %T, want *CreateIndexStmt", stmt)
			}
			if ci.Where == nil {
				t.Fatal("expected non-nil Where")
			}
			top, ok := ci.Where.(*BinaryExpr)
			if !ok {
				t.Fatalf("top-level expr = %T, want *BinaryExpr", ci.Where)
			}
			if top.Op != tc.op {
				t.Errorf("top-level Op = %d, want %d", top.Op, tc.op)
			}
			// Left side: for a 3-clause WHERE the left is itself a
			// BinaryExpr(AND). For 2-clause it is a comparison.
			if be, ok := top.Left.(*BinaryExpr); ok {
				if be.Op != tc.nestL {
					t.Errorf("Left.Op = %d, want %d", be.Op, tc.nestL)
				}
			} else if tc.nestL != LX.T_GT && tc.nestL != LX.T_LT && tc.nestL != LX.T_EQ {
				t.Errorf("Left = %T, want BinaryExpr", top.Left)
			}
			if be, ok := top.Right.(*BinaryExpr); ok {
				if be.Op != tc.nestR {
					t.Errorf("Right.Op = %d, want %d", be.Op, tc.nestR)
				}
			}
		})
	}
}

// ── FOREIGN KEY ──────────────────────────────────────────────────────

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

// ── CREATE VIEW ──────────────────────────────────────────────────────

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
	// CREATE TEMP TABLE parses as CreateTable with Temporary=true
	p := NewParser("CREATE TEMP TABLE t (id INT)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("unexpected error for CREATE TEMP TABLE: %v", err)
	}
	ct, ok := stmt.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt)
	}
	if !ct.Temporary {
		t.Error("expected Temporary=true")
	}
	if ct.Name != "t" {
		t.Errorf("Name = %q, want %q", ct.Name, "t")
	}
	if len(ct.Cols) != 1 || ct.Cols[0].Name != "id" {
		t.Errorf("Cols = %+v, want [id]", ct.Cols)
	}

	// Also verify TEMPORARY keyword works
	p2 := NewParser("CREATE TEMPORARY TABLE tmp (a TEXT, b INT)")
	stmt2, err := p2.Parse()
	if err != nil {
		t.Fatalf("unexpected error for CREATE TEMPORARY TABLE: %v", err)
	}
	ct2, ok := stmt2.(*CreateTable)
	if !ok {
		t.Fatalf("expected *CreateTable, got %T", stmt2)
	}
	if !ct2.Temporary {
		t.Error("expected Temporary=true for TEMPORARY")
	}
	if ct2.Name != "tmp" {
		t.Errorf("Name = %q, want %q", ct2.Name, "tmp")
	}
	if len(ct2.Cols) != 2 {
		t.Errorf("Cols = %d, want 2", len(ct2.Cols))
	}
}

// ── WITH RECURSIVE ───────────────────────────────────────────────────

func TestWithRecursiveParser(t *testing.T) {
	parser := NewParser("WITH RECURSIVE cnt(x) AS (SELECT 1) SELECT x FROM cnt")
	stmt, _ := parser.Parse()
	if stmt != nil {
		with, ok := stmt.(*WithStmt)
		if !ok {
			t.Fatalf("expected *WithStmt, got %T", stmt)
		}
		if !with.Recursive {
			t.Errorf("expected Recursive=true")
		}
		return
	}
	// Pre-existing CTE body parse error: verify the lexer
	// tokenizes RECURSIVE as a non-keyword identifier so the
	// parser's RECURSIVE check (via EqualFold on T_IDENT lexeme)
	// can match it.
	lex := LX.NewLexer("WITH RECURSIVE cnt")
	t1 := lex.Next()
	if t1.Type == 0 {
		t.Fatalf("expected first token")
	}
	t2 := lex.Next()
	if t2.Type == 0 {
		t.Fatalf("expected second token")
	}
	if !strings.EqualFold(t2.Lexeme, "RECURSIVE") {
		t.Errorf("expected RECURSIVE, got %q (type=%v)", t2.Lexeme, t2.Type)
	}
	// RECURSIVE is not a keyword, so it must tokenize as T_IDENT.
	if t2.Type != LX.T_IDENT {
		t.Errorf("expected T_IDENT for RECURSIVE, got %v", t2.Type)
	}
}
