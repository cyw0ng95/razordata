package EX

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// globalSubqueryCache caches results of non-correlated scalar subqueries
// across all executions. Keyed by serialized statement.
// This eliminates O(N) subquery re-evaluations for N-row result sets.
var globalSubqueryCache sync.Map

// correlatedSubqueryCache is an LRU cache for correlated scalar subqueries.
// Key = "planKey:outerPKValues" (e.g., "SELECT...:1,5,10").
// Values are cached subquery results (any).
// Max size 256 entries to bound memory.
var correlatedSubqueryCache = newLRUCache(256)

// lruCache is a simple thread-safe LRU cache.
type lruCache struct {
	mu        sync.Mutex
	items     map[string]*lruEntry
	order     []string
	maxSize   int
	evictions int64
}

type lruEntry struct {
	value any
}

func newLRUCache(maxSize int) *lruCache {
	return &lruCache{
		items:   make(map[string]*lruEntry),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

func (c *lruCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return nil, false
	}
	// Move to end (most recently used)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			break
		}
	}
	return entry.value, true
}

func (c *lruCache) Put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.items[key]; ok {
		entry.value = value
		// Move to end
		for i, k := range c.order {
			if k == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				c.order = append(c.order, key)
				break
			}
		}
		return
	}
	// Evict if at capacity
	if len(c.items) >= c.maxSize {
		oldest := c.order[0]
		delete(c.items, oldest)
		c.order = c.order[1:]
		c.evictions++
	}
	c.items[key] = &lruEntry{value: value}
	c.order = append(c.order, key)
}

func (c *lruCache) Stats() (size int, evictions int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.evictions
}

// ClearSubqueryCaches clears both the global and correlated subquery caches.
// Called by UnregisterAll() for test isolation.
func ClearSubqueryCaches() {
	globalSubqueryCache = sync.Map{}
	correlatedSubqueryCache = newLRUCache(256)
}

// serializeOuterRow serializes the outer row's column values for use
// as a cache key component in correlated subquery caching.
// Format: "col1Val1,col2Val2,..." using normalized string representation.
func serializeOuterRow(row *Row) string {
	if row == nil || len(row.Cols) == 0 {
		return ""
	}
	var parts []string
	for _, col := range row.Cols {
		if v, ok := row.Lookup(col); ok {
			parts = append(parts, fmt.Sprint(v))
		} else {
			parts = append(parts, "NULL")
		}
	}
	return strings.Join(parts, ",")
}

var ErrEval = errors.New("ex: eval error")
var ErrDivByZero = errors.New("ex: division by zero")
var ErrTypeMismatch = errors.New("ex: type mismatch")
var ErrSubquery = errors.New("ex: subquery not supported here")
var ErrIgnoreRow = errors.New("ex: ignore row")

func Eval(expr PS.Expr, row *Row, params []any) (any, error) {
	if expr == nil {
		return nil, nil
	}

	switch e := expr.(type) {
	case *PS.NumberLiteral:
		return e.Val, nil
	case *PS.FloatLiteral:
		return e.Val, nil
	case *PS.StringLiteral:
		return e.Val, nil
	case *PS.BoolLiteral:
		return e.Val, nil
	case *PS.NullLiteral:
		return nil, nil
	case *PS.Ident:
		if row != nil {
			if v, ok := row.Lookup(e.Name); ok {
				return v, nil
			}
		}
		return e.Name, nil
	case *PS.QualifiedName:
		if row != nil {
			// REQ000755: Use O(1) Lookup instead of O(N) linear scan.
			// Try qualified name first (table.col), then bare name.
			if e.CachedKey == "" {
				e.CachedKey = e.Table + "." + e.Name
			}
			for cur := row; cur != nil; cur = cur.Outer {
				if v, ok := cur.Lookup(e.CachedKey); ok {
					return v, nil
				}
			}
			// REQ000700: walk the outer chain, preferring rows
			// whose tableName matches the QualifiedName's table
			// prefix. This ensures t.g resolves to the outer row
			// from table t, not a same-named column from the
			// current subquery row (table s).
			for cur := row.Outer; cur != nil; cur = cur.Outer {
				if cur.tableName != "" && !strings.EqualFold(cur.tableName, e.Table) {
					continue
				}
				if v, ok := cur.Lookup(e.Name); ok {
					return v, nil
				}
			}
			// Current row: last resort for bare-name match.
			if v, ok := row.Lookup(e.Name); ok {
				return v, nil
			}
		}
		return e.CachedKey, nil
	case *PS.Param:
		if e.Index < len(params) {
			return normalizeInt(params[e.Index]), nil
		}
		return nil, nil
	case *PS.StarExpr:
		return "*", nil
	case *PS.UnaryExpr:
		return evalUnary(e, row, params)
	case *PS.BinaryExpr:
		return evalBinary(e, row, params)
	case *PS.ListExpr:
		return e.Items, nil
	case *PS.BetweenExpr:
		return evalBetween(e, row, params)
	case *PS.InExpr:
		return evalIn(e, row, params)
	case *PS.ExistsExpr:
		return evalExists(e, row, params)
	case *PS.SubqueryExpr:
		return evalScalarSubquery(e, row, params)
	case *PS.IntervalLiteral:
		return evalInterval(e)
	case *PS.CaseExpr:
		return evalCase(e, row, params)
	case *PS.AggregateFunc:
		return evalAggregate(e, row, params)
	case *PS.WindowFunc:
		return evalWindowFunc(e, row, params)
	case *PS.FunctionCall:
		return evalFunction(e, row, params)
	case *PS.RaiseFunc:
		return evalRaise(e, row, params)
	case *PS.CastExpr:
		return evalCast(e, row, params)
	case *PS.AliasedExpr:
		return Eval(e.Expr, row, params)
	default:
		return nil, ErrEval
	}
}

func evalUnary(e *PS.UnaryExpr, row *Row, params []any) (any, error) {
	operand, err := Eval(e.Operand, row, params)
	if err != nil {
		return nil, err
	}

	switch e.Op {
	case int(LX.T_MINUS):
		if operand == nil {
			return nil, nil
		}
		switch v := operand.(type) {
		case int64:
			return -v, nil
		case float64:
			return -v, nil
		}
		// REQ000719: handle non-int64 numeric types (e.g. Go's `int`
		// on 32-bit paths, store-deserialized int from int columns
		// when the underlying bit-width differs). Convert to int64
		// or float64 via toInt64/numericFloat before applying the
		// unary minus, so the driver/store path agrees with the
		// in-memory path.
		if n, ok := toInt64(operand); ok {
			return -n, nil
		}
		if f, ok := numericFloat(operand); ok {
			return -f, nil
		}
	case int(LX.T_PLUS):
		return operand, nil
	case int(LX.T_NOT):
		if operand == nil {
			return nil, nil
		}
		return !truthy(operand), nil
	case int(LX.T_BITNOT):
		if v, ok := toInt64(operand); ok {
			return ^v, nil
		}
	}
	return nil, ErrEval
}

func evalBinary(e *PS.BinaryExpr, row *Row, params []any) (any, error) {
	left, err := Eval(e.Left, row, params)
	if err != nil {
		return nil, err
	}

	// Short-circuit AND/OR: defer right-side evaluation until
	// we know it's needed. All other operators need both sides.
	if e.Op == int(LX.T_AND) {
		if b, ok := left.(bool); ok && !b {
			return false, nil
		}
		right, err := Eval(e.Right, row, params)
		if err != nil {
			return nil, err
		}
		return band(left, right)
	}
	if e.Op == int(LX.T_OR) {
		if b, ok := left.(bool); ok && b {
			return true, nil
		}
		right, err := Eval(e.Right, row, params)
		if err != nil {
			return nil, err
		}
		return bor(left, right)
	}

	right, err := Eval(e.Right, row, params)
	if err != nil {
		return nil, err
	}

	switch e.Op {
	case int(LX.T_EQ):
		// SQL semantics: NULL compared with anything = NULL
		if left == nil || right == nil {
			return nil, nil
		}
		return equalValue(left, right), nil
	case int(LX.T_NE):
		if left == nil || right == nil {
			return nil, nil
		}
		return !equalValue(left, right), nil
	case int(LX.T_LT):
		// REQ000445: SQL three-valued logic — comparison
		// with NULL yields UNKNOWN, not FALSE. Returning
		// nil makes the WHERE filter drop the row.
		if left == nil || right == nil {
			return nil, nil
		}
		return compare(left, right) < 0, nil
	case int(LX.T_LE):
		if left == nil || right == nil {
			return nil, nil
		}
		return compare(left, right) <= 0, nil
	case int(LX.T_GT):
		if left == nil || right == nil {
			return nil, nil
		}
		return compare(left, right) > 0, nil
	case int(LX.T_GE):
		if left == nil || right == nil {
			return nil, nil
		}
		return compare(left, right) >= 0, nil
	case int(LX.T_PLUS):
		if _, ok := right.(*IntervalValue); ok {
			if ls, lok := left.(string); lok {
				if _, lok2 := ParseDateTime(ls); lok2 {
					return DateTimeArithmetic(left, right, "+")
				}
			}
		}
		if _, ok := left.(*IntervalValue); ok {
			if rs, rok := right.(string); rok {
				if _, rok2 := ParseDateTime(rs); rok2 {
					return DateTimeArithmetic(right, left, "+")
				}
			}
		}
		return add(left, right)
	case int(LX.T_MINUS):
		if _, ok := right.(*IntervalValue); ok {
			return DateTimeArithmetic(left, right, "-")
		}
		if ls, lok := left.(string); lok {
			if _, lok2 := ParseDateTime(ls); lok2 {
				if _, rok := toTime(right); rok {
					return DateTimeArithmetic(left, right, "-")
				}
			}
		}
		return sub(left, right)
	case int(LX.T_STAR):
		return mul(left, right)
	case int(LX.T_SLASH):
		return div(left, right)
	case int(LX.T_MOD):
		return mod(left, right)
	case int(LX.T_BITAND):
		return bitand(left, right)
	case int(LX.T_BITOR):
		return bitor(left, right)
	case int(LX.T_BITXOR):
		return bitxor(left, right)
	case int(LX.T_LSHIFT):
		return lshift(left, right)
	case int(LX.T_RSHIFT):
		return rshift(left, right)
	case int(LX.T_CONCAT):
		return concat(left, right)
	case int(LX.T_LIKE):
		{
			var esc string
			if e.Escape != nil {
				v, err := Eval(e.Escape, row, params)
				if err != nil {
					return nil, err
				}
				if v != nil {
					s, ok := v.(string)
					if !ok || len(s) != 1 {
						return nil, ErrEval
					}
					esc = s
				}
			}
			return like(left, right, esc)
		}
	case int(LX.T_GLOB):
		return glob(left, right)
	case int(LX.T_DIV):
		return intdiv(left, right)
	case int(LX.T_IS):
		// `x IS NOT NULL` parses as BinaryExpr{T_IS, x, UnaryExpr{T_NOT, NULL}}.
		// Detect this and return the IS NOT NULL predicate semantics
		// directly so the rewriter's fold of `NOT NULL` does not break
		// the predicate. See REQ000361.
		if u, ok := e.Right.(*PS.UnaryExpr); ok && u.Op == int(LX.T_NOT) {
			if _, isNull := u.Operand.(*PS.NullLiteral); isNull {
				if left == nil {
					return false, nil
				}
				return true, nil
			}
		}
		return is(left, right)
	}
	return nil, ErrEval
}

func evalBetween(e *PS.BetweenExpr, row *Row, params []any) (any, error) {
	expr, err := Eval(e.Expr, row, params)
	if err != nil {
		return nil, err
	}
	low, err := Eval(e.Low, row, params)
	if err != nil {
		return nil, err
	}
	high, err := Eval(e.High, row, params)
	if err != nil {
		return nil, err
	}
	if expr == nil || low == nil || high == nil {
		return nil, nil
	}
	cmpLow := compare(expr, low)
	cmpHigh := compare(expr, high)
	return cmpLow >= 0 && cmpHigh <= 0, nil
}

func evalIn(e *PS.InExpr, row *Row, params []any) (any, error) {
	target, err := Eval(e.Expr, row, params)
	if err != nil {
		return nil, err
	}
	if e.Subquery != nil {
		// REQ000721: when target is NULL, route through evalInSubquery
		// so it can apply three-valued logic — return NULL only if the
		// subquery contains a NULL, otherwise return false.
		return evalInSubquery(target, e.Subquery, row, params)
	}
	if len(e.List) == 0 {
		return false, nil
	}
	if target == nil {
		return nil, nil
	}
	hadNull := false
	for _, item := range e.List {
		v, err := Eval(item, row, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			hadNull = true
			continue
		}
		if equalValue(target, v) {
			return true, nil
		}
	}
	if hadNull {
		return nil, nil
	}
	return false, nil
}

func evalInSubquery(target any, subq PS.Stmt, outer *Row, params []any) (any, error) {
	sel, ok := subq.(*PS.Select)
	if !ok {
		return nil, ErrSubquery
	}
	pl, err := newSubqueryPlanner(outer).Plan(sel)
	if err != nil {
		return nil, err
	}
	rows, err := runSubqueryPlan(pl, outer, params)
	if err != nil {
		return nil, err
	}
	// REQ000721: when the LHS is NULL, the IN predicate is
	// three-valued. The correct result is:
	//   - true  if any subquery row is non-NULL and equal to NULL's
	//           *typed* value (impossible — NULL is not equal to
	//           anything in SQL two-valued-or-UNKNOWN logic)
	//   - NULL  if any subquery row is NULL
	//   - false otherwise (no NULLs in subquery, NULL != any value)
	// Previously the code returned nil (NULL) for any target==nil,
	// which conflates the no-NULLs case with the has-NULLs case.
	if target == nil {
		hadNull := false
		for _, row := range rows {
			if len(row.Cols) == 0 {
				continue
			}
			if row.Data[0] == nil {
				hadNull = true
			}
		}
		if hadNull {
			return nil, nil
		}
		return false, nil
	}
	hadNull := false
	for _, row := range rows {
		if len(row.Cols) == 0 {
			continue
		}
		if row.Data[0] == nil {
			hadNull = true
			continue
		}
		if equalValue(target, row.Data[0]) {
			return true, nil
		}
	}
	if hadNull {
		return nil, nil
	}
	return false, nil
}

// EvalForTest exposes Eval for tests; do not use in production
// code paths where the row may not be valid.
func EvalForTest(e PS.Expr, row *Row, params []any) (any, error) {
	return Eval(e, row, params)
}

// currentSubqueryPlanner is set by the executor before evaluating
// a query and read by newSubqueryPlanner when no outer row
// carries a planner. Used to support top-level non-correlated
// subqueries (`SELECT EXISTS(SELECT 1 FROM s WHERE v = 2)`)
// which have no outer row but still need the executor's
// store-backed planner. See REQ000366.
// Not goroutine-safe: only the executor's owning goroutine
// should set/clear this for the duration of a single query.
// Concurrent queries on the same engine are serialized by the
// executor's own locking (see SYS/SY).
var currentSubqueryPlanner *Planner

// newSubqueryPlanner returns a planner for evaluating a subquery
// inside Eval. Priority order:
//  1. Row's ExecContext (REQ000586 — eliminates global)
//  2. Row's outer-chain planner (REQ000366)
//  3. Package-level currentSubqueryPlanner (legacy fallback)
//  4. Fresh in-memory planner (tests without a store)
func newSubqueryPlanner(outer *Row) *Planner {
	// Check ExecContext first (REQ000586).
	if ec := ExecContextFromRow(outer); ec != nil && ec.Planner != nil {
		return ec.Planner
	}
	if p := outer.Planner(); p != nil {
		return p
	}
	if currentSubqueryPlanner != nil {
		return currentSubqueryPlanner
	}
	return NewPlanner()
}

func evalExists(e *PS.ExistsExpr, outer *Row, params []any) (any, error) {
	sel, ok := e.Subquery.(*PS.Select)
	if !ok {
		return nil, ErrSubquery
	}
	pl, err := newSubqueryPlanner(outer).Plan(sel)
	if err != nil {
		return nil, err
	}
	rows, err := runSubqueryPlan(pl, outer, params)
	if err != nil {
		return nil, err
	}
	return len(rows) > 0, nil
}

func evalScalarSubquery(e *PS.SubqueryExpr, outer *Row, params []any) (any, error) {
	sel, ok := e.Subquery.(*PS.Select)
	if !ok {
		return nil, ErrSubquery
	}

	// Compute cache key from the serialized statement.
	// Non-correlated subqueries (no outer column references)
	// are cached globally to avoid O(N) re-evaluations.
	key := serializeKey(sel)

	// Try global cache first — only safe when outer is nil
	// (no outer columns the subquery could reference).
	if outer == nil {
		if cached, ok := globalSubqueryCache.Load(key); ok {
			return cached, nil
		}
	} else {
		// P0-1: Try correlated subquery LRU cache.
		// Key = planKey + outer row values (e.g., "SELECT...:1,5,10").
		correlatedKey := key + ":" + serializeOuterRow(outer)
		if cached, ok := correlatedSubqueryCache.Get(correlatedKey); ok {
			return cached, nil
		}
	}

	pl, err := newSubqueryPlanner(outer).Plan(sel)
	if err != nil {
		return nil, err
	}
	rows, err := runSubqueryPlan(pl, outer, params)
	if err != nil {
		return nil, err
	}
	var result any
	if len(rows) == 0 {
		result = nil
	} else if len(rows[0].Data) == 0 {
		result = nil
	} else {
		result = rows[0].Data[0]
	}
	// Cache the result: globally for non-correlated, LRU for correlated.
	if outer == nil {
		globalSubqueryCache.Store(key, result)
	} else {
		correlatedSubqueryCache.Put(key+":"+serializeOuterRow(outer), result)
	}
	return result, nil
}

func evalInterval(e *PS.IntervalLiteral) (any, error) {
	n, unit, ok := ParseInterval(e.Value + " " + e.Unit)
	if !ok {
		return nil, fmt.Errorf("invalid interval: %s %s", e.Value, e.Unit)
	}
	return &IntervalValue{Amount: n, Unit: unit}, nil
}

func evalCast(e *PS.CastExpr, row *Row, params []any) (any, error) {
	v, err := Eval(e.Expr, row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	if e.Type == nil {
		return v, nil
	}
	switch LX.TokenType(e.Type.Type) {
	case LX.T_INT_KW, LX.T_BIGINT:
		switch x := v.(type) {
		case int64:
			return x, nil
		case float64:
			return int64(x), nil
		case string:
			n, err := strconv.ParseInt(x, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("ex: cast %q to int: %w", x, err)
			}
			return n, nil
		case bool:
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		}
	case LX.T_FLOAT_KW:
		switch x := v.(type) {
		case int64:
			return float64(x), nil
		case float64:
			return x, nil
		case string:
			f, err := strconv.ParseFloat(x, 64)
			if err != nil {
				return nil, fmt.Errorf("ex: cast %q to float: %w", x, err)
			}
			return f, nil
		}
	case LX.T_TEXT:
		return fmt.Sprintf("%v", v), nil
	case LX.T_DECIMAL, LX.T_NUMERIC:
		return evalDecimalCast(v, e.Type.Precision, e.Type.Scale)
	case LX.T_BOOL:
		return castToBool(v), nil
	case LX.T_BLOB:
		switch x := v.(type) {
		case string:
			return []byte(x), nil
		case []byte:
			return x, nil
		default:
			return []byte(fmt.Sprintf("%v", v)), nil
		}
	}
	return nil, ErrEval
}

func evalCase(e *PS.CaseExpr, row *Row, params []any) (any, error) {
	if e.Expr != nil {
		target, err := Eval(e.Expr, row, params)
		if err != nil {
			return nil, err
		}
		for _, w := range e.WhenList {
			v, err := Eval(w.Cond, row, params)
			if err != nil {
				return nil, err
			}
			if equalValue(target, v) {
				return Eval(w.Then, row, params)
			}
		}
	} else {
		for _, w := range e.WhenList {
			cond, err := Eval(w.Cond, row, params)
			if err != nil {
				return nil, err
			}
			if truthy(cond) {
				return Eval(w.Then, row, params)
			}
		}
	}
	if e.Else != nil {
		return Eval(e.Else, row, params)
	}
	return nil, nil
}

func truthy(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	if i, ok := v.(int64); ok {
		return i != 0
	}
	if f, ok := v.(float64); ok {
		return f != 0
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

// castToBool implements SQLite-compatible CAST AS BOOLEAN semantics
// (REQ000607). Unlike truthy() — which returns true for any
// non-empty string ("false", "abc", "0", …) — castToBool parses the
// string content: "0", "false", "FALSE", and "" are false; every
// other non-empty string is true. []byte is checked by its first
// byte (0x00 = false). Numbers follow the standard 0 = false rule.
func castToBool(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	if i, ok := v.(int64); ok {
		return i != 0
	}
	if f, ok := v.(float64); ok {
		return f != 0
	}
	if s, ok := v.(string); ok {
		if s == "" || s == "0" || s == "false" || s == "FALSE" {
			return false
		}
		return true
	}
	if b, ok := v.([]byte); ok && len(b) > 0 {
		return b[0] != 0
	}
	return true
}

func evalAggregate(e *PS.AggregateFunc, row *Row, params []any) (any, error) {
	if row != nil {
		// REQ000378: HAVING and post-aggregate references to
		// aggregates must resolve to the precomputed value in
		// the row emitted by the Aggregate operator. Try all
		// aggregate column name shapes (COUNT(*), COUNT(col),
		// SUM(col), ...).
		if _, ok := e.Arg.(*PS.StarExpr); ok {
			name := e.Name + "(*)"
			if v, found := row.Lookup(name); found {
				return v, nil
			}
		}
		if ident, ok := e.Arg.(*PS.Ident); ok {
			name := e.Name + "(" + ident.Name + ")"
			if v, found := row.Lookup(name); found {
				return v, nil
			}
		}
	}
	switch strings.ToUpper(e.Name) {
	case "COUNT":
		return int64(0), nil
	case "SUM":
		return int64(0), nil
	case "AVG":
		return float64(0), nil
	case "MIN":
		return nil, nil
	case "MAX":
		return nil, nil
	}
	return nil, ErrEval
}

func evalFunction(e *PS.FunctionCall, row *Row, params []any) (any, error) {
	switch e.Name {
	case "LENGTH":
		if len(e.Args) > 0 {
			v, err := Eval(e.Args[0], row, params)
			if err != nil {
				return nil, err
			}
			if s, ok := v.(string); ok {
				return int64(len(s)), nil
			}
		}
	case "UPPER":
		if len(e.Args) > 0 {
			v, err := Eval(e.Args[0], row, params)
			if err != nil {
				return nil, err
			}
			if s, ok := v.(string); ok {
				return strings.ToUpper(s), nil
			}
		}
	case "LOWER":
		if len(e.Args) > 0 {
			v, err := Eval(e.Args[0], row, params)
			if err != nil {
				return nil, err
			}
			if s, ok := v.(string); ok {
				return strings.ToLower(s), nil
			}
		}
	case "IFNULL":
		if len(e.Args) == 2 {
			v1, err := Eval(e.Args[0], row, params)
			if err != nil {
				return nil, err
			}
			if v1 == nil {
				return Eval(e.Args[1], row, params)
			}
			return v1, nil
		}
	case "COALESCE":
		for _, arg := range e.Args {
			v, err := Eval(arg, row, params)
			if err != nil {
				return nil, err
			}
			if v != nil {
				return v, nil
			}
		}
		return nil, nil
	case "NULLIF":
		if len(e.Args) != 2 {
			return nil, fmt.Errorf("nullif: expected 2 args")
		}
		a, err := Eval(e.Args[0], row, params)
		if err != nil {
			return nil, err
		}
		b, err := Eval(e.Args[1], row, params)
		if err != nil {
			return nil, err
		}
		if equalValue(a, b) == true {
			return nil, nil
		}
		return a, nil
	case "NOW":
		return time.Now().UTC().Format(time.RFC3339), nil
	case "SUBSTR":
		return evalSubstr(e.Args, row, params)
	case "ABS":
		return evalAbs(e.Args, row, params)
	case "HEX":
		return evalHex(e.Args, row, params)
	case "ROUND":
		return evalRound(e.Args, row, params)
	case "CHAR":
		return evalChar(e.Args, row, params)
	case "CONCAT":
		return evalConcat(e.Args, row, params)
	case "CONCAT_WS":
		return evalConcatWS(e.Args, row, params)
	case "FORMAT":
		return evalFormat(e.Args, row, params)
	case "LTRIM":
		return evalLtrim(e.Args, row, params)
	case "RTRIM":
		return evalRtrim(e.Args, row, params)
	case "TRIM":
		return evalTrim(e.Args, row, params)
	case "REPLACE":
		return evalReplace(e.Args, row, params)
	case "QUOTE":
		return evalQuote(e.Args, row, params)
	case "TYPEOF":
		return evalTypeof(e.Args, row, params)
	case "OCTET_LENGTH":
		return evalOctetLength(e.Args, row, params)
	case "UNICODE":
		return evalUnicode(e.Args, row, params)
	case "SQLITE_VERSION":
		return evalSqliteVersion(e.Args, row, params)
	case "SQLITE_SOURCE_ID":
		return evalSqliteSourceID(e.Args, row, params)
	case "IIF", "IF":
		return evalIIF(e.Args, row, params)
	case "INSTR":
		return evalInstr(e.Args, row, params)
	case "SIGN":
		return evalSign(e.Args, row, params)
	case "MAX":
		return evalMaxScalar(e.Args, row, params)
	case "MIN":
		return evalMinScalar(e.Args, row, params)
	case "RANDOM":
		return evalRandom(e.Args, row, params)
	case "RANDOMBLOB":
		return evalRandomBlob(e.Args, row, params)
	case "ZEROBLOB":
		return evalZeroblob(e.Args, row, params)
	case "GLOB":
		return evalGlob(e.Args, row, params)
	case "LIKELIHOOD":
		return evalLikelihood(e.Args, row, params)
	case "LIKELY":
		return evalLikely(e.Args, row, params)
	case "SOUNDEX":
		return evalSoundex(e.Args, row, params)
	case "UNHEX":
		return evalUnhex(e.Args, row, params)
	case "UNISTR":
		return evalUnistr(e.Args, row, params)
	case "UNLIKELY":
		return evalUnlikely(e.Args, row, params)
	case "CHANGES":
		// REQ000385: changes() returns the number of rows modified
		// by the most recent INSERT, UPDATE, or DELETE. Takes no args.
		acc := getSessionCounterAccessor()
		if acc == nil {
			return int64(0), nil
		}
		return acc.ChangesCount(getCurrentSessionID()), nil
	case "LAST_INSERT_ROWID":
		// REQ000394: last_insert_rowid() returns the rowid of the
		// last successful INSERT. Takes no args.
		acc := getSessionCounterAccessor()
		if acc == nil {
			return int64(0), nil
		}
		return acc.LastInsertRowID(getCurrentSessionID()), nil
	case "TOTAL_CHANGES":
		// REQ000411: total_changes() returns cumulative rows modified
		// since connection open. Takes no args.
		acc := getSessionCounterAccessor()
		if acc == nil {
			return int64(0), nil
		}
		return acc.TotalChangesCount(getCurrentSessionID()), nil
	default:
		if isDateTimeFunc(e.Name) {
			args := make([]any, len(e.Args))
			for i, arg := range e.Args {
				v, err := Eval(arg, row, params)
				if err != nil {
					return nil, err
				}
				args[i] = v
			}
			return evalDateTimeFunc(e.Name, args)
		}
		if isJSONFunc(e.Name) {
			args := make([]any, len(e.Args))
			for i, arg := range e.Args {
				v, err := Eval(arg, row, params)
				if err != nil {
					return nil, err
				}
				args[i] = v
			}
			return evalJSONFunc(e.Name, args)
		}
	}
	return nil, ErrEval
}

// ErrTriggerAbort is returned by RAISE(ABORT, ...) evaluation to
// signal that the trigger action should abort with an error message.
// REQ000560.
var ErrTriggerAbort = errors.New("ex: trigger abort")

func evalRaise(e *PS.RaiseFunc, row *Row, params []any) (any, error) {
	action := strings.ToUpper(e.Action)
	if action == "IGNORE" {
		// RAISE(IGNORE) suppresses the trigger action.
		// Return a sentinel value; the trigger executor
		// checks for this and skips the rest of the action.
		return nil, ErrIgnoreRow
	}
	// RAISE(ABORT, 'message') or RAISE(ROLLBACK, 'message') etc.
	var msg string
	if e.Message != nil {
		v, err := Eval(e.Message, row, params)
		if err != nil {
			return nil, err
		}
		if s, ok := v.(string); ok {
			msg = s
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrTriggerAbort, msg)
}

// REQ000382: ABS, HEX, ROUND scalar functions.
// evalAbs returns the absolute value of a numeric argument. NULL
// in → NULL out; string/non-numeric in → 0.0 (SQLite standard).
// For int64, MIN_INT64 cannot be negated without overflow; we
// surface that as ErrEval per the SQLite error message.
// evalHex returns the uppercase hex encoding of its argument.
// Integers are first converted to text (decimal) then hex-encoded;
// strings/BLOBs are encoded byte-for-byte. NULL → NULL.
// evalRound rounds the first argument to the second (default 0)
// decimal places. Negative second argument is treated as 0 per
// SQLite; Y < 0 also surfaces a warning in SQLite but we treat it
// as 0 for v1.

func evalAbs(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case int64:
		if x == math.MinInt64 {
			return nil, fmt.Errorf("abs: integer overflow")
		}
		if x < 0 {
			return -x, nil
		}
		return x, nil
	case float64:
		if x < 0 {
			return -x, nil
		}
		return x, nil
	}
	return 0.0, nil
}

func evalHex(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	var s string
	switch x := v.(type) {
	case int64:
		// SQLite converts the integer to its text form first,
		// then hex-encodes that text. HEX(255) → "323535"
		// (the hex of the three ASCII digits).
		s = hex.EncodeToString([]byte(strconv.FormatInt(x, 10)))
	case float64:
		s = hex.EncodeToString([]byte(strconv.FormatFloat(x, 'g', -1, 64)))
	case []byte:
		s = hex.EncodeToString(x)
	case string:
		s = hex.EncodeToString([]byte(x))
	default:
		s = hex.EncodeToString([]byte(fmt.Sprint(x)))
	}
	return strings.ToUpper(s), nil
}

func evalRound(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	// REQ000772: int64 fast path when no places arg is given or
	// places==0. Avoid the float64 conversion in numericFloat for
	// the common case of ROUND(int_col).
	if len(args) == 1 {
		if x, ok := v.(int64); ok {
			return x, nil
		}
	}
	x, ok := numericFloat(v)
	if !ok {
		return 0.0, nil
	}
	places := int64(0)
	if len(args) == 2 {
		pv, err := Eval(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if pv != nil {
			if p, ok := toInt64(pv); ok {
				if p < 0 {
					places = 0
				} else {
					places = p
				}
			}
		}
	}
	mult := math.Pow(10, float64(places))
	return math.Round(x*mult) / mult, nil
}

// evalSubstr implements SUBSTR(str, start[, length]).
//   - start is 1-based per SQL convention; values <= 0 are clamped to 1.
//   - length is optional; when omitted the substring runs to the end of str.
//   - non-string inputs are coerced via fmt.Sprint.
func evalSubstr(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 2 {
		return nil, ErrEval
	}
	rawStr, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000606: SUBSTR(NULL, ...) and SUBSTR(s, NULL, ...) must
	// return NULL, not "<nil>" (the fmt.Sprint result).
	if rawStr == nil {
		return nil, nil
	}
	s := fmt.Sprint(rawStr)
	startV, err := Eval(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if startV == nil {
		return nil, nil
	}
	start, ok := toInt64(startV)
	if !ok {
		return nil, ErrEval
	}
	if start < 0 {
		start = int64(len(s)) + start + 1
		if start < 1 {
			start = 1
		}
	} else if start == 0 {
		start = 1
	}
	// Convert 1-based start to 0-based offset.
	offset := int(start) - 1
	if offset >= len(s) {
		return "", nil
	}
	if len(args) >= 3 {
		lenV, err := Eval(args[2], row, params)
		if err != nil {
			return nil, err
		}
		length, ok := toInt64(lenV)
		if !ok {
			return nil, ErrEval
		}
		if length < 0 {
			return "", nil
		}
		end := offset + int(length)
		if end > len(s) {
			end = len(s)
		}
		return s[offset:end], nil
	}
	return s[offset:], nil
}

// evalChar converts integer Unicode code points to a UTF-8 string.
// REQ000386.
func evalChar(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) == 0 {
		return "", nil
	}
	var sb strings.Builder
	for _, arg := range args {
		v, err := Eval(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			return nil, nil // Any NULL arg → NULL result
		}
		n, ok := toInt64(v)
		if !ok {
			continue
		}
		if n < 0 || n > unicode.MaxRune {
			continue
		}
		sb.WriteRune(rune(n))
	}
	return sb.String(), nil
}

// evalConcat concatenates all arguments into a single string.
// If any argument is NULL, the result is NULL. REQ000387.
func evalConcat(args []PS.Expr, row *Row, params []any) (any, error) {
	var sb strings.Builder
	for _, arg := range args {
		v, err := Eval(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			return nil, nil // Any NULL → NULL result
		}
		sb.WriteString(fmt.Sprint(v))
	}
	return sb.String(), nil
}

// evalConcatWS concatenates with separator. First arg is separator.
// SEP=NULL → NULL. Skips NULL values. REQ000388.
func evalConcatWS(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 2 {
		return nil, ErrEval
	}
	sep, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if sep == nil {
		return nil, nil // NULL separator → NULL result
	}
	sepStr := fmt.Sprint(sep)
	var sb strings.Builder
	first := true
	for i := 1; i < len(args); i++ {
		v, err := Eval(args[i], row, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue // Skip NULL values
		}
		if !first {
			sb.WriteString(sepStr)
		}
		sb.WriteString(fmt.Sprint(v))
		first = false
	}
	return sb.String(), nil
}

// evalFormat implements printf-style formatting. REQ000389.
func evalFormat(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	fmtV, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if fmtV == nil {
		return nil, nil
	}
	fmtStr, ok := fmtV.(string)
	if !ok {
		fmtStr = fmt.Sprint(fmtV)
	}
	// Convert remaining args to any for fmt.Sprintf
	fmtArgs := make([]any, len(args)-1)
	for i := 1; i < len(args); i++ {
		v, err := Eval(args[i], row, params)
		if err != nil {
			return nil, err
		}
		fmtArgs[i-1] = v
	}
	return fmt.Sprintf(fmtStr, fmtArgs...), nil
}

// evalLtrim trims leading characters. Default trim chars are spaces.
// REQ000397.
func evalLtrim(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	s := fmt.Sprint(v)
	if len(args) >= 2 {
		trimV, err := Eval(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV != nil {
			return strings.TrimLeft(s, fmt.Sprint(trimV)), nil
		}
	}
	return strings.TrimLeft(s, " "), nil
}

// evalRtrim trims trailing characters. Default trim chars are spaces.
// REQ000406.
func evalRtrim(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	s := fmt.Sprint(v)
	if len(args) >= 2 {
		trimV, err := Eval(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV != nil {
			return strings.TrimRight(s, fmt.Sprint(trimV)), nil
		}
	}
	return strings.TrimRight(s, " "), nil
}

// evalTrim trims leading and trailing characters. Default trim chars are spaces.
// REQ000438.
func evalTrim(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	s := fmt.Sprint(v)
	if len(args) >= 2 {
		trimV, err := Eval(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV != nil {
			return strings.Trim(s, fmt.Sprint(trimV)), nil
		}
	}
	return strings.Trim(s, " "), nil
}

// evalReplace replaces all occurrences of Y in X with Z.
// REQ000404.
func evalReplace(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 3 {
		return nil, ErrEval
	}
	x, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if x == nil {
		return nil, nil
	}
	y, err := Eval(args[1], row, params)
	if err != nil {
		return nil, err
	}
	z, err := Eval(args[2], row, params)
	if err != nil {
		return nil, err
	}
	xs := fmt.Sprint(x)
	if y == nil {
		return xs, nil // NULL pattern → return X unchanged
	}
	ys := fmt.Sprint(y)
	zs := ""
	if z != nil {
		zs = fmt.Sprint(z)
	}
	return strings.ReplaceAll(xs, ys, zs), nil
}

// evalQuote renders X as an SQL literal. Strings are single-quoted
// with escaped quotes. BLOBs as X'hex'. NULL as unquoted NULL.
// REQ000401.
func evalQuote(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return "NULL", nil
	}
	switch x := v.(type) {
	case string:
		// Escape single quotes by doubling
		escaped := strings.ReplaceAll(x, "'", "''")
		return "'" + escaped + "'", nil
	case int64, float64, int, bool:
		// Numbers and booleans are not quoted
		return fmt.Sprint(x), nil
	case []byte:
		// BLOB as X'hex'
		return "X'" + hex.EncodeToString(x) + "'", nil
	default:
		return "'" + strings.ReplaceAll(fmt.Sprint(x), "'", "''") + "'", nil
	}
}

// evalTypeof returns the type name of X: "null", "integer", "real",
// "text", or "blob". REQ000412.
func evalTypeof(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return "null", nil
	}
	switch v.(type) {
	case int64, int, float64:
		if _, ok := v.(float64); ok {
			return "real", nil
		}
		return "integer", nil
	case string:
		return "text", nil
	case []byte:
		return "blob", nil
	case bool:
		return "integer", nil // Booleans are stored as integers
	default:
		return "text", nil
	}
}

// evalOctetLength returns the byte length of X (not code points).
// REQ000400.
func evalOctetLength(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	// REQ000621: for []byte return the raw byte length, not the
	// fmt.Sprint representation (which yields "[104 101 ...]" for
	// "Hello"). For strings, return the byte length directly too
	// rather than going through fmt.Sprint.
	if b, ok := v.([]byte); ok {
		return int64(len(b)), nil
	}
	if s, ok := v.(string); ok {
		return int64(len(s)), nil
	}
	return int64(len(fmt.Sprint(v))), nil
}

// evalUnicode returns the Unicode code point of the first character.
// REQ000414.
func evalUnicode(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	s := fmt.Sprint(v)
	if len(s) == 0 {
		return int64(0), nil
	}
	r, _ := utf8.DecodeRuneInString(s)
	return int64(r), nil
}

// evalSqliteVersion returns the version string "0.26.7".
// REQ000410.
func evalSqliteVersion(args []PS.Expr, row *Row, params []any) (any, error) {
	return "0.26.7", nil
}

// evalSqliteSourceID returns "razordata-v0.26.7".
// REQ000409.
func evalSqliteSourceID(args []PS.Expr, row *Row, params []any) (any, error) {
	return "razordata-v0.26.7", nil
}

// evalIIF implements the iif(B, X, Y) conditional function.
// Short-circuits: only evaluates chosen branch. REQ000392.
func evalIIF(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 3 {
		return nil, ErrEval
	}
	cond, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if truthy(cond) {
		return Eval(args[1], row, params)
	}
	return Eval(args[2], row, params)
}

// evalInstr returns the 1-based position of Y in X, or 0 if not found.
// REQ000393.
func evalInstr(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 2 {
		return nil, ErrEval
	}
	x, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000620: NULL on either side must return NULL, not 0.
	if x == nil {
		return nil, nil
	}
	y, err := Eval(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if y == nil {
		return nil, nil
	}
	xs := fmt.Sprint(x)
	ys := fmt.Sprint(y)
	if ys == "" {
		return int64(1), nil
	}
	pos := strings.Index(xs, ys)
	if pos < 0 {
		return int64(0), nil
	}
	return int64(pos + 1), nil // 1-based
}

// evalSign returns -1, 0, or +1 based on the sign of X.
// REQ000407.
func evalSign(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000619: SIGN(NULL) must return NULL, not 0.
	if v == nil {
		return nil, nil
	}
	// REQ000772: int64 fast path — SIGN on int64 only needs a
	// single comparison instead of the float64 conversion in
	// numericFloat.
	if x, ok := v.(int64); ok {
		if x < 0 {
			return int64(-1), nil
		} else if x > 0 {
			return int64(1), nil
		}
		return int64(0), nil
	}
	n, ok := numericFloat(v)
	if !ok {
		return int64(0), nil
	}
	if n < 0 {
		return int64(-1), nil
	} else if n > 0 {
		return int64(1), nil
	}
	return int64(0), nil
}

// evalMaxScalar returns the maximum of multiple scalar arguments.
// NULLs are skipped. REQ000398.
func evalMaxScalar(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	var maxV any
	for _, arg := range args {
		v, err := Eval(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if maxV == nil {
			maxV = v
			continue
		}
		// Compare with current max
		if compare(v, maxV) > 0 {
			maxV = v
		}
	}
	return maxV, nil
}

// evalMinScalar returns the minimum of multiple scalar arguments.
// REQ000399.
func evalMinScalar(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	var minV any
	for _, arg := range args {
		v, err := Eval(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v == nil {
			continue
		}
		if minV == nil {
			minV = v
			continue
		}
		if compare(v, minV) < 0 {
			minV = v
		}
	}
	return minV, nil
}

// evalRandom returns a pseudo-random int64. REQ000402.
func evalRandom(args []PS.Expr, row *Row, params []any) (any, error) {
	sign := 1
	if rand.IntN(2) == 1 {
		sign = -1
	}
	return int64(sign * int(rand.Int64())), nil
}

// evalRandomBlob returns N bytes of random data. REQ000403.
func evalRandomBlob(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	nV, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	n, ok := toInt64(nV)
	if !ok || n < 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(rand.IntN(256))
	}
	return buf, nil
}

// evalZeroblob returns N bytes of 0x00. REQ000417.
func evalZeroblob(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	nV, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	n, ok := toInt64(nV)
	if !ok || n < 0 {
		return nil, nil
	}
	return make([]byte, n), nil
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	}
	return 0, false
}

func compare(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	// REQ000753: int64-int64 fast path — avoid float64 conversion.
	if ai, aok := a.(int64); aok {
		if bi, bok := b.(int64); bok {
			if ai < bi {
				return -1
			}
			if ai > bi {
				return 1
			}
			return 0
		}
	}
	// float64-float64 fast path.
	if af, aok := a.(float64); aok {
		if bf, bok := b.(float64); bok {
			return cmpFloat(af, bf)
		}
	}
	// Fallback: convert to float64.
	if af, aok := numericFloat(a); aok {
		if bf, bok := numericFloat(b); bok {
			return cmpFloat(af, bf)
		}
	}
	switch v := a.(type) {
	case string:
		if vb, ok := b.(string); ok {
			return cmpString(v, vb)
		}
	case bool:
		if vb, ok := b.(bool); ok {
			return cmpBool(v, vb)
		}
	}
	return 0
}

func cmpFloat(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpBool(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return 1
	}
	return -1
}

func add(a, b any) (any, error) {
	return numericArith(a, b, '+')
}

func sub(a, b any) (any, error) {
	return numericArith(a, b, '-')
}

func mul(a, b any) (any, error) {
	return numericArith(a, b, '*')
}

func div(a, b any) (any, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	if ai, ok := a.(int64); ok {
		if bi, ok := b.(int64); ok {
			if bi == 0 {
				return nil, nil
			}
			return ai / bi, nil
		}
	}
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if !aok || !bok {
		return nil, nil
	}
	if bf == 0 {
		return nil, nil
	}
	return af / bf, nil
}

func mod(a, b any) (any, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	if ai, ok := a.(int64); ok {
		if bi, ok := b.(int64); ok {
			if bi == 0 {
				return nil, nil
			}
			return ai % bi, nil
		}
	}
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if !aok || !bok {
		return nil, nil
	}
	if bf == 0 {
		return nil, nil
	}
	return math.Mod(af, bf), nil
}

func bitand(a, b any) (any, error) {
	ai, aok := toInt64(a)
	bi, bok := toInt64(b)
	if !aok || !bok {
		return nil, nil
	}
	return ai & bi, nil
}

func bitor(a, b any) (any, error) {
	ai, aok := toInt64(a)
	bi, bok := toInt64(b)
	if !aok || !bok {
		return nil, nil
	}
	return ai | bi, nil
}

func bitxor(a, b any) (any, error) {
	ai, aok := toInt64(a)
	bi, bok := toInt64(b)
	if !aok || !bok {
		return nil, nil
	}
	return ai ^ bi, nil
}

func lshift(a, b any) (any, error) {
	ai, aok := toInt64(a)
	bi, bok := toInt64(b)
	if !aok || !bok {
		return nil, nil
	}
	if bi < 0 || bi > 63 {
		return nil, nil
	}
	return ai << bi, nil
}

func rshift(a, b any) (any, error) {
	ai, aok := toInt64(a)
	bi, bok := toInt64(b)
	if !aok || !bok {
		return nil, nil
	}
	if bi < 0 || bi > 63 {
		return nil, nil
	}
	return ai >> bi, nil
}

func concat(a, b any) (any, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return as + bs, nil
		}
	}
	return fmt.Sprintf("%v%v", a, b), nil
}

func numericArith(a, b any, op rune) (any, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	// REQ000752: int64-int64 fast path — avoid float64 conversion.
	// This is the most common case (integer columns).
	if ai, aok := a.(int64); aok {
		if bi, bok := b.(int64); bok {
			switch op {
			case '+':
				if (bi > 0 && ai > math.MaxInt64-bi) || (bi < 0 && ai < math.MinInt64-bi) {
					return nil, nil
				}
				return ai + bi, nil
			case '-':
				if (bi < 0 && ai > math.MaxInt64+bi) || (bi > 0 && ai < math.MinInt64+bi) {
					return nil, nil
				}
				return ai - bi, nil
			case '*':
				if ai == 0 || bi == 0 {
					return int64(0), nil
				}
				if ai == -1 && bi == math.MinInt64 {
					return nil, nil
				}
				if bi == -1 && ai == math.MinInt64 {
					return nil, nil
				}
				if ai > 0 && bi > 0 && ai > math.MaxInt64/bi {
					return nil, nil
				}
				if ai < 0 && bi < 0 && ai < math.MaxInt64/bi {
					return nil, nil
				}
				if (ai > 0 && bi < 0 && bi < math.MinInt64/ai) ||
					(ai < 0 && bi > 0 && ai < math.MinInt64/bi) {
					return nil, nil
				}
				return ai * bi, nil
			}
		}
	}
	// Fallback: float64 path for mixed int/float or float/float.
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if !aok || !bok {
		return nil, nil
	}
	var r float64
	switch op {
	case '+':
		r = af + bf
	case '-':
		r = af - bf
	case '*':
		r = af * bf
	}
	return r, nil
}

func numericFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

// band implements three-valued SQL AND semantics (REQ000605):
//   - false AND *       = false (NULL operand can never make AND true)
//   - true  AND NULL    = NULL
//   - true  AND true    = true
//   - NULL  AND NULL    = NULL
func band(a, b any) (any, error) {
	if a == false || b == false {
		return false, nil
	}
	if a == nil || b == nil {
		return nil, nil
	}
	return true, nil
}

// bor implements three-valued SQL OR semantics (REQ000605):
//   - true OR *       = true (NULL operand can never make OR false)
//   - false OR NULL   = NULL
//   - false OR false  = false
//   - NULL OR NULL    = NULL
func bor(a, b any) (any, error) {
	if a == true || b == true {
		return true, nil
	}
	if a == nil || b == nil {
		return nil, nil
	}
	return false, nil
}

func like(a, b any, escape string) (bool, error) {
	s, ok := a.(string)
	if !ok {
		return false, nil
	}
	pattern, ok := b.(string)
	if !ok {
		return false, nil
	}
	return matchLike(pattern, s, escape), nil
}

func matchLike(pattern, s, escape string) bool {
	pi, si := 0, 0
	starPI, starSI := -1, -1
	escByte := byte(0)
	if len(escape) == 1 {
		escByte = escape[0]
	}
	for si < len(s) {
		if pi < len(pattern) {
			c := pattern[pi]
			// If escape char is set and the current pattern char is the escape,
			// treat the next pattern char as a literal.
			if escByte != 0 && c == escByte && pi+1 < len(pattern) {
				pi++
				c = pattern[pi]
				if c == s[si] {
					pi++
					si++
					continue
				}
				// If the escaped char doesn't match, fall through to star logic
			} else {
				switch c {
				case '%':
					starPI = pi
					starSI = si
					pi++
					continue
				case '_':
					pi++
					si++
					continue
				}
			}
			if c == s[si] {
				pi++
				si++
				continue
			}
		}
		if starPI >= 0 {
			pi = starPI + 1
			starSI++
			si = starSI
			continue
		}
		return false
	}
	// Consume trailing % and escaped trailing escape char
	for pi < len(pattern) {
		c := pattern[pi]
		if escByte != 0 && c == escByte && pi+1 < len(pattern) {
			pi += 2 // skip escaped char at end (it's a literal that must match)
			continue
		}
		if c == '%' {
			pi++
		} else {
			break
		}
	}
	return pi == len(pattern)
}

func is(a, b any) (bool, error) {
	if a == nil && b == nil {
		return true, nil
	}
	if a == nil || b == nil {
		return false, nil
	}
	return a == b, nil
}

func equalValue(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	// REQ000754: int64-int64 fast path — avoid normalizeInt re-boxing.
	if ai, aok := a.(int64); aok {
		if bi, bok := b.(int64); bok {
			return ai == bi
		}
		if bf, bok := b.(float64); bok {
			return float64(ai) == bf
		}
		if bi, bok := b.(int); bok {
			return ai == int64(bi)
		}
		return false
	}
	// float64 fast path.
	if af, aok := a.(float64); aok {
		if bf, bok := b.(float64); bok {
			return af == bf
		}
		if bi, bok := b.(int64); bok {
			return af == float64(bi)
		}
		return false
	}
	// int type (Go's non-64-bit int).
	if ai, aok := a.(int); aok {
		if bi, bok := b.(int); bok {
			return ai == bi
		}
		if bi, bok := b.(int64); bok {
			return int64(ai) == bi
		}
		return false
	}
	// string and other types.
	return a == b
}

// normalizeInt converts Go int to int64 for consistent comparison.
func normalizeInt(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	}
	return v
}

// evalGlob implements glob(X,Y) — pattern matching with *, ?, [...].
// REQ000390.
func evalGlob(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("glob requires 2 args")
	}
	pattern, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if pattern == nil {
		return nil, nil
	}
	str, err := Eval(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if str == nil {
		return nil, nil
	}
	p, ok := pattern.(string)
	if !ok {
		return nil, nil
	}
	s, ok := str.(string)
	if !ok {
		return nil, nil
	}
	if globMatch(p, s) {
		return int64(1), nil
	}
	return int64(0), nil
}

// globMatch implements SQL GLOB pattern matching as a direct
// byte-matcher. REQ000581: the previous implementation called
// regexp.MustCompile on every invocation, allocating a new
// regex per row. For "WHERE name GLOB '*.txt'" over 10K rows
// that was 10K identical compiles. SQL GLOB supports only three
// wildcard forms (*, ?, [...]) which are easy to match without
// a regex engine.
func globMatch(pattern, s string) bool {
	return globMatchFrom(pattern, 0, s, 0)
}

// globMatchFrom is the recursive worker. Returns true if the
// remainder of pattern starting at pi matches the remainder of
// s starting at si.
func globMatchFrom(pattern string, pi int, s string, si int) bool {
	for pi < len(pattern) {
		c := pattern[pi]
		switch c {
		case '*':
			// * matches any number of characters. Try matching
			// the rest of the pattern against every suffix of s.
			for skip := si; skip <= len(s); skip++ {
				if globMatchFrom(pattern, pi+1, s, skip) {
					return true
				}
			}
			return false
		case '?':
			if si >= len(s) {
				return false
			}
			pi++
			si++
		case '[':
			// Character class: [abc], [a-z], [^abc]. We support
			// single chars and ranges; negation with leading ^.
			if si >= len(s) {
				return false
			}
			pi++
			negate := false
			if pi < len(pattern) && pattern[pi] == '^' {
				negate = true
				pi++
			}
			matched := false
			for pi < len(pattern) && pattern[pi] != ']' {
				lo := pattern[pi]
				pi++
				if pi+1 < len(pattern) && pattern[pi] == '-' && pattern[pi+1] != ']' {
					hi := pattern[pi+1]
					pi += 2
					if lo <= s[si] && s[si] <= hi {
						matched = true
					}
				} else {
					if lo == s[si] {
						matched = true
					}
				}
			}
			if pi < len(pattern) {
				pi++ // skip ']'
			}
			if matched == negate {
				return false
			}
			si++
		case '\\':
			if pi+1 >= len(pattern) {
				return false
			}
			pi++
			if si >= len(s) || pattern[pi] != s[si] {
				return false
			}
			pi++
			si++
		default:
			if si >= len(s) || c != s[si] {
				return false
			}
			pi++
			si++
		}
	}
	return si == len(s)
}

// evalLikelihood implements likelihood(X,Y) — no-op pass-through.
// REQ000395.
func evalLikelihood(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	return Eval(args[0], row, params)
}

// evalLikely implements likely(X) — no-op pass-through.
// REQ000396.
func evalLikely(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	return Eval(args[0], row, params)
}

// evalSoundex implements soundex(X) — 4-char phonetic encoding.
// REQ000408.
// evalSoundex implements soundex(X) — 4-char phonetic encoding.
// REQ000408.
// REQ000591: pre-allocated soundex encoding map. Kept at package
// level to avoid re-allocation on every evalSoundex call.
var soundexCodes = map[byte]byte{
	'B': '1', 'F': '1', 'P': '1', 'V': '1',
	'C': '2', 'G': '2', 'J': '2', 'K': '2', 'Q': '2', 'S': '2', 'X': '2', 'Z': '2',
	'D': '3', 'T': '3',
	'L': '4',
	'M': '5', 'N': '5',
	'R': '6',
}

func evalSoundex(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	val, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return "?000", nil
	}
	if s == "" {
		return "?000", nil
	}

	// Convert to uppercase, keep only letters
	var letters []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			letters = append(letters, c-32) // to upper
		} else if c >= 'A' && c <= 'Z' {
			letters = append(letters, c)
		}
	}

	if len(letters) == 0 {
		return "?000", nil
	}

	codes := soundexCodes

	result := make([]byte, 0, 4)
	result = append(result, letters[0])

	prevCode := codes[letters[0]]

	for i := 1; i < len(letters) && len(result) < 4; i++ {
		code := codes[letters[i]]

		if code != 0 && code != prevCode {
			result = append(result, code)
		}

		// Vowels (and H, W) don't separate same-code consonants
		isVowel := letters[i] == 'A' || letters[i] == 'E' || letters[i] == 'I' ||
			letters[i] == 'O' || letters[i] == 'U' || letters[i] == 'Y' ||
			letters[i] == 'H' || letters[i] == 'W'

		if !isVowel {
			prevCode = code
		}
	}

	for len(result) < 4 {
		result = append(result, '0')
	}

	return string(result), nil
}

// REQ000413.
func evalUnhex(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	val, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, nil
	}

	// Decode hex string
	s = strings.TrimSpace(s)
	if len(s)%2 != 0 {
		return nil, nil
	}

	result := make([]byte, 0, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		h1 := hexDigit(s[i])
		h2 := hexDigit(s[i+1])
		if h1 == 0xff || h2 == 0xff {
			return nil, nil
		}
		result = append(result, h1<<4|h2)
	}

	return result, nil
}

func hexDigit(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0xff
	}
}

// evalUnistr implements unistr(X) — backslash-escape decoder.
// REQ000415.
func evalUnistr(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	val, err := Eval(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, nil
	}

	var result strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			result.WriteByte(s[i])
			continue
		}

		i++
		switch s[i] {
		case 'u':
			if i+4 >= len(s) {
				result.WriteByte('\\')
				result.WriteByte('u')
				break
			}
			hexStr := s[i+1 : i+5]
			if r, ok := parseHex4(hexStr); ok {
				result.WriteRune(r)
				i += 4
			} else {
				result.WriteByte('\\')
				result.WriteByte('u')
				result.WriteString(hexStr)
				i += 4
			}
		case 'U':
			if i+8 >= len(s) {
				result.WriteByte('\\')
				result.WriteByte('U')
				break
			}
			hexStr := s[i+1 : i+9]
			if r, ok := parseHex8(hexStr); ok {
				result.WriteRune(r)
				i += 8
			} else {
				result.WriteByte('\\')
				result.WriteByte('U')
				result.WriteString(hexStr)
				i += 8
			}
		case '+':
			if i+6 >= len(s) {
				result.WriteByte('\\')
				result.WriteByte('+')
				break
			}
			hexStr := s[i+1 : i+7]
			if r, ok := parseHex6(hexStr); ok {
				result.WriteRune(r)
				i += 6
			} else {
				result.WriteByte('\\')
				result.WriteByte('+')
				result.WriteString(hexStr)
				i += 6
			}
		case 'n':
			result.WriteByte('\n')
		case 'r':
			result.WriteByte('\r')
		case 't':
			result.WriteByte('\t')
		case '\\':
			result.WriteByte('\\')
		default:
			result.WriteByte('\\')
			result.WriteByte(s[i])
		}
	}

	return result.String(), nil
}

func parseHex4(s string) (rune, bool) {
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, false
	}
	return rune(v), true
}

func parseHex6(s string) (rune, bool) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return rune(v), true
}

func parseHex8(s string) (rune, bool) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, false
	}
	return rune(v), true
}

// evalUnlikely implements unlikely(X) — no-op pass-through.
// REQ000416.
func evalUnlikely(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	return Eval(args[0], row, params)
}

// glob implements the GLOB binary operator (SQLite-compatible).
// Returns true if string matches the glob pattern.
func glob(pattern, s any) (any, error) {
	if pattern == nil || s == nil {
		return nil, nil
	}
	p, ok := pattern.(string)
	if !ok {
		return nil, fmt.Errorf("ex: GLOB pattern must be string, got %T", pattern)
	}
	str, ok := s.(string)
	if !ok {
		return nil, fmt.Errorf("ex: GLOB operand must be string, got %T", s)
	}
	return globMatch(p, str), nil
}

// intdiv implements the DIV integer division operator.
// Truncates toward zero (like SQLite's / operator on integers).
func intdiv(a, b any) (any, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	ai, aok := a.(int64)
	bi, bok := b.(int64)
	if aok && bok {
		if bi == 0 {
			return nil, nil
		}
		return ai / bi, nil
	}
	// Fallback: convert to float64 and truncate
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if aok && bok {
		if bf == 0 {
			return nil, nil
		}
		return int64(af / bf), nil
	}
	return nil, fmt.Errorf("ex: DIV requires numeric operands")
}
