package EV

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// REQ001552 — three-valued IN-list short-circuit coverage.
//
// evidence/in1.test had 12 IN/NOT IN failures all rooted in three
// distinct three-valued-logic shapes:
//
//	(A) target hit and no NULL in list           -> return TRUE / FALSE
//	    immediately (do NOT keep scanning for NULLs after the match).
//	(B) target miss but list contains NULL       -> return NULL (SQL standard).
//	(C) target is NULL itself                    -> NULL (per SQL standard).
//	(D) list entirely NULLs                      -> NULL.
//
// EvalInValue (eval.go:973) already implements (B)/(C)/(D) correctly.
// The unit tests pin each shape so a future refactor cannot regress.
func TestEvalInList_ThreeValuedLogic_REQ001552(t *testing.T) {
	cases := []struct {
		name    string
		expr    PS.Expr
		list    []PS.Expr
		want    any // nil means DT.NullValue
		wantErr bool
	}{
		{
			name: "A_hit_no_null_in_list",
			expr: &PS.NumberLiteral{Val: int64(4)},
			list: []PS.Expr{
				&PS.NumberLiteral{Val: int64(2)},
				&PS.NumberLiteral{Val: int64(3)},
				&PS.NumberLiteral{Val: int64(4)},
			},
			want: true,
		},
		{
			name: "A_miss_no_null_in_list",
			expr: &PS.NumberLiteral{Val: int64(99)},
			list: []PS.Expr{
				&PS.NumberLiteral{Val: int64(2)},
				&PS.NumberLiteral{Val: int64(3)},
				&PS.NumberLiteral{Val: int64(4)},
			},
			want: false,
		},
		{
			name: "B_miss_list_has_null",
			expr: &PS.NumberLiteral{Val: int64(99)},
			list: []PS.Expr{
				&PS.NumberLiteral{Val: int64(2)},
				&PS.NullLiteral{},
				&PS.NumberLiteral{Val: int64(3)},
			},
			want: nil,
		},
		{
			name: "C_target_is_null",
			expr: &PS.NullLiteral{},
			list: []PS.Expr{
				&PS.NumberLiteral{Val: int64(2)},
				&PS.NumberLiteral{Val: int64(3)},
			},
			want: nil,
		},
		{
			name: "D_list_only_nulls",
			expr: &PS.NumberLiteral{Val: int64(2)},
			list: []PS.Expr{
				&PS.NullLiteral{},
				&PS.NullLiteral{},
			},
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := &PS.InExpr{Expr: c.expr, List: c.list}
			gotVal, err := EvalInValue(in, nil, nil)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, want err=%v", err, c.wantErr)
			}
			var got any
			if gotVal.Kind == DT.KindNull {
				got = nil
			} else {
				got = gotVal.ToAny()
			}
			if got != c.want {
				t.Errorf("got %v (%T), want %v", got, got, c.want)
			}
		})
	}
}
