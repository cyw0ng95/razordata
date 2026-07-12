package OP

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// pollution when one query's compiled closure referenced a stale
// row.ColIndex.
var predicateCache sync.Map

// compiled predicates today are column-literal comparisons which
// ARE state-dependent (row.ColIndex), so the global cache is
// bypassed by per-Filter caching entirely.
func lookupOrCompilePredicate(e PS.Expr) func(*Row) (bool, error) {
	key := fmt.Sprintf("%v", e)
	if cached, ok := predicateCache.Load(key); ok {
		return cached.(func(*Row) (bool, error))
	}
	fn := compileFilterExpr(e)
	if fn != nil {
		predicateCache.Store(key, fn)
	}
	return fn
}

// inferProjectType infers the token type from a Go value.
// REQ001184: mirrors Values.inferType for Project output rows.
func inferProjectType(v any) LX.TokenType {
	if v == nil {
		return LX.TokenType(-1)
	}
	switch v.(type) {
	case int64, int, int32:
		return LX.T_INT_KW
	case float64, float32:
		return LX.T_FLOAT_KW
	case bool:
		return LX.T_BOOL
	case string:
		return LX.T_TEXT
	case []byte:
		return LX.T_BLOB
	}
	return LX.T_TEXT
}

// compileFilterExpr compiles a simple Filter predicate into a
// specialized function that reads directly from row.Data, bypassing
// the Eval dispatch tree. Returns nil for complex predicates that
// cannot be compiled. REQ000802.
func compileFilterExpr(e PS.Expr) func(*Row) (bool, error) {
	if e == nil {
		return nil
	}
	switch v := e.(type) {
	case *PS.BinaryExpr:
		return compileBinary(v)
	case *PS.InExpr:
		return compileInExpr(v)
	case *PS.UnaryExpr:
		if v.Op == LX.T_NOT {
			// REQ001127: NOT InExpr requires NULL-aware three-valued
			// logic that the compiled (bool,error) path cannot express
			// (NULL IN list is NULL, not FALSE, so NOT NULL is NULL,
			// not TRUE). Fall back to the per-row Eval path which
			// handles NULL correctly.
			if _, ok := v.Operand.(*PS.InExpr); ok {
				return nil
			}
			inner := compileFilterExpr(v.Operand)
			if inner == nil {
				return nil
			}
			return func(row *Row) (bool, error) {
				res, err := inner(row)
				if err != nil {
					return false, err
				}
				return !res, nil
			}
		}
		return nil
	default:
		return nil
	}
}

// compileInExpr compiles a `col IN (lit1, lit2, ...)` predicate into
// a map lookup. Pre-computes the lookup map once at Filter creation
// so per-row evaluation is O(1) instead of O(N) comparisons.
// REQ001087. Only works when the IN-list contains only literal
// values (no subqueries or computed expressions).
func compileInExpr(e *PS.InExpr) func(*Row) (bool, error) {
	if e.Subquery != nil || len(e.List) == 0 {
		return nil
	}
	colName, ok := colRefName(e.Expr)
	if !ok {
		return nil
	}
	lookup := make(map[string]bool, len(e.List))
	for _, item := range e.List {
		lit, ok := extractLiteral(item)
		if !ok {
			return nil // non-constant expression; fall back to Eval
		}
		lookup[apValueKey(DT.ValueFromAny(lit))] = true
	}
	bareName := colName
	if dot := strings.LastIndexByte(colName, '.'); dot >= 0 {
		bareName = colName[dot+1:]
	}
	return func(row *Row) (bool, error) {
		idx := -1
		for i, c := range row.Cols {
			if strings.EqualFold(c, colName) {
				idx = i
				break
			}
		}
		if idx < 0 {
			for i, c := range row.Cols {
				if strings.EqualFold(c, bareName) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			lk := strings.ToLower(bareName)
			for i, c := range row.Cols {
				if strings.HasSuffix(strings.ToLower(c), "."+lk) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		if idx < 0 || idx >= len(row.Data) {
			return false, nil
		}
		return lookup[apValueKey(row.Data[idx])], nil
	}
}

// apValueKey returns a string key for a Value suitable for map lookup.
// The key includes the Kind prefix so different types never collide
// (e.g. int 1 != text "1"), and avoids []byte incomparability.
// REQ001087.
func apValueKey(v Value) string {
	switch v.Kind {
	case KindNull:
		return "NULL"
	case KindInt:
		return "I:" + strconv.FormatInt(v.I64, 10)
	case KindFloat:
		return "F:" + strconv.FormatFloat(v.F64, 'g', -1, 64)
	case KindText:
		return "T:" + v.S
	case KindBool:
		if v.Bo {
			return "B:true"
		}
		return "B:false"
	case KindBlob:
		return "BL:" + string(v.B)
	default:
		return ""
	}
}

func compileBinary(e *PS.BinaryExpr) func(*Row) (bool, error) {
	// Handle AND/OR by compiling both sides.
	if e.Op == LX.T_AND {
		left := compileFilterExpr(e.Left)
		right := compileFilterExpr(e.Right)
		if left == nil || right == nil {
			return nil
		}
		return func(row *Row) (bool, error) {
			lr, err := left(row)
			if err != nil || !lr {
				return false, err
			}
			return right(row)
		}
	}
	if e.Op == LX.T_OR {
		left := compileFilterExpr(e.Left)
		right := compileFilterExpr(e.Right)
		if left == nil || right == nil {
			return nil
		}
		return func(row *Row) (bool, error) {
			lr, err := left(row)
			if err != nil || lr {
				return lr, err
			}
			return right(row)
		}
	}

	// Simple comparisons: col OP literal
	col, literal, ok := extractColLiteralPair(e)
	if ok && literal != nil {
		colName := col
		litVal := literal

		switch e.Op {
		case LX.T_EQ:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.EqualValueValue(a, b)
			})
		case LX.T_NE:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				// SQL semantics: NULL compared with anything = UNKNOWN (drop row)
				// The NULL guard in makeCompiledCmp handles this for col and lit.
				// We just need to negate the equality result for non-NULL values.
				if a.IsNull() || b.IsNull() {
					return false
				}
				return !pl.EqualValueValue(a, b)
			})
		case LX.T_GT:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) > 0
			})
		case LX.T_GE:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) >= 0
			})
		case LX.T_LT:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) < 0
			})
		case LX.T_LE:
			return makeCompiledCmp(colName, litVal, func(a, b Value) bool {
				return pl.CompareValue(a, b) <= 0
			})
		}
	}

	// Equi-join: col OP col (both sides are column references).
	// This is the common pattern in multi-table joins like
	// "WHERE t1.a = t2.b AND t2.b = t3.c". Compiling these
	// eliminates Eval dispatch overhead in the hot join path.
	// REQ000802+: only compile when both columns are from the
	// same table or are bare names — cross-table qualified names
	// require the Eval path to resolve table aliases correctly.
	// Even with Outer-chain walking in findColIndex, correlated
	// subqueries (e.g., x.b<t1.b where x is an inner alias and t1
	// is the outer table) need the Eval path because the compiled
	// function caches column indices from the first row, which
	// might not have the Outer chain set up yet.
	leftCol, rightCol, ok := extractColColPair(e)
	if ok {
		leftDot := strings.LastIndexByte(leftCol, '.')
		rightDot := strings.LastIndexByte(rightCol, '.')
		if leftDot < 0 && rightDot < 0 {
			// Both are bare names — only compile if both columns
			// exist in the current row. Correlated subqueries where
			// one bare name resolves via the outer chain (e.g.
			// WHERE user_id = id where id is in the outer users row)
			// must NOT be compiled because makeCompiledColColCmp
			// reads from row.Data which belongs to the inner row
			// only. REQ000846.
			return nil
		}
		if leftDot >= 0 && rightDot >= 0 {
			// Both are qualified names. Only compile if they
			// reference the same table prefix. Cross-table
			// qualified names (including correlated subqueries
			// like x.b<t1.b) fall back to Eval.
			leftTbl := leftCol[:leftDot]
			rightTbl := rightCol[:rightDot]
			if leftTbl == rightTbl {
				return makeCompiledColColCmp(leftCol, rightCol, e.Op)
			}
			return nil
		}
		// Mixed (one qualified, one bare) — compile is safe
		// since the bare name resolves via the row's columns.
		if leftDot >= 0 || rightDot >= 0 {
			return makeCompiledColColCmp(leftCol, rightCol, e.Op)
		}
	}

	return nil
}

func makeCompiledCmp(colName string, litVal any, cmp func(a, b Value) bool) func(*Row) (bool, error) {
	bareName := colName
	if dot := strings.LastIndexByte(colName, '.'); dot >= 0 {
		bareName = colName[dot+1:]
	}
	// Pre-convert literal to Value to avoid boxing in hot path.
	litValue := DT.ValueFromAny(litVal)
	// REQ001084: idx must NOT be captured in closure — rows in
	// a batch may have different Cols (e.g. cross join produces
	// rows with varying prefixed columns between left/right sides).
	// A cached idx from row N can be wrong for row M in the same
	// batch. Recompute idx per row by linear scan — slower per row
	// but correct across heterogeneous row layouts.
	return func(row *Row) (bool, error) {
		// REQ001084: recompute idx every call. Cross-join batches
		// can have rows with different Cols between left/right
		// sides; a cached idx from a previous row would be wrong.
		idx := -1
		// Try direct match first (qualified name like "t1.a").
		for i, c := range row.Cols {
			if strings.EqualFold(c, colName) {
				idx = i
				break
			}
		}
		// Fallback: try bare column name (works for SeqScan rows).
		if idx < 0 {
			for i, c := range row.Cols {
				if strings.EqualFold(c, bareName) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		// Fallback: suffix match for bare names on prefixed rows.
		if idx < 0 {
			lk := strings.ToLower(bareName)
			for i, c := range row.Cols {
				if strings.HasSuffix(strings.ToLower(c), "."+lk) && i < len(row.Data) {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			return false, nil
		}
		if idx >= len(row.Data) {
			return false, nil
		}
		// SQL three-valued logic: NULL compared with anything = UNKNOWN.
		// Without this guard, DT.Compare() returns a non-zero ordering for
		// NULL values, causing the comparison to incorrectly evaluate
		// as true/false instead of NULL (filtered out by Filter).
		if isNullValueValue(row.Data[idx]) || isNullValueValue(litValue) {
			return false, nil
		}
		// Direct Value comparison — no boxing.
		return cmp(row.Data[idx], litValue), nil
	}
}

// extractColLiteralPair extracts (column_name, literal_value, ok) from a
// BinaryExpr where one side is a column reference and the other is a literal.
// REQ000802: supports both Ident (bare name) and QualifiedName (table.col).
func extractColLiteralPair(e *PS.BinaryExpr) (string, any, bool) {
	if col, ok := colRefName(e.Left); ok {
		if lit, ok := extractLiteral(e.Right); ok {
			return col, lit, true
		}
	}
	if col, ok := colRefName(e.Right); ok {
		if lit, ok := extractLiteral(e.Left); ok {
			return col, lit, true
		}
	}
	return "", nil, false
}

// colRefName returns the column name from an expression that is
// either an Ident or a QualifiedName, plus whether it succeeded.
func colRefName(e PS.Expr) (string, bool) {
	switch v := e.(type) {
	case *PS.Ident:
		return v.Name, true
	case *PS.QualifiedName:
		return v.Table + "." + v.Name, true
	}
	return "", false
}

// extractLiteral returns the Go value from a literal expression node.
func extractLiteral(e PS.Expr) (any, bool) {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return v.Val, true
	case *PS.FloatLiteral:
		return v.Val, true
	case *PS.StringLiteral:
		return v.Val, true
	case *PS.BoolLiteral:
		return v.Val, true
	case *PS.NullLiteral:
		return nil, true
	default:
		return nil, false
	}
}

// extractColColPair extracts (leftColName, rightColName, ok) from a
// BinaryExpr where both sides are column references (Ident or QualifiedName).
// This enables compilation of equi-join predicates like "t1.a = t2.b".
func extractColColPair(e *PS.BinaryExpr) (string, string, bool) {
	leftCol, leftOK := colRefName(e.Left)
	rightCol, rightOK := colRefName(e.Right)
	if leftOK && rightOK {
		return leftCol, rightCol, true
	}
	return "", "", false
}

// makeCompiledColColCmp builds a compiled comparison function for
// two column references. It handles both bare names ("a") and
// qualified names ("t1.a"), with fallbacks for prefixed rows.
// REQ001273: revalidates cached column indices on each call to
// handle rows with varying column layouts (cross-join output).
func makeCompiledColColCmp(leftCol, rightCol string, op LX.TokenType) func(*Row) (bool, error) {
	var leftIdx, rightIdx int = -1, -1
	var lastLeftRowLen, lastRightRowLen int

	return func(row *Row) (bool, error) {
		// Revalidate cached indices if row layout changed.
		if leftIdx >= 0 && (leftIdx >= len(row.Data) || len(row.Data) != lastLeftRowLen) {
			leftIdx = findColIndex(row, leftCol)
		}
		if rightIdx >= 0 && (rightIdx >= len(row.Data) || len(row.Data) != lastRightRowLen) {
			rightIdx = findColIndex(row, rightCol)
		}
		if leftIdx < 0 {
			leftIdx = findColIndex(row, leftCol)
		}
		if rightIdx < 0 {
			rightIdx = findColIndex(row, rightCol)
		}
		if leftIdx >= 0 {
			lastLeftRowLen = len(row.Data)
		}
		if rightIdx >= 0 {
			lastRightRowLen = len(row.Data)
		}

		// If either column is not found, the comparison yields false.
		if leftIdx < 0 || rightIdx < 0 || leftIdx >= len(row.Data) || rightIdx >= len(row.Data) {
			return false, nil
		}

		a, b := row.Data[leftIdx], row.Data[rightIdx]

		// SQL three-valued logic: NULL compared with anything = UNKNOWN.
		if isNullValue(a) || isNullValue(b) {
			return false, nil
		}

		switch op {
		case LX.T_EQ:
			eq, _ := DT.EqualValue(a, b)
			return eq, nil
		case LX.T_NE:
			eq, _ := DT.EqualValue(a, b)
			return !eq, nil
		case LX.T_GT:
			return DT.CompareValue(a, b) > 0, nil
		case LX.T_GE:
			return DT.CompareValue(a, b) >= 0, nil
		case LX.T_LT:
			return DT.CompareValue(a, b) < 0, nil
		case LX.T_LE:
			return DT.CompareValue(a, b) <= 0, nil
		}
		return false, nil
	}
}

// findColIndex finds the column index for a name in a row, handling
// bare names, qualified names, suffix matches for prefixed rows,
// and correlated subquery outer-row resolution.
func findColIndex(row *Row, name string) int {
	idx := findColIndexInRow(row, name)
	if idx >= 0 {
		return idx
	}
	// REQ000700: walk the outer chain for correlated subquery
	// resolution. The subquery's inner row may not contain the
	// outer-referenced column (e.g., t1.b when inner row is aliased
	// as x with columns [x.a, x.b, ...]).
	for cur := row.Outer; cur != nil; cur = cur.Outer {
		if nameHasTable(name) && cur.TableName != "" && !strings.EqualFold(cur.TableName, tableOfName(name)) {
			continue
		}
		idx = findColIndexInRow(cur, name)
		if idx >= 0 {
			return idx
		}
	}
	return -1
}

// nameHasTable reports whether name contains a table prefix.
func nameHasTable(name string) bool {
	return strings.IndexByte(name, '.') >= 0
}

// tableOfName returns the table prefix of "t.col" (returns "t");
// returns "" if name has no table prefix.
func tableOfName(name string) string {
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		return name[:dot]
	}
	return ""
}

// findColIndexInRow searches a single row for the column name.
// Does not walk the Outer chain — use findColIndex for that.
// REQ000816: skip strings.ToLower when name is already lowercase.
func findColIndexInRow(row *Row, name string) int {
	if row == nil {
		return -1
	}
	// Fast path: skip ToLower when name is already lowercase.
	lower := name
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			lower = strings.ToLower(name)
			break
		}
	}
	bareName := name
	hasDot := false
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		bareName = name[dot+1:]
		hasDot = true
	}
	bareLower := bareName
	if lower != name {
		// If name had uppercase, we need ToLower for bareLower too.
		bareLower = strings.ToLower(bareName)
	}

	// For qualified names (containing dot), prefer exact linear scan
	// to avoid colIndex's duplicate-key issue in self-joins where
	// both sides produce columns with the same qualified prefix.
	if hasDot {
		for i, c := range row.Cols {
			if strings.EqualFold(c, name) && i < len(row.Data) {
				return i
			}
		}
	}

	// Fast path: use colIndex map (safe for bare names or when
	// the qualified name wasn't found via linear scan).
	if row.ColIndex != nil {
		if idx, ok := row.ColIndex[lower]; ok && idx < len(row.Data) {
			return idx
		}
		if idx, ok := row.ColIndex[bareLower]; ok && idx < len(row.Data) {
			return idx
		}
	}

	// Linear scan for exact match (bare or qualified).
	for i, c := range row.Cols {
		cl := strings.ToLower(c)
		if cl == lower || cl == bareLower {
			if i < len(row.Data) {
				return i
			}
			return -1
		}
	}

	// Suffix match for bare names on prefixed rows (e.g., "a" matches "t1.a").
	for i, c := range row.Cols {
		if strings.HasSuffix(strings.ToLower(c), "."+bareLower) && i < len(row.Data) {
			return i
		}
	}

	return -1
}

// compileRowExpr compiles a single SELECT expression into a function
// that reads directly from the input row and returns a Value.
// Returns nil for unrecognized patterns (caller falls back to Eval).
func compileRowExpr(e PS.Expr) func(*Row) Value {
	switch v := e.(type) {
	case *PS.QualifiedName:
		return compileColRef(v.Table+"."+v.Name, v.SlotIdx)
	case *PS.Ident:
		return compileColRef(v.Name, v.SlotIdx)
	case *PS.NumberLiteral:
		return compileConstInt(v.Val)
	case *PS.AliasedExpr:
		return compileRowExpr(v.Expr)
	case *PS.BinaryExpr:
		return compileBinaryArith(v)
	default:
		return nil
	}
}

// compileConstInt compiles a constant integer literal.
func compileConstInt(val int64) func(*Row) Value {
	return func(*Row) Value {
		return Value{Kind: KindInt, I64: val}
	}
}

// compileProjectBool compiles an expression to a bool-returning closure
// suitable for CASE WHEN conditions. NULL values return false.
func compileProjectBool(e PS.Expr) func(*Row) bool {
	switch v := e.(type) {
	case *PS.BinaryExpr:
		switch v.Op {
		case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
			return compileProjectCmp(v)
		case LX.T_AND:
			return compileProjectAnd(v)
		case LX.T_OR:
			return compileProjectOr(v)
		}
	case *PS.NumberLiteral:
		// Non-zero constant is always true (SQL truthiness)
		if v.Val != 0 {
			return func(*Row) bool { return true }
		}
		return func(*Row) bool { return false }
	}
	return nil
}

// compileProjectCmp compiles a comparison binary expression (col OP literal,
// col OP col, or general expr OP expr) into a bool closure. NULL → false.
func compileProjectCmp(v *PS.BinaryExpr) func(*Row) bool {
	col, lit, ok := extractColLiteralPair(v)
	if ok {
		return makeProjectCmp(col, lit, v.Op)
	}
	leftCol, rightCol, ok := extractColColPair(v)
	if ok {
		return makeProjectColColCmp(leftCol, rightCol, v.Op)
	}
	// General case: compile both sides as Value expressions, compare results.
	// Handles patterns like a<b-3 where one side is an arithmetic expression.
	left := compileRowExpr(v.Left)
	right := compileRowExpr(v.Right)
	if left == nil || right == nil {
		return nil
	}
	return makeProjectExprCmp(left, right, v.Op)
}

// makeProjectCmp compiles col OP literal comparison.
func makeProjectCmp(colName string, litVal any, op LX.TokenType) func(*Row) bool {
	litValue := DT.ValueFromAny(litVal)
	return func(row *Row) bool {
		idx := findColIndex(row, colName)
		if idx < 0 || idx >= len(row.Data) {
			return false
		}
		v := row.Data[idx]
		if v.IsNull() || litValue.IsNull() {
			return false
		}
		switch op {
		case LX.T_EQ:
			eq, _ := DT.EqualValue(v, litValue)
			return eq
		case LX.T_NE:
			eq, _ := DT.EqualValue(v, litValue)
			return !eq
		case LX.T_LT:
			return DT.CompareValue(v, litValue) < 0
		case LX.T_LE:
			return DT.CompareValue(v, litValue) <= 0
		case LX.T_GT:
			return DT.CompareValue(v, litValue) > 0
		case LX.T_GE:
			return DT.CompareValue(v, litValue) >= 0
		}
		return false
	}
}

// makeProjectColColCmp compiles col OP col comparison.
func makeProjectColColCmp(leftCol, rightCol string, op LX.TokenType) func(*Row) bool {
	return func(row *Row) bool {
		leftIdx := findColIndex(row, leftCol)
		rightIdx := findColIndex(row, rightCol)
		if leftIdx < 0 || rightIdx < 0 || leftIdx >= len(row.Data) || rightIdx >= len(row.Data) {
			return false
		}
		a, b := row.Data[leftIdx], row.Data[rightIdx]
		if a.IsNull() || b.IsNull() {
			return false
		}
		switch op {
		case LX.T_EQ:
			eq, _ := DT.EqualValue(a, b)
			return eq
		case LX.T_NE:
			eq, _ := DT.EqualValue(a, b)
			return !eq
		case LX.T_LT:
			return DT.CompareValue(a, b) < 0
		case LX.T_LE:
			return DT.CompareValue(a, b) <= 0
		case LX.T_GT:
			return DT.CompareValue(a, b) > 0
		case LX.T_GE:
			return DT.CompareValue(a, b) >= 0
		}
		return false
	}
}

// compileProjectAnd compiles AND by chaining two bool closures.
func compileProjectAnd(v *PS.BinaryExpr) func(*Row) bool {
	left := compileProjectBool(v.Left)
	right := compileProjectBool(v.Right)
	if left == nil || right == nil {
		return nil
	}
	return func(row *Row) bool {
		return left(row) && right(row)
	}
}

// compileProjectOr compiles OR by chaining two bool closures.
func compileProjectOr(v *PS.BinaryExpr) func(*Row) bool {
	left := compileProjectBool(v.Left)
	right := compileProjectBool(v.Right)
	if left == nil || right == nil {
		return nil
	}
	return func(row *Row) bool {
		return left(row) || right(row)
	}
}

// makeProjectExprCmp compares two Value expressions. Handles patterns like
// a<b-3 where each side is a compiled row expression.
func makeProjectExprCmp(left, right func(*Row) Value, op LX.TokenType) func(*Row) bool {
	return func(row *Row) bool {
		a, b := left(row), right(row)
		if a.IsNull() || b.IsNull() {
			return false
		}
		switch op {
		case LX.T_EQ:
			eq, _ := DT.EqualValue(a, b)
			return eq
		case LX.T_NE:
			eq, _ := DT.EqualValue(a, b)
			return !eq
		case LX.T_LT:
			return DT.CompareValue(a, b) < 0
		case LX.T_LE:
			return DT.CompareValue(a, b) <= 0
		case LX.T_GT:
			return DT.CompareValue(a, b) > 0
		case LX.T_GE:
			return DT.CompareValue(a, b) >= 0
		}
		return false
	}
}

// compileProjectCaseExpr compiles CASE WHEN to a closure chain.
// Falls back to Eval for complex patterns (simple CASE with Expr, subqueries).
func compileProjectCaseExpr(e *PS.CaseExpr) func(*Row) (Value, error) {
	// Simple CASE (CASE x WHEN a THEN b) — fall back to Eval for now.
	if e.Expr != nil {
		return compileProjectCaseFallback(e)
	}
	type whenClause struct {
		cond func(*Row) bool
		then func(*Row) Value
	}
	clauses := make([]whenClause, 0, len(e.WhenList))
	for _, w := range e.WhenList {
		cond := compileProjectBool(w.Cond)
		then := compileRowExpr(w.Then)
		if cond == nil || then == nil {
			return compileProjectCaseFallback(e)
		}
		clauses = append(clauses, whenClause{cond, then})
	}
	var elseFn func(*Row) Value
	if e.Else != nil {
		elseFn = compileRowExpr(e.Else)
		if elseFn == nil {
			return compileProjectCaseFallback(e)
		}
	}
	return func(row *Row) (Value, error) {
		for _, c := range clauses {
			if c.cond(row) {
				return c.then(row), nil
			}
		}
		if elseFn != nil {
			return elseFn(row), nil
		}
		return Value{}, nil
	}
}

// compileProjectCaseFallback returns a closure that uses EV.EvalValue
// for CASE expressions that cannot be compiled.
func compileProjectCaseFallback(e *PS.CaseExpr) func(*Row) (Value, error) {
	return func(row *Row) (Value, error) {
		return EV.EvalValue(e, row, nil)
	}
}

// compileProjectExprs compiles SELECT expressions into fast-path
// evaluators that read directly from row.Data, bypassing Eval
// dispatch and Value↔any boxing. REQ000802.
func (p *Project) compileProjectExprs() {
	p.compiledExprs = make([]func(*Row) (Value, error), len(p.cols))
	for i, c := range p.cols {
		switch e := c.(type) {
		case *PS.FunctionCall:
			p.compiledExprs[i] = compileProjectFuncCall(e)
		case *PS.CaseExpr:
			p.compiledExprs[i] = compileProjectCaseExpr(e)
		default:
			if fn := compileRowExpr(c); fn != nil {
				fn2 := fn
				p.compiledExprs[i] = func(row *Row) (Value, error) {
					return fn2(row), nil
				}
			}
		}
	}
	// REQ001282: pre-allocate dataBuf to child's estimated row count
	// to avoid repeated doubling reallocations for large result sets.
	if est := estimateChildRowCount(p.child); est > 0 {
		needed := int(est) * p.dataPerRow
		if cap(p.dataBuf) < needed {
			newBuf := make([]Value, len(p.dataBuf), needed)
			copy(newBuf, p.dataBuf)
			p.dataBuf = newBuf
		}
	}
}

// estimateChildRowCount returns an approximate row count from the
// child operator, or -1 if unknown. For SeqScan with a store, it
// counts keys by iterating the store once. REQ001282.
func estimateChildRowCount(child Operator) int {
	ss, ok := child.(*SeqScan)
	if !ok || ss == nil {
		return -1
	}
	store := ss.Store()
	if store == nil {
		return -1
	}
	iter := store.NewIterator(nil)
	if iter == nil {
		return -1
	}
	defer iter.Close()
	count := 0
	for iter.Next() {
		count++
	}
	if err := iter.Err(); err != nil {
		return -1
	}
	return count
}

// compileBinaryArith compiles a binary arithmetic expression (+-*/ and DIV)
// into a function that reads directly from the input row.
func compileBinaryArith(v *PS.BinaryExpr) func(*Row) Value {
	if v.Op != LX.T_PLUS && v.Op != LX.T_MINUS &&
		v.Op != LX.T_STAR && v.Op != LX.T_SLASH && v.Op != LX.T_DIV {
		return nil
	}
	left := compileRowExpr(v.Left)
	right := compileRowExpr(v.Right)
	if left == nil || right == nil {
		return nil
	}
switch v.Op {
	case LX.T_PLUS:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 + b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) + valueToFloat(b)}
		}
	case LX.T_MINUS:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 - b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) - valueToFloat(b)}
		}
	case LX.T_STAR:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 * b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) * valueToFloat(b)}
		}
	case LX.T_SLASH:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindInt && b.I64 == 0 {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindFloat && b.F64 == 0 {
				return Value{Kind: KindNull}
			}
			if a.Kind == KindInt && b.Kind == KindInt {
				return Value{Kind: KindInt, I64: a.I64 / b.I64}
			}
			return Value{Kind: KindFloat, F64: valueToFloat(a) / valueToFloat(b)}
		}
	case LX.T_DIV:
		return func(row *Row) Value {
			a, b := left(row), right(row)
			if a.IsNull() || b.IsNull() {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindInt && b.I64 == 0 {
				return Value{Kind: KindNull}
			}
			if b.Kind == KindFloat && b.F64 == 0 {
				return Value{Kind: KindNull}
			}
			var ai, bi int64
			switch a.Kind {
			case KindInt:
				ai = a.I64
			case KindFloat:
				ai = int64(a.F64)
			default:
				return Value{Kind: KindNull}
			}
			switch b.Kind {
			case KindInt:
				bi = b.I64
			case KindFloat:
				bi = int64(b.F64)
			default:
				return Value{Kind: KindNull}
			}
			if bi == 0 {
				return Value{Kind: KindNull}
			}
			return Value{Kind: KindInt, I64: ai / bi}
		}
	}
	return nil
}

// compileColRef compiles a column reference (bare or qualified name)
// into a function that reads directly from row.Data.
// REQ000898: caches the column index on first lookup so repeated
// calls (across rows in a batch) use O(1) direct index access.
func compileColRef(name string, slotIdx int) func(*Row) Value {
	lower := strings.ToLower(name)
	bareName := name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		bareName = name[dot+1:]
	}
	bareLower := strings.ToLower(bareName)
	return func(row *Row) Value {
		if slotIdx >= 0 && slotIdx < len(row.Data) && slotIdx < len(row.Cols) {
			cl := row.Cols[slotIdx]
			if len(cl) > 0 {
				if strings.EqualFold(cl, lower) || strings.EqualFold(cl, bareLower) {
					return row.Data[slotIdx]
				}
			}
		}
		if row.ColIndex != nil {
			if i, ok := row.ColIndex[lower]; ok && i < len(row.Data) {
				return row.Data[i]
			}
		}
		for i, c := range row.Cols {
			cl := strings.ToLower(c)
			if cl == lower || cl == bareLower {
				if i < len(row.Data) {
					return row.Data[i]
				}
				return Value{Kind: KindNull}
			}
		}
		for i, c := range row.Cols {
			if strings.HasSuffix(strings.ToLower(c), "."+bareLower) && i < len(row.Data) {
				return row.Data[i]
			}
		}
		return Value{Kind: KindNull}
	}
}

// compileProjectFuncCall compiles a function call expression into a
// closure that calls EvalFunction directly, bypassing the top-level
// EvalValue type-switch. REQ001291.
func compileProjectFuncCall(e *PS.FunctionCall) func(*Row) (Value, error) {
	return func(row *Row) (Value, error) {
		return EV.EvalFunction(e, row, nil)
	}
}

// valueToFloat converts a Value to float64 for mixed-type arithmetic.
func valueToFloat(v Value) float64 {
	switch v.Kind {
	case KindInt:
		return float64(v.I64)
	case KindFloat:
		return v.F64
	default:
		return 0
	}
}