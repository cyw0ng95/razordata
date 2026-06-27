package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestIndexedColumnLikePrefix verifies REQ001070: detection of LIKE
// with a constant prefix pattern (e.g. 'abc%').
func TestIndexedColumnLikePrefix(t *testing.T) {
	tests := []struct {
		name    string
		expr    PS.Expr
		wantCol string
		wantOK  bool
		wantPfx string
	}{
		{
			name: "simple prefix",
			expr: &PS.BinaryExpr{
				Op:    int(LX.T_LIKE),
				Left:  &PS.Ident{Name: "name"},
				Right: &PS.StringLiteral{Val: "abc%"},
			},
			wantCol: "name",
			wantOK:  true,
			wantPfx: "abc",
		},
		{
			name: "exact match no wildcard",
			expr: &PS.BinaryExpr{
				Op:    int(LX.T_LIKE),
				Left:  &PS.Ident{Name: "name"},
				Right: &PS.StringLiteral{Val: "abc"},
			},
			wantCol: "name",
			wantOK:  true,
			wantPfx: "abc",
		},
		{
			name: "wildcard in middle",
			expr: &PS.BinaryExpr{
				Op:    int(LX.T_LIKE),
				Left:  &PS.Ident{Name: "name"},
				Right: &PS.StringLiteral{Val: "ab%c"},
			},
			wantCol: "name",
			wantOK:  true,
			wantPfx: "ab",
		},
		{
			name: "no prefix wildcard first char",
			expr: &PS.BinaryExpr{
				Op:    int(LX.T_LIKE),
				Left:  &PS.Ident{Name: "name"},
				Right: &PS.StringLiteral{Val: "%abc"},
			},
			wantCol: "",
			wantOK:  false,
		},
		{
			name: "column on right side",
			expr: &PS.BinaryExpr{
				Op:    int(LX.T_LIKE),
				Left:  &PS.StringLiteral{Val: "abc%"},
				Right: &PS.Ident{Name: "name"},
			},
			wantCol: "",
			wantOK:  false,
		},
		{
			name:    "nil expr",
			expr:    nil,
			wantCol: "",
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			col, prefix, ok := indexedColumnLikePrefix(tc.expr)
			if ok != tc.wantOK {
				t.Fatalf("indexedColumnLikePrefix ok=%v, want %v", ok, tc.wantOK)
			}
			if ok {
				if col != tc.wantCol {
					t.Errorf("col=%q, want %q", col, tc.wantCol)
				}
				if string(prefix) != tc.wantPfx {
					t.Errorf("prefix=%q, want %q", string(prefix), tc.wantPfx)
				}
			}
		})
	}
}

// TestIndexedColumnLikePrefix_Underscore verifies REQ001070: underscore
// wildcard in LIKE pattern. The prefix ends before the first '_' or '%'.
func TestIndexedColumnLikePrefix_Underscore(t *testing.T) {
	expr := &PS.BinaryExpr{
		Op:    int(LX.T_LIKE),
		Left:  &PS.Ident{Name: "name"},
		Right: &PS.StringLiteral{Val: "abc_def%"},
	}
	col, prefix, ok := indexedColumnLikePrefix(expr)
	if !ok {
		t.Fatal("expected indexedColumnLikePrefix to succeed")
	}
	if col != "name" {
		t.Errorf("col=%q, want %q", col, "name")
	}
	if string(prefix) != "abc" {
		t.Errorf("prefix=%q, want %q", string(prefix), "abc")
	}
}
