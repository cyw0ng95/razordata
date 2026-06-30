//go:build !slt_corpus_full

package EX

import (
	"testing"

	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestCompareValue verifies REQ000776: compareValue operates directly
// on Value types via Kind switching (no interface conversion).
func TestCompareValue(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want int
	}{
		{"int_int_equal", NewIntValue(5), NewIntValue(5), 0},
		{"int_int_less", NewIntValue(3), NewIntValue(7), -1},
		{"int_int_greater", NewIntValue(9), NewIntValue(2), 1},
		{"int_int_neg", NewIntValue(-5), NewIntValue(-3), -1},
		{"float_float_equal", NewFloatValue(3.5), NewFloatValue(3.5), 0},
		{"float_float_less", NewFloatValue(1.2), NewFloatValue(3.4), -1},
		{"int_float_mixed", NewIntValue(5), NewFloatValue(3.0), 1},
		{"float_int_mixed", NewFloatValue(1.5), NewIntValue(2), -1},
		{"text_text", NewTextValue("abc"), NewTextValue("abd"), -1},
		{"text_text_eq", NewTextValue("abc"), NewTextValue("abc"), 0},
		{"bool_bool", NewBoolValue(true), NewBoolValue(false), 1},
		{"null_null", NullValue(), NullValue(), 0},
		{"null_int", NullValue(), NewIntValue(5), -1},
		{"int_null", NewIntValue(5), NullValue(), 1},
		{"blob_eq", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 3}), 0},
		{"blob_ne", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 4}), -1},
		{"int_text", NewIntValue(5), NewTextValue("5"), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pl.CompareValue(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("pl.CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestEqualValueValue verifies REQ000776: equalValueValue switches on
// Kind to compare values without interface dispatch.
func TestEqualValueValue(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"int_eq", NewIntValue(5), NewIntValue(5), true},
		{"int_ne", NewIntValue(5), NewIntValue(6), false},
		{"float_eq", NewFloatValue(3.5), NewFloatValue(3.5), true},
		{"text_eq", NewTextValue("hello"), NewTextValue("hello"), true},
		{"text_ne", NewTextValue("hello"), NewTextValue("world"), false},
		{"bool_eq", NewBoolValue(true), NewBoolValue(true), true},
		{"bool_ne", NewBoolValue(true), NewBoolValue(false), false},
		{"null_null", NullValue(), NullValue(), true},
		{"null_int", NullValue(), NewIntValue(5), false},
		{"blob_eq", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 3}), true},
		{"blob_ne", NewBlobValue([]byte{1, 2, 3}), NewBlobValue([]byte{1, 2, 4}), false},
		{"diff_kinds", NewIntValue(5), NewTextValue("5"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pl.EqualValueValue(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("pl.EqualValueValue(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestNumericArithValue verifies REQ000776: numericArithValue operates
// on Value directly and returns Value.
func TestNumericArithValue(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		op   rune
		want Value
	}{
		{"int_add", NewIntValue(2), NewIntValue(3), '+', NewIntValue(5)},
		{"int_sub", NewIntValue(10), NewIntValue(3), '-', NewIntValue(7)},
		{"int_mul", NewIntValue(4), NewIntValue(5), '*', NewIntValue(20)},
		{"int_zero", NewIntValue(0), NewIntValue(5), '*', NewIntValue(0)},
		{"float_add", NewFloatValue(1.5), NewFloatValue(2.5), '+', NewFloatValue(4.0)},
		{"float_mul", NewFloatValue(2.0), NewFloatValue(3.0), '*', NewFloatValue(6.0)},
		{"int_float_mixed", NewIntValue(5), NewFloatValue(2.0), '*', NewFloatValue(10.0)},
		{"null_int", NullValue(), NewIntValue(5), '+', NullValue()},
		{"int_null", NewIntValue(5), NullValue(), '+', NullValue()},
		{"text_int", NewTextValue("x"), NewIntValue(5), '+', NullValue()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EV.NumericArithValue(tc.a, tc.b, tc.op)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("EV.NumericArithValue(%v, %v, %c) = %v, want %v",
					tc.a, tc.b, tc.op, got, tc.want)
			}
		})
	}
}

// TestEncodeDecodeBlobRoundTrip verifies REQ000776: EncodeRow and
// decodeRow correctly serialize and deserialize a blob column as
// KindBlob (not KindText as it was previously).
func TestEncodeDecodeBlobRoundTrip(t *testing.T) {
	schema := &StoreSchema{
		Cols: []string{"data"},
	}
	original := Row{
		Cols: []string{"data"},
		Data: []Value{NewBlobValue([]byte{0x00, 0x01, 0x02, 0xff, 0xfe})},
	}
	encoded, err := OP.EncodeRow(schema, original)
	if err != nil {
		t.Fatalf("EncodeRow: %v", err)
	}
	decoded, err := OP.DecodeRow(encoded, schema)
	if err != nil {
		t.Fatalf("decodeRow: %v", err)
	}
	if len(decoded.Data) != 1 {
		t.Fatalf("expected 1 column, got %d", len(decoded.Data))
	}
	v := decoded.Data[0]
	if v.Kind != KindBlob {
		t.Errorf("decoded Kind = %v, want KindBlob", v.Kind)
	}
	if !v.Equal(original.Data[0]) {
		t.Errorf("round-trip mismatch: got %v, want %v", v, original.Data[0])
	}
}

// TestEvalInValue verifies REQ000776: evalInValue uses per-kind
// hash sets for O(1) IN-list probing without boxing.
func TestEvalInValue(t *testing.T) {
	expr := &PS.InExpr{
		Expr: &PS.NumberLiteral{Val: 5},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 1},
			&PS.NumberLiteral{Val: 3},
			&PS.NumberLiteral{Val: 5},
			&PS.NumberLiteral{Val: 7},
			&PS.NumberLiteral{Val: 9},
		},
	}

	t.Run("hit", func(t *testing.T) {
		got, err := EV.EvalInValue(expr, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(NewBoolValue(true)) {
			t.Errorf("got %v, want true", got)
		}
	})
	t.Run("miss", func(t *testing.T) {
		e2 := *expr
		e2.Expr = &PS.NumberLiteral{Val: 4}
		got, err := EV.EvalInValue(&e2, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(NewBoolValue(false)) {
			t.Errorf("got %v, want false", got)
		}
	})
	t.Run("null_target", func(t *testing.T) {
		e2 := *expr
		e2.Expr = &PS.NullLiteral{}
		got, err := EV.EvalInValue(&e2, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(NullValue()) {
			t.Errorf("got %v, want NULL", got)
		}
	})
	t.Run("empty_list", func(t *testing.T) {
		e2 := *expr
		e2.List = nil
		got, err := EV.EvalInValue(&e2, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(NewBoolValue(false)) {
			t.Errorf("got %v, want false (empty IN)", got)
		}
	})
}

// TestEvalInHash_Int64Only verifies REQ000817: int64-only IN-lists
// use an int64-keyed map to avoid Value boxing.
func TestEvalInHash_Int64Only(t *testing.T) {
	// Clear the cache to ensure we test fresh state.
	delete(
		EV.InHashCacheMap, getTestInExpr())

	expr := &PS.InExpr{
		Expr: &PS.NumberLiteral{Val: 42},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 10},
			&PS.NumberLiteral{Val: 20},
			&PS.NumberLiteral{Val: 30},
			&PS.NumberLiteral{Val: 42},
			&PS.NumberLiteral{Val: 50},
		},
	}

	// Hit
	got, err := EV.EvalInValue(expr, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(NewBoolValue(true)) {
		t.Errorf("got %v, want true (42 in list)", got)
	}

	// Verify int64Set was populated
	cached :=
		EV.InHashCacheMap[expr]
	if cached == nil {
		t.Fatal("expected cached entry")
	}
	if cached.Int64Set == nil {
		t.Error("expected int64Set to be populated for int64-only list")
	}
	if _, ok := cached.Int64Set[42]; !ok {
		t.Error("expected 42 in int64Set")
	}
}

// TestEvalInHash_MixedTypes verifies REQ000817: mixed-type lists
// fall back to the any-typed map.
func TestEvalInHash_MixedTypes(t *testing.T) {
	expr := &PS.InExpr{
		Expr: &PS.NumberLiteral{Val: 42},
		List: []PS.Expr{
			&PS.NumberLiteral{Val: 10},
			&PS.StringLiteral{Val: "hello"},
			&PS.NumberLiteral{Val: 42},
			&PS.StringLiteral{Val: "world"},
			&PS.NumberLiteral{Val: 50},
		},
	}

	// Hit (string match shouldn't match the int target)
	got, err := EV.EvalInValue(expr, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(NewBoolValue(true)) {
		t.Errorf("got %v, want true (42 in mixed list)", got)
	}

	// Verify int64Set was NOT populated
	cached :=
		EV.InHashCacheMap[expr]
	if cached == nil {
		t.Fatal("expected cached entry")
	}
	if cached.Int64Set != nil {
		t.Error("expected int64Set to be nil for mixed-type list")
	}
}

func getTestInExpr() *PS.InExpr {
	return &PS.InExpr{
		Expr: &PS.NumberLiteral{Val: 0},
		List: []PS.Expr{&PS.NumberLiteral{Val: 0}},
	}
}
