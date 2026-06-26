package PS

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestParseCaseExpr tests the parser's CASE expression handling
// (REQ000202 - CASE expression coverage).
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

// TestParseExists tests the parser's EXISTS subquery handling
// (REQ000202 - EXISTS expression coverage).
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

// TestExistsExprType verifies the ExistsExpr structure
func TestExistsExprType(t *testing.T) {
	sel := &Select{From: "t"}
	exists := &ExistsExpr{Subquery: sel}

	if exists.Subquery == nil {
		t.Fatal("Subquery should not be nil")
	}
	// Subquery is Stmt interface, assert to *Select
	if sel2, ok := exists.Subquery.(*Select); ok {
		if sel2.From != "t" {
			t.Errorf("Subquery.From: got %q, want %q", sel2.From, "t")
		}
	} else {
		t.Errorf("expected Subquery to be *Select, got %T", exists.Subquery)
	}
}

// TestCaseExprType verifies the CaseExpr structure
func TestCaseExprType(t *testing.T) {
	when := []WhenClause{
		{Cond: &BinaryExpr{Op: int(LX.T_EQ)}, Then: &Ident{Name: "y"}},
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
