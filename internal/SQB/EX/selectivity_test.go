package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestEstimateEqSelectivity_Uniform(t *testing.T) {
	stats := &ls.ColumnStats{
		DistinctCount: 100,
		RowCount:      1000,
	}
	sel := CO.EstimateEqSelectivity(stats, []byte("42"))
	expected := 1.0 / 100.0
	if sel != expected {
		t.Errorf("expected %f, got %f", expected, sel)
	}
}

func TestEstimateEqSelectivity_Histogram(t *testing.T) {
	stats := &ls.ColumnStats{
		RowCount: 1000,
		Histogram: []ls.HistogramBucket{
			{LowerBound: []byte("0"), UpperBound: []byte("100"), Count: 100},
			{LowerBound: []byte("100"), UpperBound: []byte("200"), Count: 50},
			{LowerBound: []byte("200"), UpperBound: []byte("300"), Count: 200},
		},
	}
	// DT.Value "150" falls in second bucket (50/1000 = 0.05)
	sel := CO.EstimateEqSelectivity(stats, []byte("150"))
	if sel != 0.05 {
		t.Errorf("expected 0.05, got %f", sel)
	}
}

func TestEstimateEqSelectivity_NoMatch(t *testing.T) {
	stats := &ls.ColumnStats{
		RowCount: 1000,
		Histogram: []ls.HistogramBucket{
			{LowerBound: []byte("0"), UpperBound: []byte("100"), Count: 100},
		},
	}
	// DT.Value "500" doesn't fall in any bucket
	sel := CO.EstimateEqSelectivity(stats, []byte("500"))
	if sel != 0.0 {
		t.Errorf("expected 0.0, got %f", sel)
	}
}

func TestEstimateRangeSelectivity_LessThan(t *testing.T) {
	stats := &ls.ColumnStats{
		RowCount: 1000,
		Histogram: []ls.HistogramBucket{
			{LowerBound: []byte("0"), UpperBound: []byte("100"), Count: 200},
			{LowerBound: []byte("100"), UpperBound: []byte("200"), Count: 300},
			{LowerBound: []byte("200"), UpperBound: []byte("300"), Count: 500},
		},
	}
	// "col < 150" matches first bucket fully (200) + half of second (~150) = 350
	// Without interpolation: 200/1000 = 0.2
	sel := CO.EstimateRangeSelectivity(stats, nil, []byte("150"))
	if sel < 0.1 || sel > 0.5 {
		t.Errorf("expected reasonable selectivity, got %f", sel)
	}
}

func TestEstimateRangeSelectivity_GreaterThan(t *testing.T) {
	stats := &ls.ColumnStats{
		RowCount: 1000,
		Histogram: []ls.HistogramBucket{
			{LowerBound: []byte("0"), UpperBound: []byte("100"), Count: 200},
			{LowerBound: []byte("100"), UpperBound: []byte("200"), Count: 300},
		},
	}
	// "col > 150" matches 0 from first bucket + part of second = some fraction
	sel := CO.EstimateRangeSelectivity(stats, []byte("150"), nil)
	if sel < 0.0 || sel > 1.0 {
		t.Errorf("expected valid selectivity, got %f", sel)
	}
}

func TestExtractColumnLiteral(t *testing.T) {
	expr := &PS.BinaryExpr{
		Op: 1, // T_EQ
		Left: &PS.Ident{
			Name: "age",
		},
		Right: &PS.NumberLiteral{
			Val: 25,
		},
	}
	col, lit, ok := CO.ExtractColumnLiteral(expr)
	if !ok {
		t.Fatal("expected successful extraction")
	}
	if col != "age" {
		t.Errorf("expected col 'age', got %q", col)
	}
	if string(lit) != "25" {
		t.Errorf("expected lit '25', got %q", string(lit))
	}
}

func TestLiteralToBytes(t *testing.T) {
	cases := []struct {
		expr   PS.Expr
		want   string
		wantOk bool
	}{
		{&PS.NumberLiteral{Val: 42}, "42", true},
		{&PS.StringLiteral{Val: "hello"}, "hello", true},
		{&PS.BoolLiteral{Val: true}, "true", true},
		{&PS.BoolLiteral{Val: false}, "false", true},
		{&PS.NullLiteral{}, "", false},
	}
	for _, c := range cases {
		got, ok := CO.LiteralToBytes(c.expr)
		if ok != c.wantOk {
			t.Errorf("ok: got %v, want %v", ok, c.wantOk)
		}
		if ok && string(got) != c.want {
			t.Errorf("value: got %q, want %q", string(got), c.want)
		}
	}
}
