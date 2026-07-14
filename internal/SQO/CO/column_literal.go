package CO

import (
	"fmt"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// ExtractColumnLiteral extracts (column name, literal value) from a
// binary expression of the form `col = literal`. Returns false if the
// left side is not an Ident or the right side is not a literal.
// REQ001248, REQ001439.
func ExtractColumnLiteral(v *PS.BinaryExpr) (string, []byte, bool) {
	col, ok := v.Left.(*PS.Ident)
	if !ok {
		return "", nil, false
	}
	lit, ok := LiteralToBytes(v.Right)
	if !ok {
		return "", nil, false
	}
	return col.Name, lit, true
}

// LiteralToBytes extracts the byte representation from a literal
// expression (NumberLiteral, StringLiteral, BoolLiteral).
// REQ001248, REQ001439.
func LiteralToBytes(e PS.Expr) ([]byte, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return []byte(fmt.Sprintf("%d", v.Val)), true
	case *PS.StringLiteral:
		return []byte(v.Val), true
	case *PS.BoolLiteral:
		if v.Val {
			return []byte("true"), true
		}
		return []byte("false"), true
	}
	return nil, false
}

// ExtractColumnLiteralExpr is like ExtractColumnLiteral but accepts
// any expression. Unwraps BinaryExpr first.
// REQ001248, REQ001439.
func ExtractColumnLiteralExpr(e PS.Expr) (string, []byte, bool) {
	v, ok := e.(*PS.BinaryExpr)
	if !ok {
		return "", nil, false
	}
	return ExtractColumnLiteral(v)
}
