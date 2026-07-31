package CO

import (
	"strings"

	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// IsColumnLiteralPair returns true when a is an Ident and b is a
// literal (number, string, bool, null).
// REQ001248, REQ001439.
func IsColumnLiteralPair(a, b PS.Expr) bool {
	if _, ok := a.(*PS.Ident); !ok {
		return false
	}
	switch b.(type) {
	case *PS.NumberLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return true
	}
	return false
}

// LiteralValue extracts a typed value from a literal expression.
// Returns (val, true) for NumberLiteral/StringLiteral/BoolLiteral,
// (_, false) otherwise.
// REQ001248, REQ001439.
func LiteralValue(e PS.Expr) (any, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return v.Val, true
	case *PS.StringLiteral:
		return v.Val, true
	case *PS.BoolLiteral:
		return v.Val, true
	}
	return nil, false
}

// ExtractEqualityAnySide extracts (columnName, value, ok) from
// `col = literal` OR `literal = col`. Both operands may be the
// column reference.
// REQ001248, REQ001439.
func ExtractEqualityAnySide(pred PS.Expr) (string, any, bool) {
	bin, ok := pred.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_EQ {
		return "", nil, false
	}
	if col, ok := bin.Left.(*PS.Ident); ok {
		if v, ok := LiteralValue(bin.Right); ok {
			return col.Name, v, true
		}
	}
	if col, ok := bin.Right.(*PS.Ident); ok {
		if v, ok := LiteralValue(bin.Left); ok {
			return col.Name, v, true
		}
	}
	return "", nil, false
}

// ExtractInListValues extracts (columnName, values, ok) from a
// predicate of the form "col IN (val1, val2, ...)" where all values
// are literals. Handles both *PS.InExpr and
// *PS.BinaryExpr{Op: T_IN} (legacy).
// REQ001218, REQ001439.
func ExtractInListValues(pred PS.Expr) (string, []any, bool) {
	var col *PS.Ident
	var items []PS.Expr
	switch p := pred.(type) {
	case *PS.InExpr:
		ident, ok := p.Expr.(*PS.Ident)
		if !ok {
			return "", nil, false
		}
		col = ident
		items = p.List
	case *PS.BinaryExpr:
		if p.Op != LX.T_IN {
			return "", nil, false
		}
		ident, ok := p.Left.(*PS.Ident)
		if !ok {
			return "", nil, false
		}
		col = ident
		list, ok := p.Right.(*PS.ListExpr)
		if !ok {
			return "", nil, false
		}
		items = list.Items
	default:
		return "", nil, false
	}
	if len(items) == 0 {
		return "", nil, false
	}
	values := make([]any, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case *PS.NumberLiteral:
			values = append(values, v.Val)
		case *PS.StringLiteral:
			values = append(values, v.Val)
		case *PS.BoolLiteral:
			values = append(values, v.Val)
		case *PS.NullLiteral:
		default:
			return "", nil, false
		}
	}
	return col.Name, values, true
}

// ExtractOrChainEquality extracts (columnName, values, ok) from a
// same-column OR-chain of equality predicates. Returns
// (colName, [val1, val2, ...], true) when all leaves are
// `col = literal` on the same column. flattenOr must be provided
// as a callback (from EX/index_plan.go).
// REQ001218, REQ001439.
func ExtractOrChainEquality(pred PS.Expr, flattenOr func(PS.Expr) []PS.Expr) (string, []any, bool) {
	leaves := flattenOr(pred)
	if len(leaves) < 2 {
		return "", nil, false
	}
	var colName string
	values := make([]any, 0, len(leaves))
	for _, leaf := range leaves {
		c, v, ok := ExtractEqualityAnySide(leaf)
		if !ok {
			return "", nil, false
		}
		if colName == "" {
			colName = c
		} else if !strings.EqualFold(colName, c) {
			return "", nil, false
		}
		values = append(values, v)
	}
	return colName, values, true
}

// AllInSet returns true when every key in m is present in set.
// REQ001248, REQ001439.
func AllInSet(m, set map[string]bool) bool {
	if len(m) == 0 {
		return false
	}
	for k := range m {
		if !set[k] {
			return false
		}
	}
	return true
}

// ColNameFromExpr extracts a column name from an expression.
// Handles Ident (bare column) and QualifiedName (table.col).
// REQ000794, REQ001439.
func ColNameFromExpr(e PS.Expr) string {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name
	case *PS.QualifiedName:
		return v.Table + "." + v.Name
	}
	return ""
}

// ExtractSingleOnEquiKey returns (leftKey, rightKey, true) when ON
// is a simple equality of one column from leftTbl and one from
// rightTbl. Output is normalized so the first column is always from
// leftTbl. REQ000800, REQ001439.
func ExtractSingleOnEquiKey(on PS.Expr, leftTbl, rightTbl string) (string, string, bool) {
	bin, ok := on.(*PS.BinaryExpr)
	if !ok || bin.Op != LX.T_EQ {
		return "", "", false
	}
	a, aok := bin.Left.(*PS.QualifiedName)
	b, bok := bin.Right.(*PS.QualifiedName)
	if !aok || !bok {
		return "", "", false
	}
	if a.Table == leftTbl && b.Table == rightTbl {
		return a.Name, b.Name, true
	}
	if a.Table == rightTbl && b.Table == leftTbl {
		return b.Name, a.Name, true
	}
	return "", "", false
}

// ResolveSingleTablePredicate finds which single table a predicate
// references. Returns the table name if exactly one candidate table
// is referenced, or "" for cross-table or unresolvable predicates.
// findTableInSchemas is a callback for resolving column -> table.
// REQ001092, REQ001439.
func ResolveSingleTablePredicate(
	e PS.Expr,
	candidates []string,
	findTableInSchemas func(string) string,
) string {
	var cols []string
	WalkExpr(e, func(node PS.Expr) {
		switch v := node.(type) {
		case *PS.Ident:
			if v.Name != "" {
				cols = append(cols, v.Name)
			}
		case *PS.QualifiedName:
			cols = append(cols, v.Table+"."+v.Name)
		}
	})
	if len(cols) == 0 {
		return ""
	}
	var resolvedTables []string
	for _, col := range cols {
		var tbl string
		if dotIdx := strings.IndexByte(col, '.'); dotIdx >= 0 {
			tbl = col[:dotIdx]
		} else {
			tbl = findTableInSchemas(col)
		}
		if tbl == "" {
			return ""
		}
		resolvedTables = append(resolvedTables, tbl)
	}
	first := resolvedTables[0]
	for _, t := range resolvedTables[1:] {
		if t != first {
			return ""
		}
	}
	// Check against candidate set.
	for _, c := range candidates {
		if strings.EqualFold(first, c) {
			return c
		}
	}
	return ""
}
