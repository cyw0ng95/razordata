package CO

import (
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestSplitAlphaNum(t *testing.T) {
	tests := []struct {
		in    string
		alpha string
		num   string
	}{
		{"", "", ""},
		{"abc", "", ""},
		{"123", "", ""},
		{"e8", "e", "8"},
		{"col10", "col", "10"},
		{"x", "", ""},
		{"1a", "", ""},
	}
	for _, tt := range tests {
		a, n := SplitAlphaNum(tt.in)
		if a != tt.alpha || n != tt.num {
			t.Errorf("SplitAlphaNum(%q) = (%q, %q), want (%q, %q)",
				tt.in, a, n, tt.alpha, tt.num)
		}
	}
}

func TestResolveTableForColumn(t *testing.T) {
	schemas := map[string][]string{
		"t8": {"e"},
		"t10": {"col"},
	}
	if got := ResolveTableForColumn("e8", schemas); got != "t8" {
		t.Errorf("got %q, want t8", got)
	}
	if got := ResolveTableForColumn("col10", schemas); got != "t10" {
		t.Errorf("got %q, want t10", got)
	}
	if got := ResolveTableForColumn("unknown5", schemas); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFindTableInSchemas(t *testing.T) {
	schemas := map[string][]string{
		"users": {"id", "name"},
		"items": {"sku"},
	}
	if got := FindTableInSchemas("id", schemas); got != "users" {
		t.Errorf("got %q, want users", got)
	}
	if got := FindTableInSchemas("name", schemas); got != "users" {
		t.Errorf("got %q, want users", got)
	}
	if got := FindTableInSchemas("sku", schemas); got != "items" {
		t.Errorf("got %q, want items", got)
	}
	if got := FindTableInSchemas("unknown", schemas); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	// SLT fallback
	schemas2 := map[string][]string{"t8": {"e"}}
	if got := FindTableInSchemas("e8", schemas2); got != "t8" {
		t.Errorf("got %q, want t8 (SLT fallback)", got)
	}
}

func TestWalkExpr(t *testing.T) {
	e := &PS.BinaryExpr{
		Left: &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	var names []string
	WalkExpr(e, func(node PS.Expr) {
		if id, ok := node.(*PS.Ident); ok {
			names = append(names, id.Name)
		}
	})
	if len(names) != 1 || names[0] != "x" {
		t.Errorf("names = %v, want [x]", names)
	}
}

func TestWalkExpr_Nil(t *testing.T) {
	called := false
	WalkExpr(nil, func(e PS.Expr) { called = true })
	if called {
		t.Error("walkExpr(nil) should not invoke fn")
	}
}

func TestWalkExpr_Deep(t *testing.T) {
	inner := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	e := &PS.BinaryExpr{
		Left:  &PS.UnaryExpr{Operand: &PS.Ident{Name: "b"}},
		Right: inner,
	}
	count := 0
	WalkExpr(e, func(node PS.Expr) { count++ })
	// Binary(Unary(Ident(b)), Binary(Ident(a), Number(1))) = 6 nodes
	if count != 6 {
		t.Errorf("count = %d, want 7", count)
	}
}
