package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT")

// TestCheckConstraintValidateCheckFunc tests the validateCheck function directly (REQ000211).
func TestCheckConstraintValidateCheckFunc(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols: []string{"x"},
		Checks: []PS.Expr{
			&PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 0}},
		},
	}

	// x = 5 should pass
	row := Row{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(5))}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for x=5, got %v", err)
	}

	// x = 0 should fail (0 > 0 is false)
	row = Row{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(0))}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for x=0")
	}

	// x = -1 should fail (-1 > 0 is false)
	row = Row{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(-1))}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for x=-1")
	}
}

// TestCheckConstraintMultiple checks multiple CHECK constraints.
func TestCheckConstraintMultiple(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols: []string{"score"},
		Checks: []PS.Expr{
			&PS.BinaryExpr{Op: LX.T_GE, Left: &PS.Ident{Name: "score"}, Right: &PS.NumberLiteral{Val: 0}},
			&PS.BinaryExpr{Op: LX.T_LE, Left: &PS.Ident{Name: "score"}, Right: &PS.NumberLiteral{Val: 100}},
		},
	}

	// score = 50 should pass both checks
	row := Row{Cols: []string{"score"}, Data: []Value{NewIntValue(int64(50))}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for score=50, got %v", err)
	}

	// score = -1 should fail first check
	row = Row{Cols: []string{"score"}, Data: []Value{NewIntValue(int64(-1))}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for score=-1")
	}

	// score = 150 should fail second check
	row = Row{Cols: []string{"score"}, Data: []Value{NewIntValue(int64(150))}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for score=150")
	}
}

// TestCheckConstraintNilExpr verifies nil CHECK expressions are skipped.
func TestCheckConstraintNilExpr(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:   []string{"x"},
		Checks: []PS.Expr{nil},
	}

	row := Row{Cols: []string{"x"}, Data: []Value{NewIntValue(int64(0))}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for nil CHECK, got %v", err)
	}
}

// TestCheckConstraintWithAnd verifies complex CHECK expressions.
func TestCheckConstraintWithAnd(t *testing.T) {
	// price > 0 AND price < 1000
	andExpr := &PS.BinaryExpr{
		Op:    LX.T_AND,
		Left:  &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "price"}, Right: &PS.NumberLiteral{Val: 0}},
		Right: &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "price"}, Right: &PS.NumberLiteral{Val: 1000}},
	}

	schema := &DT.StoreSchema{
		Cols:   []string{"price"},
		Checks: []PS.Expr{andExpr},
	}

	// price = 500 should pass
	row := Row{Cols: []string{"price"}, Data: []Value{NewIntValue(int64(500))}}
	if err := validateCheck(schema, row); err != nil {
		t.Errorf("expected no error for price=500, got %v", err)
	}

	// price = 0 should fail
	row = Row{Cols: []string{"price"}, Data: []Value{NewIntValue(int64(0))}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for price=0")
	}

	// price = 1000 should fail
	row = Row{Cols: []string{"price"}, Data: []Value{NewIntValue(int64(1000))}}
	if err := validateCheck(schema, row); err == nil {
		t.Error("expected error for price=1000")
	}
}
