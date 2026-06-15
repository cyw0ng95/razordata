package EX

import (
	"testing"
	
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// TestCheckConstraintValidateCheckFunc tests the validateCheck function directly (REQ000211).
func TestCheckConstraintValidateCheckFunc(t *testing.T) {
	schema := &storeSchema{
		cols: []string{"x"},
		checks: []PS.Expr{
			&PS.BinaryExpr{Op: int(LX.T_GT), Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 0}},
		},
	}
	
	// x = 5 should pass
	row := Row{Cols: []string{"x"}, Data: []any{int64(5)}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for x=5, got %v", err)
	}
	
	// x = 0 should fail (0 > 0 is false)
	row = Row{Cols: []string{"x"}, Data: []any{int64(0)}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for x=0")
	}
	
	// x = -1 should fail (-1 > 0 is false)
	row = Row{Cols: []string{"x"}, Data: []any{int64(-1)}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for x=-1")
	}
}

// TestCheckConstraintMultiple checks multiple CHECK constraints.
func TestCheckConstraintMultiple(t *testing.T) {
	schema := &storeSchema{
		cols: []string{"score"},
		checks: []PS.Expr{
			&PS.BinaryExpr{Op: int(LX.T_GE), Left: &PS.Ident{Name: "score"}, Right: &PS.NumberLiteral{Val: 0}},
			&PS.BinaryExpr{Op: int(LX.T_LE), Left: &PS.Ident{Name: "score"}, Right: &PS.NumberLiteral{Val: 100}},
		},
	}
	
	// score = 50 should pass both checks
	row := Row{Cols: []string{"score"}, Data: []any{int64(50)}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for score=50, got %v", err)
	}
	
	// score = -1 should fail first check
	row = Row{Cols: []string{"score"}, Data: []any{int64(-1)}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for score=-1")
	}
	
	// score = 150 should fail second check
	row = Row{Cols: []string{"score"}, Data: []any{int64(150)}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for score=150")
	}
}

// TestCheckConstraintNilExpr verifies nil CHECK expressions are skipped.
func TestCheckConstraintNilExpr(t *testing.T) {
	schema := &storeSchema{
		cols:   []string{"x"},
		checks: []PS.Expr{nil},
	}
	
	row := Row{Cols: []string{"x"}, Data: []any{int64(0)}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for nil CHECK, got %v", err)
	}
}

// TestCheckConstraintWithAnd verifies complex CHECK expressions.
func TestCheckConstraintWithAnd(t *testing.T) {
	// price > 0 AND price < 1000
	andExpr := &PS.BinaryExpr{
		Op: int(LX.T_AND),
		Left: &PS.BinaryExpr{Op: int(LX.T_GT), Left: &PS.Ident{Name: "price"}, Right: &PS.NumberLiteral{Val: 0}},
		Right: &PS.BinaryExpr{Op: int(LX.T_LT), Left: &PS.Ident{Name: "price"}, Right: &PS.NumberLiteral{Val: 1000}},
	}
	
	schema := &storeSchema{
		cols:   []string{"price"},
		checks: []PS.Expr{andExpr},
	}
	
	// price = 500 should pass
	row := Row{Cols: []string{"price"}, Data: []any{int64(500)}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for price=500, got %v", err)
	}
	
	// price = 0 should fail
	row = Row{Cols: []string{"price"}, Data: []any{int64(0)}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for price=0")
	}
	
	// price = 1000 should fail
	row = Row{Cols: []string{"price"}, Data: []any{int64(1000)}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for price=1000")
	}
}
