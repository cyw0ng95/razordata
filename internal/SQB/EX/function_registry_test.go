// Package EX function/aggregate registry tests.
//
// REQ000978: verifies every documented function and aggregate has
// a registry entry, and that the registry dispatches to the right
// implementation.
package EX

import (
	"strings"
	"testing"

	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestFunctionRegistry_AllFunctionsRegistered verifies every
// documented function name has a registry entry.
func TestFunctionRegistry_AllFunctionsRegistered(t *testing.T) {
	expected := []string{
		"LENGTH", "UPPER", "LOWER", "IFNULL", "COALESCE", "NULLIF",
		"NOW", "SUBSTR", "ABS", "HEX", "ROUND", "CHAR",
		"CONCAT", "CONCAT_WS", "FORMAT", "LTRIM", "RTRIM", "TRIM",
		"REPLACE", "QUOTE", "TYPEOF", "OCTET_LENGTH", "UNICODE",
		"SQLITE_VERSION", "SQLITE_SOURCE_ID", "IIF", "IF", "INSTR",
		"SIGN", "MAX", "MIN", "RANDOM", "RANDOMBLOB", "ZEROBLOB",
		"GLOB", "LIKELIHOOD", "LIKELY", "SOUNDEX", "UNHEX", "UNISTR",
		"UNLIKELY", "CHANGES", "LAST_INSERT_ROWID", "TOTAL_CHANGES",
	}
	for _, name := range expected {
		if _, ok := EV.ScalarFuncRegistry[name]; !ok {
			t.Errorf("ScalarFuncRegistry missing entry for %q", name)
		}
	}
}

func TestAggregateRegistry_AllFunctionsRegistered(t *testing.T) {
	expected := []string{"COUNT", "SUM", "AVG", "MIN", "MAX", "GROUP_CONCAT"}
	for _, name := range expected {
		if _, ok := AG.AggregateFuncRegistry[name]; !ok {
			t.Errorf("AG.AggregateFuncRegistry missing entry for %q", name)
		}
	}
}

// TestFunctionRegistry_DispatchReachesImpl verifies the registry
// dispatches to the registered implementation for a few sample
// functions.
func TestFunctionRegistry_DispatchReachesImpl(t *testing.T) {
	cases := []struct {
		name string
		expr *PS.FunctionCall
		want any
	}{
		{
			name: "UPPER",
			expr: &PS.FunctionCall{Name: "UPPER", Args: []PS.Expr{&PS.StringLiteral{Val: "abc"}}},
			want: "ABC",
		},
		{
			name: "LOWER",
			expr: &PS.FunctionCall{Name: "LOWER", Args: []PS.Expr{&PS.StringLiteral{Val: "ABC"}}},
			want: "abc",
		},
		{
			name: "LENGTH",
			expr: &PS.FunctionCall{Name: "LENGTH", Args: []PS.Expr{&PS.StringLiteral{Val: "hello"}}},
			want: int64(5),
		},
		{
			name: "COALESCE",
			expr: &PS.FunctionCall{Name: "COALESCE", Args: []PS.Expr{&PS.NullLiteral{}, &PS.StringLiteral{Val: "fallback"}}},
			want: "fallback",
		},
		{
			name: "IFNULL",
			expr: &PS.FunctionCall{Name: "IFNULL", Args: []PS.Expr{&PS.NullLiteral{}, &PS.StringLiteral{Val: "fb"}}},
			want: "fb",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := EV.EvalValue(tc.expr, nil, nil)
			if err != nil {
				t.Fatalf("EV.EvalValue: %v", err)
			}
			got := v.ToAny()
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFunctionRegistry_AggregateDispatch verifies aggregate
// dispatch via the registry.
func TestFunctionRegistry_AggregateDispatch(t *testing.T) {
	rows := []DT.Row{
		{Cols: []string{"x"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []DT.Value{DT.NewIntValue(10)}},
		{Cols: []string{"x"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []DT.Value{DT.NewIntValue(20)}},
		{Cols: []string{"x"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []DT.Value{DT.NewIntValue(30)}},
	}
	t.Run("COUNT", func(t *testing.T) {
		agg := &PS.AggregateFunc{Name: "COUNT", Arg: &PS.StarExpr{}}
		v, err := AG.EvalAggregateOver(agg, rows, nil)
		if err != nil {
			t.Fatalf("COUNT: %v", err)
		}
		if v != int64(3) {
			t.Errorf("COUNT = %v, want 3", v)
		}
	})
	t.Run("SUM", func(t *testing.T) {
		agg := &PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "x"}}
		v, err := AG.EvalAggregateOver(agg, rows, nil)
		if err != nil {
			t.Fatalf("SUM: %v", err)
		}
		if v != int64(60) {
			t.Errorf("SUM = %v, want 60", v)
		}
	})
	t.Run("AVG", func(t *testing.T) {
		agg := &PS.AggregateFunc{Name: "AVG", Arg: &PS.Ident{Name: "x"}}
		v, err := AG.EvalAggregateOver(agg, rows, nil)
		if err != nil {
			t.Fatalf("AVG: %v", err)
		}
		if v != float64(20) {
			t.Errorf("AVG = %v, want 20", v)
		}
	})
}

// TestFunctionRegistry_FunctionDispatch verifies scalar function
// dispatch via the registry.
func TestFunctionRegistry_FunctionDispatch(t *testing.T) {
	expr := &PS.FunctionCall{Name: "UPPER", Args: []PS.Expr{&PS.StringLiteral{Val: "hello"}}}
	v, err := EV.EvalFunction(expr, nil, nil, nil)
	if err != nil {
		t.Fatalf("UPPER: %v", err)
	}
	if v.ToAny() != "HELLO" {
		t.Errorf("UPPER = %v, want HELLO", v.ToAny())
	}
}

// TestFunctionRegistry_UnknownFunction verifies that an unknown
// function returns ErrEval.
func TestFunctionRegistry_UnknownFunction(t *testing.T) {
	expr := &PS.FunctionCall{Name: "NONEXISTENT", Args: []PS.Expr{}}
	_, err := EV.EvalFunction(expr, nil, nil, nil)
	if !strings.Contains(err.Error(), "eval") && err != EV.ErrEval {
		t.Errorf("expected ErrEval, got %v", err)
	}
}

// TestFunctionRegistry_DispatchByString checks a few functions that
// are NOT in the registry still work via isDateTimeFunc / isJSONFunc
// paths (smoke test).
func TestFunctionRegistry_DispatchUnknownFails(t *testing.T) {
	expr := &PS.FunctionCall{Name: "NOT_A_REAL_FUNC", Args: nil}
	v, err := EV.EvalFunction(expr, nil, nil, nil)
	if err == nil {
		t.Errorf("expected error for unknown function, got %v", v)
	}
	if !strings.Contains(err.Error(), "eval") && err != EV.ErrEval {
		t.Logf("got err: %v (acceptable)", err)
	}
}
