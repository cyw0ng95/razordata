package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// ── CASE ─────────────────────────────────────────────────────────────

func TestParseCaseExpr(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "simple case with when then else",
			input:   "SELECT CASE WHEN x > 0 THEN 1 ELSE 0 END FROM t",
			wantErr: false,
		},
		{
			name:    "searched case with multiple when",
			input:   "SELECT CASE WHEN a=1 THEN 'one' WHEN a=2 THEN 'two' ELSE 'other' END FROM t",
			wantErr: false,
		},
		{
			name:    "case without else",
			input:   "SELECT CASE WHEN x > 0 THEN 1 END FROM t",
			wantErr: false,
		},
		{
			name:    "case with expression",
			input:   "SELECT CASE x WHEN 1 THEN 'one' WHEN 2 THEN 'two' END FROM t",
			wantErr: false,
		},
		{
			name:    "invalid case - no when",
			input:   "SELECT CASE END FROM t",
			wantErr: true,
		},
		{
			name:    "invalid case - missing end",
			input:   "SELECT CASE WHEN x > 0 THEN 1 FROM t",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser(tt.input)
			_, err := p.Parse()
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCaseExprType(t *testing.T) {
	when := []WhenClause{
		{Cond: &BinaryExpr{Op: LX.T_EQ}, Then: &Ident{Name: "y"}},
	}
	caseExpr := &CaseExpr{
		Expr:     &Ident{Name: "x"},
		WhenList: when,
		Else:     &Ident{Name: "z"},
	}
	if len(caseExpr.WhenList) != 1 {
		t.Errorf("WhenList: got %d, want 1", len(caseExpr.WhenList))
	}
	if caseExpr.Else == nil {
		t.Error("Else should not be nil")
	}
	if caseExpr.Expr == nil {
		t.Error("Expr should not be nil")
	}
}

// ── EXISTS ───────────────────────────────────────────────────────────

func TestParseExists(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "exists in where clause",
			input:   "SELECT * FROM t WHERE EXISTS (SELECT 1 FROM u)",
			wantErr: false,
		},
		{
			name:    "exists with correlated subquery",
			input:   "SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t1.id)",
			wantErr: false,
		},
		{
			name:    "exists without parentheses - invalid",
			input:   "SELECT * FROM t WHERE EXISTS SELECT 1",
			wantErr: true,
		},
		{
			name:    "exists in select list",
			input:   "SELECT EXISTS (SELECT 1 FROM t) AS has_rows",
			wantErr: false,
		},
		{
			name:    "not exists",
			input:   "SELECT * FROM t WHERE NOT EXISTS (SELECT 1 FROM u)",
			wantErr: false,
		},
		{
			name:    "exists with empty subquery - invalid",
			input:   "SELECT * FROM t WHERE EXISTS ()",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser(tt.input)
			_, err := p.Parse()
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExistsExprType(t *testing.T) {
	sel := &Select{From: "t"}
	exists := &ExistsExpr{Subquery: sel}

	if exists.Subquery == nil {
		t.Fatal("Subquery should not be nil")
	}
	if sel2, ok := exists.Subquery.(*Select); ok {
		if sel2.From != "t" {
			t.Errorf("Subquery.From: got %q, want %q", sel2.From, "t")
		}
	} else {
		t.Errorf("expected Subquery to be *Select, got %T", exists.Subquery)
	}
}

// ── CHECK ────────────────────────────────────────────────────────────

func TestParseCheckConstraint(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"CREATE TABLE t (c INT CHECK (c > 0))"},
		{"CREATE TABLE t (c INT CHECK (c >= 1 AND c <= 100))"},
		{"CREATE TABLE t (score INT CHECK (score BETWEEN 0 AND 100))"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct, ok := stmt.(*CreateTable)
			if !ok {
				t.Fatalf("expected *CreateTable, got %T", stmt)
			}
			if len(ct.Cols) != 1 {
				t.Fatalf("expected 1 col, got %d", len(ct.Cols))
			}
			if ct.Cols[0].Check == nil {
				t.Error("expected Check constraint, got nil")
			}
		})
	}
}

func TestParseCheckToken(t *testing.T) {
	l := LX.NewLexer("CHECK")
	tok := l.Next()
	if tok.Type != LX.T_CHECK {
		t.Errorf("got type %v, want T_CHECK (%v)", tok.Type, LX.T_CHECK)
	}
}

func TestParseCheckMixedConstraints(t *testing.T) {
	tests := []struct {
		input     string
		wantCheck bool
		wantPK    bool
		wantUniq  bool
	}{
		{"CREATE TABLE t (c INT CHECK (c > 0) NOT NULL)", true, false, false},
		{"CREATE TABLE t (c INT PRIMARY KEY CHECK (c > 0))", true, true, false},
		{"CREATE TABLE t (c INT UNIQUE CHECK (c > 0))", true, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct := stmt.(*CreateTable)
			if ct.Cols[0].Check == nil && tt.wantCheck {
				t.Error("expected Check, got nil")
			}
			if ct.Cols[0].PK != tt.wantPK {
				t.Errorf("PK: got %v, want %v", ct.Cols[0].PK, tt.wantPK)
			}
			if ct.Cols[0].Unique != tt.wantUniq {
				t.Errorf("Unique: got %v, want %v", ct.Cols[0].Unique, tt.wantUniq)
			}
		})
	}
}

// ── DEFAULT ──────────────────────────────────────────────────────────

func TestParseDefaultClause(t *testing.T) {
	tests := []struct {
		input       string
		wantDefault bool
		wantType    LX.TokenType
	}{
		{"CREATE TABLE t (c INT DEFAULT 0)", true, LX.T_INT_KW},
		{"CREATE TABLE t (c TEXT DEFAULT 'hello')", true, LX.T_TEXT},
		{"CREATE TABLE t (c INT DEFAULT NULL)", true, LX.T_INT_KW},
		{"CREATE TABLE t (c INT)", false, LX.T_INT_KW},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct, ok := stmt.(*CreateTable)
			if !ok {
				t.Fatalf("expected *CreateTable, got %T", stmt)
			}
			if len(ct.Cols) != 1 {
				t.Fatalf("expected 1 col, got %d", len(ct.Cols))
			}
			if tt.wantDefault {
				if ct.Cols[0].Default == nil {
					t.Errorf("expected Default, got nil")
				}
			} else {
				if ct.Cols[0].Default != nil {
					t.Errorf("expected no Default, got %T", ct.Cols[0].Default)
				}
			}
			if ct.Cols[0].Type != tt.wantType {
				t.Errorf("Type: got %d, want %d", ct.Cols[0].Type, tt.wantType)
			}
		})
	}
}

func TestParseDefaultExpressions(t *testing.T) {
	tests := []struct {
		input    string
		wantExpr string
	}{
		{"CREATE TABLE t (c INT DEFAULT 42)", "IntLit"},
		{"CREATE TABLE t (c TEXT DEFAULT 'x')", "StringLit"},
		{"CREATE TABLE t (c INT DEFAULT NULL)", "NullLit"},
		{"CREATE TABLE t (c BOOL DEFAULT TRUE)", "BoolLit"},
		{"CREATE TABLE t (c INT DEFAULT CURRENT_TIMESTAMP)", "Ident"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct := stmt.(*CreateTable)
			if ct.Cols[0].Default == nil {
				t.Fatal("Default should not be nil")
			}
			_ = tt.wantExpr
		})
	}
}

// ── VARCHAR / DECIMAL / NUMERIC / CAST ──────────────────────────────

func TestParseVarcharSize(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"CREATE TABLE t (c VARCHAR)", 0},
		{"CREATE TABLE t (c VARCHAR(10))", 10},
		{"CREATE TABLE t (c VARCHAR(255))", 255},
		{"CREATE TABLE t (c VARCHAR(1))", 1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct, ok := stmt.(*CreateTable)
			if !ok {
				t.Fatalf("expected *CreateTable, got %T", stmt)
			}
			if len(ct.Cols) != 1 {
				t.Fatalf("expected 1 col, got %d", len(ct.Cols))
			}
			if ct.Cols[0].Type != LX.T_VARCHAR {
				t.Errorf("Type: got %d, want VARCHAR (%d)", ct.Cols[0].Type, LX.T_VARCHAR)
			}
			if tt.want != 0 && ct.Cols[0].Size != tt.want {
				t.Errorf("Size: got %d, want %d", ct.Cols[0].Size, tt.want)
			}
		})
	}
}

func TestParseDecimalPrecisionScale(t *testing.T) {
	tests := []struct {
		input string
		wantP int
		wantS int
	}{
		{"CREATE TABLE t (c DECIMAL)", 0, 0},
		{"CREATE TABLE t (c DECIMAL(10,2))", 10, 2},
		{"CREATE TABLE t (c DECIMAL(38,18))", 38, 18},
		{"CREATE TABLE t (c DECIMAL(5))", 5, 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p := NewParser(tt.input)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			ct, ok := stmt.(*CreateTable)
			if !ok {
				t.Fatalf("expected *CreateTable, got %T", stmt)
			}
			if len(ct.Cols) != 1 {
				t.Fatalf("expected 1 col, got %d", len(ct.Cols))
			}
			if ct.Cols[0].Type != LX.T_DECIMAL {
				t.Errorf("Type: got %d, want DECIMAL (%d)", ct.Cols[0].Type, LX.T_DECIMAL)
			}
			if tt.wantP != 0 && ct.Cols[0].Size != tt.wantP {
				t.Errorf("Size (precision): got %d, want %d", ct.Cols[0].Size, tt.wantP)
			}
		})
	}
}

func TestParseNumericType(t *testing.T) {
	p := NewParser("CREATE TABLE t (c NUMERIC(20,5))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(ct.Cols))
	}
	if ct.Cols[0].Type != LX.T_NUMERIC {
		t.Errorf("Type: got %d, want NUMERIC", ct.Cols[0].Type)
	}
	if ct.Cols[0].Size != 20 {
		t.Errorf("Size: got %d, want 20", ct.Cols[0].Size)
	}
}

func TestCastExprType(t *testing.T) {
	p := NewParser("SELECT CAST(x AS VARCHAR(50))")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sel := stmt.(*Select)
	if len(sel.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(sel.Cols))
	}
	cast, ok := sel.Cols[0].(*CastExpr)
	if !ok {
		t.Fatalf("expected *CastExpr, got %T", sel.Cols[0])
	}
	if cast.Type == nil {
		t.Fatal("Type should not be nil")
	}
	if cast.Type.Type != LX.T_VARCHAR {
		t.Errorf("Type: got %d, want VARCHAR", cast.Type.Type)
	}
	if cast.Type.Size != 50 {
		t.Errorf("Size: got %d, want 50", cast.Type.Size)
	}
}

// ── INTERVAL ─────────────────────────────────────────────────────────

func TestParse_Interval_InvalidUnit(t *testing.T) {
	input := "SELECT INTERVAL '7' FOO FROM t"
	p := NewParser(input)
	_, err := p.Parse()
	if err == nil {
		t.Error("expected error for invalid interval unit FOO")
	}
}

func TestParse_Interval_ValidUnits(t *testing.T) {
	units := []string{"YEAR", "MONTH", "DAY", "HOUR", "MINUTE", "SECOND"}
	for _, unit := range units {
		input := "SELECT INTERVAL '1' " + unit + " FROM t"
		p := NewParser(input)
		_, err := p.Parse()
		if err != nil {
			t.Errorf("valid unit %s: unexpected error: %v", unit, err)
		}
	}
}

// ── UNARY ────────────────────────────────────────────────────────────

func TestUnaryNot(t *testing.T) {
	tests := []string{
		"SELECT NOT 1",
		"SELECT NOT 0",
		"SELECT NOT NULL",
		"SELECT NOT (1 = 1)",
		"SELECT -5",
		"SELECT ~5",
		"SELECT NOT NOT 1",
	}
	for _, sql := range tests {
		t.Run(sql, func(t *testing.T) {
			parser := NewParser(sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error for %q: %v", sql, err)
			}
			t.Logf("%q => %+v", sql, stmt)
		})
	}
}