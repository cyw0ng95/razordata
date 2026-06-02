package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestEval(t *testing.T) {
	cases := []struct {
		name   string
		expr   PS.Expr
		params []interface{}
		want   interface{}
		err    bool
	}{
		{"number", &PS.NumberLiteral{Val: 42}, nil, int64(42), false},
		{"float", &PS.FloatLiteral{Val: 3.14}, nil, float64(3.14), false},
		{"string", &PS.StringLiteral{Val: "hello"}, nil, "hello", false},
		{"bool_true", &PS.BoolLiteral{Val: true}, nil, true, false},
		{"bool_false", &PS.BoolLiteral{Val: false}, nil, false, false},
		{"null", &PS.NullLiteral{}, nil, nil, false},
		{"ident", &PS.Ident{Name: "x"}, nil, "x", false},
		{"param", &PS.Param{Index: 0}, []interface{}{10}, 10, false},
		{"star", &PS.StarExpr{}, nil, "*", false},
		{"unary_minus", &PS.UnaryExpr{Op: int(LX.T_MINUS), Operand: &PS.NumberLiteral{Val: 5}}, nil, int64(-5), false},
		{"unary_plus", &PS.UnaryExpr{Op: int(LX.T_PLUS), Operand: &PS.NumberLiteral{Val: 5}}, nil, int64(5), false},
		{"binary_eq", &PS.BinaryExpr{Op: int(LX.T_EQ), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_ne", &PS.BinaryExpr{Op: int(LX.T_NE), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, true, false},
		{"binary_lt", &PS.BinaryExpr{Op: int(LX.T_LT), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, true, false},
		{"binary_le", &PS.BinaryExpr{Op: int(LX.T_LE), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_gt", &PS.BinaryExpr{Op: int(LX.T_GT), Left: &PS.NumberLiteral{Val: 2}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_ge", &PS.BinaryExpr{Op: int(LX.T_GE), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 1}}, nil, true, false},
		{"binary_plus", &PS.BinaryExpr{Op: int(LX.T_PLUS), Left: &PS.NumberLiteral{Val: 1}, Right: &PS.NumberLiteral{Val: 2}}, nil, int64(3), false},
		{"binary_minus", &PS.BinaryExpr{Op: int(LX.T_MINUS), Left: &PS.NumberLiteral{Val: 5}, Right: &PS.NumberLiteral{Val: 3}}, nil, int64(2), false},
		{"binary_mul", &PS.BinaryExpr{Op: int(LX.T_STAR), Left: &PS.NumberLiteral{Val: 3}, Right: &PS.NumberLiteral{Val: 4}}, nil, int64(12), false},
		{"binary_div", &PS.BinaryExpr{Op: int(LX.T_SLASH), Left: &PS.NumberLiteral{Val: 10}, Right: &PS.NumberLiteral{Val: 2}}, nil, int64(5), false},
		{"binary_and", &PS.BinaryExpr{Op: int(LX.T_AND), Left: &PS.BoolLiteral{Val: true}, Right: &PS.BoolLiteral{Val: true}}, nil, true, false},
		{"binary_or", &PS.BinaryExpr{Op: int(LX.T_OR), Left: &PS.BoolLiteral{Val: false}, Right: &PS.BoolLiteral{Val: true}}, nil, true, false},
		{"case_simple", &PS.CaseExpr{
			WhenList: []PS.WhenClause{{Cond: &PS.BoolLiteral{Val: true}, Then: &PS.StringLiteral{Val: "yes"}}},
			Else:     &PS.StringLiteral{Val: "no"},
		}, nil, "yes", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Eval(tc.expr, nil, tc.params)
			if tc.err {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOperators(t *testing.T) {
	scan := NewSeqScan("t", nil)
	if scan == nil {
		t.Fatal("NewSeqScan returned nil")
	}

	filter := NewFilter(scan, nil)
	if filter == nil {
		t.Fatal("NewFilter returned nil")
	}

	project := NewProject(scan, nil)
	if project == nil {
		t.Fatal("NewProject returned nil")
	}

	sort := NewSort(scan, nil, true)
	if sort == nil {
		t.Fatal("NewSort returned nil")
	}

	limit := NewLimit(scan, nil)
	if limit == nil {
		t.Fatal("NewLimit returned nil")
	}
}

func TestSeqScanNotImplemented(t *testing.T) {
	scan := NewSeqScan("t", nil)
	_, err := scan.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestFilterNotImplemented(t *testing.T) {
	scan := NewSeqScan("t", nil)
	filter := NewFilter(scan, nil)
	_, err := filter.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestProjectNotImplemented(t *testing.T) {
	scan := NewSeqScan("t", nil)
	project := NewProject(scan, nil)
	_, err := project.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestSortNotImplemented(t *testing.T) {
	scan := NewSeqScan("t", nil)
	sort := NewSort(scan, nil, true)
	_, err := sort.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestLimitNotImplemented(t *testing.T) {
	scan := NewSeqScan("t", nil)
	limit := NewLimit(scan, nil)
	_, err := limit.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestInsertNotImplemented(t *testing.T) {
	insert := NewInsert("t", nil, nil)
	_, err := insert.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestUpdateNotImplemented(t *testing.T) {
	scan := NewSeqScan("t", nil)
	update := NewUpdate("t", nil, nil, scan)
	_, err := update.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}

func TestDeleteNotImplemented(t *testing.T) {
	scan := NewSeqScan("t", nil)
	delete := NewDelete("t", nil, scan)
	_, err := delete.Next(context.Background())
	if err != ErrNotImplemented {
		t.Errorf("expected ErrNotImplemented, got %v", err)
	}
}
