package DT

import (
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestAggregateLookupKey_StarExpr verifies that COUNT(*) produces
// the canonical "NAME(*)" key. REQ001975 / REQ001688.
func TestAggregateLookupKey_StarExpr(t *testing.T) {
	agg := &PS.AggregateFunc{
		Name: "COUNT",
		Arg:  &PS.StarExpr{},
	}
	got := AggregateLookupKey(agg)
	if got != "COUNT(*)" {
		t.Errorf("StarExpr: got %q, want %q", got, "COUNT(*)")
	}
}

// TestAggregateLookupKey_Ident verifies that an Ident arg produces
// "NAME(ident)" — and that two different idents produce different
// keys. REQ001975 / REQ001688.
func TestAggregateLookupKey_Ident(t *testing.T) {
	a := &PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v1"}}
	b := &PS.AggregateFunc{Name: "MIN", Arg: &PS.Ident{Name: "v2"}}
	ka := AggregateLookupKey(a)
	kb := AggregateLookupKey(b)
	if ka != "MIN(v1)" {
		t.Errorf("a: got %q, want %q", ka, "MIN(v1)")
	}
	if kb != "MIN(v2)" {
		t.Errorf("b: got %q, want %q", kb, "MIN(v2)")
	}
	if ka == kb {
		t.Error("different idents produced the same key")
	}
}

// TestAggregateLookupKey_Caching verifies that repeated calls return
// the same string (and thus the sync.Once cache is working). The
// pointer-based key for complex args must be stable across calls.
// REQ001975.
func TestAggregateLookupKey_Caching(t *testing.T) {
	// Use a BinaryExpr (complex arg) to exercise the reflect path.
	arg := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 1},
		Op:    LX.T_PLUS,
	}
	agg := &PS.AggregateFunc{Name: "SUM", Arg: arg}
	first := AggregateLookupKey(agg)
	second := AggregateLookupKey(agg)
	if first != second {
		t.Errorf("caching broken: first=%q second=%q", first, second)
	}
	// Complex-arg key must contain the type name and a colon separator.
	if !strings.HasPrefix(first, "SUM(") {
		t.Errorf("missing NAME( prefix: %q", first)
	}
	if !strings.HasSuffix(first, ")") {
		t.Errorf("missing ) suffix: %q", first)
	}
	if !strings.Contains(first, ":") {
		t.Errorf("complex-arg key missing type:ptr separator: %q", first)
	}
}

// TestAggregateLookupKey_DistinctComplexArgs verifies that two
// aggregates with the same name but different complex arg nodes
// produce different keys (no collision). REQ001688 regression guard.
func TestAggregateLookupKey_DistinctComplexArgs(t *testing.T) {
	a := &PS.AggregateFunc{
		Name: "MIN",
		Arg: &PS.NumberLiteral{Val: 94},
	}
	b := &PS.AggregateFunc{
		Name: "MIN",
		Arg: &PS.UnaryExpr{
			Op:      LX.T_MINUS,
			Operand: &PS.NumberLiteral{Val: 93},
		},
	}
	ka := AggregateLookupKey(a)
	kb := AggregateLookupKey(b)
	if ka == kb {
		t.Errorf("distinct complex args collided: ka=%q kb=%q", ka, kb)
	}
}

// TestAggregateLookupKey_NilSafety verifies that AggregateLookupKey
// does not panic for the supported arg kinds. We deliberately do
// NOT test nil Arg (that would panic in computeAggregateLookupKey
// just as it would have panicked the prior fmt.Sprintf("%T:%p", nil, nil)
// path — out of scope for REQ001975).
func TestAggregateLookupKey_NilSafety(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AggregateLookupKey panicked: %v", r)
		}
	}()
	agg := &PS.AggregateFunc{
		Name: "MAX",
		Arg:  &PS.NumberLiteral{Val: 42},
	}
	_ = AggregateLookupKey(agg)
}
