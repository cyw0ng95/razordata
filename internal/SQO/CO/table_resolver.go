package CO

import (
	"unicode"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// SplitAlphaNum splits "e8" into ("e", "8"). Returns ("", "")
// if the string doesn't match {letters}{digits}.
// REQ001248, REQ001439.
func SplitAlphaNum(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	i := 0
	for i < len(s) && unicode.IsLetter(rune(s[i])) {
		i++
	}
	if i == 0 || i == len(s) {
		return "", ""
	}
	for j := i; j < len(s); j++ {
		if !unicode.IsDigit(rune(s[j])) {
			return "", ""
		}
	}
	return s[:i], s[i:]
}

// ResolveTableForColumn tries to resolve a column name using the
// SLT naming convention: "e8" => column "e" of table "t8".
// schemas is a map of table name → column names.
// REQ001248, REQ001439.
func ResolveTableForColumn(col string, schemas map[string][]string) string {
	base, numStr := SplitAlphaNum(col)
	if base == "" {
		return ""
	}
	tbl := "t" + numStr
	cols, ok := schemas[tbl]
	if !ok {
		return ""
	}
	for _, c := range cols {
		if c == base {
			return tbl
		}
	}
	return ""
}

// FindTableInSchemas searches the schema map for which table
// owns the given column. Falls back to SLT naming convention.
// schemas is a map of table name → column names.
// REQ001248, REQ001439.
func FindTableInSchemas(col string, schemas map[string][]string) string {
	for tbl, cols := range schemas {
		for _, c := range cols {
			if c == col {
				return tbl
			}
		}
	}
	if tbl := ResolveTableForColumn(col, schemas); tbl != "" {
		return tbl
	}
	return ""
}

// WalkExpr is a generic expression tree walker that calls fn for
// each expression node in depth-first order.
// REQ000983, REQ001439.
func WalkExpr(e PS.Expr, fn func(PS.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch v := e.(type) {
	case *PS.BinaryExpr:
		WalkExpr(v.Left, fn)
		WalkExpr(v.Right, fn)
	case *PS.UnaryExpr:
		WalkExpr(v.Operand, fn)
	case *PS.ListExpr:
		for _, item := range v.Items {
			WalkExpr(item, fn)
		}
	case *PS.InExpr:
		WalkExpr(v.Expr, fn)
		for _, item := range v.List {
			WalkExpr(item, fn)
		}
	case *PS.BetweenExpr:
		WalkExpr(v.Expr, fn)
		WalkExpr(v.Low, fn)
		WalkExpr(v.High, fn)
	case *PS.AggregateFunc:
		if v.Arg != nil {
			WalkExpr(v.Arg, fn)
		}
	case *PS.CaseExpr:
		if v.Expr != nil {
			WalkExpr(v.Expr, fn)
		}
		for _, w := range v.WhenList {
			WalkExpr(w.Cond, fn)
			WalkExpr(w.Then, fn)
		}
		if v.Else != nil {
			WalkExpr(v.Else, fn)
		}
	case *PS.FunctionCall:
		for _, arg := range v.Args {
			WalkExpr(arg, fn)
		}
	case *PS.WindowFunc:
		for _, arg := range v.Args {
			WalkExpr(arg, fn)
		}
	case *PS.CastExpr:
		WalkExpr(v.Expr, fn)
	case *PS.AliasedExpr:
		WalkExpr(v.Expr, fn)
	case *PS.SubqueryExpr, *PS.ExistsExpr:
	}
}
