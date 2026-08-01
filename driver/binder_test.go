package driver

import (
	"database/sql/driver"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestParamBinderBind verifies REQ002291: positional driver.NamedValue
// args convert to []any through the shared toDriverValue coercion.
func TestParamBinderBind(t *testing.T) {
	cases := []struct {
		name string
		in   []driver.NamedValue
		want []any
	}{
		{"empty", nil, []any{}},
		{"int64", []driver.NamedValue{{Ordinal: 1, Value: int64(42)}}, []any{int64(42)}},
		{"string", []driver.NamedValue{{Ordinal: 1, Value: "hello"}}, []any{"hello"}},
		{"blob", []driver.NamedValue{{Ordinal: 1, Value: []byte("x")}}, []any{[]byte("x")}},
		{"nil", []driver.NamedValue{{Ordinal: 1, Value: nil}}, []any{nil}},
		{"bool-true", []driver.NamedValue{{Ordinal: 1, Value: true}}, []any{int64(1)}},
		{"bool-false", []driver.NamedValue{{Ordinal: 1, Value: false}}, []any{int64(0)}},
		{
			"mixed-order",
			[]driver.NamedValue{
				{Ordinal: 1, Value: int64(1)},
				{Ordinal: 2, Value: "a"},
				{Ordinal: 3, Value: nil},
			},
			[]any{int64(1), "a", nil},
		},
		{
			"ap-value",
			[]driver.NamedValue{{Ordinal: 1, Value: AP.Value{Kind: AP.KindInt, I64: 7}}},
			[]any{int64(7)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParamBinder{}.Bind(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d", len(got), len(c.want))
			}
			for i := range got {
				if !equalAny(got[i], c.want[i]) {
					t.Errorf("arg[%d] = %#v, want %#v", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestParamBinderBindValues verifies the deprecated []driver.Value path.
func TestParamBinderBindValues(t *testing.T) {
	got := ParamBinder{}.BindValues([]driver.Value{int64(5), "s", true})
	want := []any{int64(5), "s", int64(1)}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if !equalAny(got[i], want[i]) {
			t.Errorf("arg[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestToNamedValues verifies 1-based ordinal wrapping.
func TestToNamedValues(t *testing.T) {
	got := ToNamedValues([]driver.Value{int64(1), "x"})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Ordinal != 1 || got[1].Ordinal != 2 {
		t.Errorf("ordinals = %d,%d want 1,2", got[0].Ordinal, got[1].Ordinal)
	}
	if got[0].Value != int64(1) || got[1].Value != "x" {
		t.Errorf("values = %#v,%#v", got[0].Value, got[1].Value)
	}
}

// TestParamBinderBindPrepared verifies REQ002291 placeholder-count
// validation against NumInput.
func TestParamBinderBindPrepared(t *testing.T) {
	var binder ParamBinder
	// zero/zero allowed
	got, err := binder.BindPrepared(nil, 0)
	if err != nil || got != nil {
		t.Errorf("nil/0: got %v, err %v", got, err)
	}
	// mismatch rejected
	if _, err := binder.BindPrepared([]driver.Value{int64(1)}, 2); err == nil {
		t.Error("expected error for 1 arg vs NumInput 2")
	}
	// match accepted
	got, err = binder.BindPrepared([]driver.Value{int64(9)}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != int64(9) {
		t.Errorf("BindPrepared = %#v, want [9]", got)
	}
}

func equalAny(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []byte:
		bv, ok := b.([]byte)
		if !ok {
			return false
		}
		if len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
		return true
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	default:
		return a == b
	}
}
