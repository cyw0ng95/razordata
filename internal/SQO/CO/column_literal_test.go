package CO

import (
	"bytes"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestExtractColumnLiteral_Valid(t *testing.T) {
	v := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "age"},
		Right: &PS.NumberLiteral{Val: 42},
	}
	col, lit, ok := ExtractColumnLiteral(v)
	if !ok {
		t.Fatal("expected true")
	}
	if col != "age" {
		t.Errorf("col = %q, want %q", col, "age")
	}
	if !bytes.Equal(lit, []byte("42")) {
		t.Errorf("lit = %q, want %q", lit, "42")
	}
}

func TestExtractColumnLiteral_NotIdent(t *testing.T) {
	v := &PS.BinaryExpr{
		Left:  &PS.NumberLiteral{Val: 1},
		Right: &PS.NumberLiteral{Val: 2},
	}
	if _, _, ok := ExtractColumnLiteral(v); ok {
		t.Error("expected false when left is not ident")
	}
}

func TestExtractColumnLiteral_NotLiteral(t *testing.T) {
	v := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.Ident{Name: "y"},
	}
	if _, _, ok := ExtractColumnLiteral(v); ok {
		t.Error("expected false when right is not literal")
	}
}

func TestLiteralToBytes_Number(t *testing.T) {
	got, ok := LiteralToBytes(&PS.NumberLiteral{Val: -7})
	if !ok || !bytes.Equal(got, []byte("-7")) {
		t.Errorf("got %q, want %q", got, "-7")
	}
}

func TestLiteralToBytes_String(t *testing.T) {
	got, ok := LiteralToBytes(&PS.StringLiteral{Val: "hello"})
	if !ok || !bytes.Equal(got, []byte("hello")) {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestLiteralToBytes_Bool(t *testing.T) {
	got, ok := LiteralToBytes(&PS.BoolLiteral{Val: true})
	if !ok || !bytes.Equal(got, []byte("true")) {
		t.Errorf("got %q, want true", got)
	}
	got, ok = LiteralToBytes(&PS.BoolLiteral{Val: false})
	if !ok || !bytes.Equal(got, []byte("false")) {
		t.Errorf("got %q, want false", got)
	}
}

func TestLiteralToBytes_NotLiteral(t *testing.T) {
	if _, ok := LiteralToBytes(&PS.Ident{Name: "x"}); ok {
		t.Error("expected false for non-literal")
	}
}

func TestExtractColumnLiteralExpr(t *testing.T) {
	e := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Right: &PS.StringLiteral{Val: "foo"},
	}
	col, lit, ok := ExtractColumnLiteralExpr(e)
	if !ok || col != "id" || !bytes.Equal(lit, []byte("foo")) {
		t.Errorf("got (%q, %q, %v), want (id, foo, true)", col, lit, ok)
	}
}

func TestExtractColumnLiteralExpr_NotBinary(t *testing.T) {
	if _, _, ok := ExtractColumnLiteralExpr(&PS.Ident{Name: "x"}); ok {
		t.Error("expected false for non-binary expr")
	}
}
