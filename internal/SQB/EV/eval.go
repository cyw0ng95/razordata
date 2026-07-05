package EV

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// globalSubqueryCache caches results of non-correlated scalar subqueries
// across all executions. Keyed by serialized statement.
// This eliminates O(N) subquery re-evaluations for N-row result sets.
var globalSubqueryCache sync.Map

// subqueryKeyCache memoizes the serialized cache key for each
// *PS.SubqueryExpr pointer. PL.SerializeKey hashes the full AST on
// every call; for Sort key extraction that re-evaluates the same
// subquery for every row, this saves repeated serializations.
// REQ001202-idx.
var subqueryKeyCache sync.Map

// cachedSubqueryKey returns the cached serialized cache key for the
// given SubqueryExpr, computing it on first use. Safe for concurrent
// use; multiple callers may compute the same key concurrently but
// only one value is stored. REQ001202-idx.
func cachedSubqueryKey(e *PS.SubqueryExpr) string {
	if v, ok := subqueryKeyCache.Load(e); ok {
		return v.(string)
	}
	sel := e.Subquery.(*PS.Select)
	key := PL.SerializeKey(sel)
	subqueryKeyCache.Store(e, key)
	return key
}

// subqueryColRefCache memoizes the correlated column references for each
// *PS.SubqueryExpr. extractCorrelatedColumns is an O(n) AST traversal;
// caching per pointer avoids repeated analysis.
var subqueryColRefCache sync.Map

// cachedCorrelatedCols returns the memoized list of correlated column
// names for the given SubqueryExpr.
func cachedCorrelatedCols(e *PS.SubqueryExpr) []string {
	if v, ok := subqueryColRefCache.Load(e); ok {
		return v.([]string)
	}
	cols := extractCorrelatedColumns(e.Subquery.(*PS.Select))
	subqueryColRefCache.Store(e, cols)
	return cols
}

// extractCorrelatedColumns walks the subquery's WHERE AST and returns
// the list of outer-column references. An Ident is treated as
// correlated if it is NOT resolved by the subquery's FROM table.
// QualifiedName references are correlated when their Table does not
// match the subquery's FROM table.
func extractCorrelatedColumns(sel *PS.Select) []string {
	if sel.Where == nil {
		return nil
	}
	subqAlias := sel.FromAlias
	subqFrom := sel.From
	seen := map[string]bool{}
	correlated := []string{}
	var walk func(PS.Expr)
	walk = func(expr PS.Expr) {
		if expr == nil {
			return
		}
		switch e := expr.(type) {
		case *PS.BinaryExpr:
			walk(e.Left)
			walk(e.Right)
		case *PS.UnaryExpr:
			walk(e.Operand)
		case *PS.Ident:
			// A bare Ident in the subquery's WHERE without table qualifier.
			// Without schema info, we cannot determine if it resolves to
			// the inner table or the outer table. Conservatively treat it
			// as NOT correlated (inner table) — correct, but no caching
			// benefit for these queries.
		case *PS.QualifiedName:
			// QualifiedName: correlated only when the table qualifier
			// refers to the OUTER table, not the subquery's own FROM.
			if subqFrom == "" {
				break
			}
			// If subquery has an alias and the qualifier matches it →
			// inner table reference → NOT correlated.
			if subqAlias != "" && e.Table == subqAlias {
				break
			}
			// No alias and qualifier matches the FROM table →
			// inner table reference (ambiguous, safe default) → NOT correlated.
			if subqAlias == "" && e.Table == subqFrom {
				break
			}
			// Everything else: outer table (e.g. t1.b when inner is "t1 AS x")
			// or a different table entirely → IS correlated.
			if !seen[e.Name] {
				correlated = append(correlated, e.Name)
				seen[e.Name] = true
			}
		case *PS.FunctionCall:
			for _, arg := range e.Args {
				walk(arg)
			}
		case *PS.BetweenExpr:
			walk(e.Expr)
			walk(e.Low)
			walk(e.High)
		case *PS.InExpr:
			walk(e.Expr)
			for _, arg := range e.List {
				walk(arg)
			}
		case *PS.AliasedExpr:
			walk(e.Expr)
		}
	}
	walk(sel.Where)
	return correlated
}

// correlatedSubqueryCache is an LRU cache for correlated scalar subqueries.
// Key = "planKey:outerPKValues" (e.g., "SELECT...:1,5,10").
// Values are cached subquery results (any).
// Max size 256 entries to bound memory.
var correlatedSubqueryCache = newCorrelatedLRU(256)

// correlatedLRU is a thread-safe LRU cache that stores DT.Value
// results directly (no any boxing). Uses sync.RWMutex for read
// performance and a map[string]uint64 for LRU ordering.
type correlatedLRU struct {
	mu        sync.RWMutex
	items     map[string]DT.Value
	order     map[string]uint64
	access    uint64
	evictions int64
	maxSize   int
}

func newCorrelatedLRU(maxSize int) *correlatedLRU {
	return &correlatedLRU{
		items:   make(map[string]DT.Value),
		order:   make(map[string]uint64),
		maxSize: maxSize,
	}
}

func (c *correlatedLRU) Get(key string) (DT.Value, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	val, ok := c.items[key]
	if !ok {
		return DT.NullValue(), false
	}
	c.access++
	c.order[key] = c.access
	return val, true
}

func (c *correlatedLRU) Put(key string, val DT.Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.items[key]
	if !exists && len(c.items) >= c.maxSize {
		if c.evict() {
			c.evictions++
		}
	}
	c.items[key] = val
	// Update access counter for LRU ordering
	c.access++
	c.order[key] = c.access
}

func (c *correlatedLRU) evict() bool {
	minKey := ""
	var minOrder uint64 = ^uint64(0)
	for k, o := range c.order {
		if o < minOrder {
			minOrder = o
			minKey = k
		}
	}
	if minKey != "" {
		delete(c.items, minKey)
		delete(c.order, minKey)
		return true
	}
	return false
}

func (c *correlatedLRU) Clear() {
	// REQ001228: Clear the cache (per-statement invalidation).
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.items)
	clear(c.order)
	c.access = 0
}

func (c *correlatedLRU) Stats() (size int, evictions int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items), c.evictions
}

// ClearSubqueryCaches clears both the global and correlated subquery caches.
// Called by UnregisterAll() for test isolation.
func ClearSubqueryCaches() {
	globalSubqueryCache = sync.Map{}
	correlatedSubqueryCache.Clear()
}

// serializeCorrelatedValues serializes only the specified column values
// from the outer row, used as the cache key for correlated subqueries.
// REQ001228: fine-grained keying based on correlated columns only.
func serializeCorrelatedValues(row *Row, cols []string) string {
	if row == nil || len(cols) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cols))
	for _, col := range cols {
		if v, ok := row.LookupValue(col); ok {
			parts = append(parts, DT.ValueToString(v))
		} else {
			parts = append(parts, "NULL")
		}
	}
	return strings.Join(parts, ",")
}

var ErrEval = errors.New("ex: eval error")
var ErrEvalDivByZero = errors.New("ex: division by zero")
var ErrTypeMismatch = errors.New("ex: type mismatch")
var ErrSubquery = errors.New("ex: subquery not supported here")
var ErrIgnoreRow = errors.New("ex: ignore row")

func Eval(expr PS.Expr, row *Row, params []any) (any, error) {
	if expr == nil {
		return nil, nil
	}
	v, err := EvalValue(expr, row, params)
	if err != nil {
		return nil, err
	}
	return v.ToAny(), nil
}

// EvalValue evaluates an expression against a single row. For simple
// expressions (literals, column references, unary ops, aliases) it calls
// evalFallbackEvalValue directly to avoid the overhead of batch allocation.
// For binary expressions that benefit from vectorized evaluation, it
// packages the row into a synthetic 1-row batch and delegates to
// EvalBatchExpr. REQ001210.
func EvalValue(expr PS.Expr, row *Row, params []any) (Value, error) {
	if expr == nil {
		return DT.NullValue(), nil
	}
	if row == nil || row.Outer != nil {
		return evalFallbackEvalValue(expr, row, params)
	}
	// Simple expressions: bypass batch allocation and call the row-at-a-time
	// evaluator directly. This avoids rowToBatch overhead for the common case
	// of column reads, literals, and unary ops.
	switch expr.(type) {
	case *PS.Ident, *PS.NumberLiteral, *PS.FloatLiteral,
		*PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral,
		*PS.Param, *PS.StarExpr, *PS.UnaryExpr, *PS.AliasedExpr,
		*PS.FunctionCall, *PS.AggregateFunc, *PS.WindowFunc,
		*PS.CastExpr, *PS.CaseExpr, *PS.BetweenExpr,
		*PS.InExpr, *PS.ListExpr, *PS.IntervalLiteral,
		*PS.ExistsExpr, *PS.SubqueryExpr, *PS.RaiseFunc,
		*PS.BinaryExpr:
		return evalFallbackEvalValue(expr, row, params)
	}
	b := rowToBatch(row)
	col := EvalBatchExpr(expr, b, params)
	v := columnValueAt(col, 0)
	return v, nil
}

// evalFallbackEvalValue is the original row-at-a-time evaluator.
// Returns inline Value structs instead of boxed any, avoiding convT64
// overhead in the hot evaluation path. REQ000776.
func evalFallbackEvalValue(expr PS.Expr, row *Row, params []any) (Value, error) {
	if expr == nil {
		return DT.NullValue(), nil
	}

	switch e := expr.(type) {
	case *PS.NumberLiteral:
		return DT.NewIntValue(e.Val), nil
	case *PS.FloatLiteral:
		return DT.NewFloatValue(e.Val), nil
	case *PS.StringLiteral:
		return DT.NewTextValue(e.Val), nil
	case *PS.BoolLiteral:
		return DT.NewBoolValue(e.Val), nil
	case *PS.NullLiteral:
		return DT.NullValue(), nil
	case *PS.Ident:
		if row != nil {
			// REQ001202: pre-resolved SlotIdx — direct access, skip Lookup.
			if e.SlotIdx >= 0 && e.SlotIdx < len(row.Data) && e.SlotIdx < len(row.Cols) {
				if strings.EqualFold(row.Cols[e.SlotIdx], e.Name) {
					return row.Data[e.SlotIdx], nil
				}
			}
			if v, ok := row.Lookup(e.Name); ok {
				return DT.ValueFromAny(v), nil
			}
		}
		return DT.NewTextValue(e.Name), nil
	case *PS.QualifiedName:
		if row != nil {
			// REQ001202: pre-resolved SlotIdx — direct access.
			// For qualified names, only use SlotIdx when the column
			// at that index matches the FULL qualified name. This
			// prevents incorrectly reading from the inner row when
			// a correlated subquery's QN references an outer column
			// that happens to share the same bare name.
			if e.SlotIdx >= 0 && e.SlotIdx < len(row.Data) && e.SlotIdx < len(row.Cols) {
				full := e.Table + "." + e.Name
				if strings.EqualFold(row.Cols[e.SlotIdx], full) {
					return row.Data[e.SlotIdx], nil
				}
				if e.Table == "" && strings.EqualFold(row.Cols[e.SlotIdx], e.Name) {
					return row.Data[e.SlotIdx], nil
				}
			}
			if e.CachedKey == "" {
				e.CachedKey = e.Table + "." + e.Name
			}
			for cur := row; cur != nil; cur = cur.Outer {
				if v, ok := cur.Lookup(e.CachedKey); ok {
					return DT.ValueFromAny(v), nil
				}
			}
			for cur := row.Outer; cur != nil; cur = cur.Outer {
				if cur.TableName != "" && !strings.EqualFold(cur.TableName, e.Table) {
					continue
				}
				if v, ok := cur.Lookup(e.Name); ok {
					return DT.ValueFromAny(v), nil
				}
			}
			if v, ok := row.Lookup(e.Name); ok {
				return DT.ValueFromAny(v), nil
			}
		}
		return DT.NewTextValue(e.CachedKey), nil
	case *PS.Param:
		if e.Index < len(params) {
			return DT.ValueFromAny(normalizeInt(params[e.Index])), nil
		}
		return DT.NullValue(), nil
	case *PS.StarExpr:
		return DT.NewTextValue("*"), nil
	case *PS.UnaryExpr:
		return evalUnaryValue(e, row, params)
	case *PS.BinaryExpr:
		switch e.Op {
		case LX.T_AND, LX.T_OR:
			return evalBinaryShortCircuit(e, row, params)
		}
		return evalBinaryValue(e, row, params)
	case *PS.ListExpr:
		return DT.ValueFromAny(e.Items), nil
	case *PS.BetweenExpr:
		return evalBetween(e, row, params)
	case *PS.InExpr:
		return EvalInValue(e, row, params)
	case *PS.ExistsExpr:
		v, err := evalExists(e, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		return DT.ValueFromAny(v), nil
	case *PS.SubqueryExpr:
		v, err := evalScalarSubquery(e, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		return v, nil
	case *PS.IntervalLiteral:
		v, err := evalInterval(e)
		if err != nil {
			return DT.NullValue(), err
		}
		return DT.ValueFromAny(v), nil
	case *PS.CaseExpr:
		return evalCase(e, row, params)
	case *PS.AggregateFunc:
		return evalAggregate(e, row, params)
	case *PS.WindowFunc:
		return evalWindowFunc(e, row, params)
	case *PS.FunctionCall:
		return EvalFunction(e, row, params)
	case *PS.RaiseFunc:
		return evalRaise(e, row, params)
	case *PS.CastExpr:
		return evalCast(e, row, params)
	case *PS.AliasedExpr:
		return evalFallbackEvalValue(e.Expr, row, params)
	default:
		return DT.NullValue(), ErrEval
	}
}

// evalWindowFunc evaluates a WindowFunc expression. Window functions
// require WindowOperator execution; direct EvalValue only reports the error.
func evalWindowFunc(e *PS.WindowFunc, row *Row, params []any) (Value, error) {
	return DT.NullValue(), fmt.Errorf("window function %s requires WindowOperator execution", e.Name)
}

// evalBinaryShortCircuit handles AND/OR with three-valued logic and
// short-circuit evaluation (REQ000776). Right side is only evaluated
// when the left side does not determine the result.
func evalBinaryShortCircuit(e *PS.BinaryExpr, row *Row, params []any) (Value, error) {
	left, err := evalFallbackEvalValue(e.Left, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	switch e.Op {
	case LX.T_AND:
		if left.Kind == KindBool && !left.Bo {
			return DT.NewBoolValue(false), nil
		}
		right, err := evalFallbackEvalValue(e.Right, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		return bandValue(left, right), nil
	case LX.T_OR:
		if left.Kind == KindBool && left.Bo {
			return DT.NewBoolValue(true), nil
		}
		right, err := evalFallbackEvalValue(e.Right, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		return borValue(left, right), nil
	}
	return DT.NullValue(), ErrEval
}


// evalUnaryValue is the Value-typed fast path for unary operators
// (REQ000776). It calls EvalValue and dispatches via Kind switch.
func evalUnaryValue(e *PS.UnaryExpr, row *Row, params []any) (Value, error) {
	operand, err := evalFallbackEvalValue(e.Operand, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	if operand.Kind == KindNull {
		return DT.NullValue(), nil
	}
	switch e.Op {
	case LX.T_MINUS:
		switch operand.Kind {
		case KindInt:
			return DT.NewIntValue(-operand.I64), nil
		case KindFloat:
			return DT.NewFloatValue(-operand.F64), nil
		}
		return DT.NullValue(), nil
	case LX.T_PLUS:
		return operand, nil
	case LX.T_NOT:
		if operand.Kind == KindBool {
			return DT.NewBoolValue(!operand.Bo), nil
		}
		return DT.NewBoolValue(!isValueTruthy(operand)), nil
	case LX.T_BITNOT:
		if operand.Kind == KindInt {
			return DT.NewIntValue(^operand.I64), nil
		}
	}
	return DT.NullValue(), ErrEval
}

// isValueTruthy mirrors the truthy() logic for Value types so the
// evalUnaryValue NOT path can avoid boxing into any.
func isValueTruthy(v Value) bool {
	switch v.Kind {
	case KindNull:
		return false
	case KindInt:
		return v.I64 != 0
	case KindFloat:
		return v.F64 != 0
	case KindText:
		return v.S != ""
	case KindBool:
		return v.Bo
	case KindBlob:
		return len(v.B) > 0
	}
	return false
}




// evalBinaryValue is the Value-typed fast path for comparison and
// arithmetic binary operators. REQ000776. It calls EvalValue to get
// Value-typed results, then dispatches via Kind switch in the
// Value-based helpers (compareValue, equalValueValue,
// numericArithValue) to avoid interface conversion.
func evalBinaryValue(e *PS.BinaryExpr, row *Row, params []any) (Value, error) {
	left, err := evalFallbackEvalValue(e.Left, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	right, err := evalFallbackEvalValue(e.Right, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	switch e.Op {
	case LX.T_EQ:
		if left.Kind == KindNull || right.Kind == KindNull {
			return DT.NullValue(), nil
		}
		return DT.NewBoolValue(PL.EqualValueValue(left, right)), nil
	case LX.T_NE:
		if left.Kind == KindNull || right.Kind == KindNull {
			return DT.NullValue(), nil
		}
		return DT.NewBoolValue(!PL.EqualValueValue(left, right)), nil
	case LX.T_LT:
		if left.Kind == KindNull || right.Kind == KindNull {
			return DT.NullValue(), nil
		}
		return DT.NewBoolValue(PL.CompareValue(left, right) < 0), nil
	case LX.T_LE:
		if left.Kind == KindNull || right.Kind == KindNull {
			return DT.NullValue(), nil
		}
		return DT.NewBoolValue(PL.CompareValue(left, right) <= 0), nil
	case LX.T_GT:
		if left.Kind == KindNull || right.Kind == KindNull {
			return DT.NullValue(), nil
		}
		return DT.NewBoolValue(PL.CompareValue(left, right) > 0), nil
	case LX.T_GE:
		if left.Kind == KindNull || right.Kind == KindNull {
			return DT.NullValue(), nil
		}
		return DT.NewBoolValue(PL.CompareValue(left, right) >= 0), nil
	case LX.T_PLUS, LX.T_MINUS, LX.T_STAR, LX.T_SLASH:
		var opRune rune
		switch e.Op {
		case LX.T_PLUS:
			opRune = '+'
		case LX.T_MINUS:
			opRune = '-'
		case LX.T_STAR:
			opRune = '*'
		case LX.T_SLASH:
			opRune = '/'
		}
		r, err := NumericArithValue(left, right, opRune)
		if err != nil {
			return DT.NullValue(), err
		}
		return r, nil
	case LX.T_MOD:
		return modValue(left, right)
	case LX.T_DIV:
		return divValue(left, right)
	case LX.T_BITAND:
		return bitandValue(left, right)
	case LX.T_BITOR:
		return bitorValue(left, right)
	case LX.T_BITXOR:
		return bitxorValue(left, right)
	case LX.T_LSHIFT:
		return lshiftValue(left, right)
	case LX.T_RSHIFT:
		return rshiftValue(left, right)
	case LX.T_CONCAT:
		return ConcatValue(left, right)
	case LX.T_LIKE:
		var esc string
		if e.Escape != nil {
			v, err := evalFallbackEvalValue(e.Escape, row, params)
			if err != nil {
				return DT.NullValue(), err
			}
			if v.Kind == KindText && len(v.S) == 1 {
				esc = v.S
			} else if v.Kind != KindNull {
				return DT.NullValue(), ErrEval
			}
		}
		return likeValue(left, right, esc)
	case LX.T_GLOB:
		return GlobValue(left, right)
	case LX.T_IS:
		// `x IS NOT NULL` parses as BinaryExpr{T_IS, x, UnaryExpr{T_NOT, NULL}}.
		if u, ok := e.Right.(*PS.UnaryExpr); ok && u.Op == LX.T_NOT {
			if _, isNull := u.Operand.(*PS.NullLiteral); isNull {
				if left.Kind == KindNull {
					return DT.NewBoolValue(false), nil
				}
				return DT.NewBoolValue(true), nil
			}
		}
		return isValue(left, right)
	}
	return DT.NullValue(), ErrEval
}

func evalBetween(e *PS.BetweenExpr, row *Row, params []any) (Value, error) {
	expr, err := evalFallbackEvalValue(e.Expr, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	low, err := evalFallbackEvalValue(e.Low, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	high, err := evalFallbackEvalValue(e.High, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	// Three-valued logic for BETWEEN: x BETWEEN y AND z = x >= y AND x <= z.
	// NULL AND FALSE = FALSE; NULL AND TRUE = NULL; TRUE AND TRUE = TRUE.
	var lowOk, highOk bool
	var lowGE, highLE bool
	if expr.Kind != KindNull && low.Kind != KindNull {
		lowOk = true
		lowGE = PL.CompareValue(expr, low) >= 0
	}
	if expr.Kind != KindNull && high.Kind != KindNull {
		highOk = true
		highLE = PL.CompareValue(expr, high) <= 0
	}
	if lowOk && !lowGE {
		return DT.NewBoolValue(false), nil
	}
	if highOk && !highLE {
		return DT.NewBoolValue(false), nil
	}
	if lowOk && highOk {
		return DT.NewBoolValue(true), nil
	}
	return DT.NullValue(), nil
}



// evalInValue is the Value-typed handler for IN expressions (REQ000776).
// Uses EvalValue to avoid the any->Value conversion. For 4+ item
// lists, routes through evalInHashValue which uses per-kind
// hash sets (int64/float64/string) for zero-boxing O(1) probing.
func EvalInValue(e *PS.InExpr, row *Row, params []any) (Value, error) {
	target, err := evalFallbackEvalValue(e.Expr, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	if e.Subquery != nil {
		// Subquery path still uses legacy any-based hash; convert
		// the result back to Value.
		r, err := evalInSubquery(target.ToAny(), e.Subquery, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		return DT.ValueFromAny(r), nil
	}
	if len(e.List) == 0 {
		return DT.NewBoolValue(false), nil
	}
	if target.Kind == KindNull {
		return DT.NullValue(), nil
	}
	// Hash-set path for 4+ items.
	if len(e.List) >= 4 {
		return EvalInHashValue(e, target, row, params)
	}
	// Linear-scan path for short lists.
	hadNull := false
	for _, item := range e.List {
		v, err := evalFallbackEvalValue(item, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		if v.Kind == KindNull {
			hadNull = true
			continue
		}
		if PL.EqualValueValue(target, v) {
			return DT.NewBoolValue(true), nil
		}
	}
	if hadNull {
		return DT.NullValue(), nil
	}
	return DT.NewBoolValue(false), nil
}

// REQ000817: InHashCache tracks the hash set for IN-list probing.
// For int64-only lists, int64Set is used to avoid any boxing.
// Keyed by *PS.InExpr pointer identity; entries live for the
// query lifetime.
type InHashCache struct {
	set      map[any]struct{}
	Int64Set map[int64]struct{}
	hadNull  bool
}

var InHashCacheMap = map[*PS.InExpr]*InHashCache{}

// evalInHashValue is the Value-typed variant of evalInHash (REQ000776).
// Converts target to any for probe; the hash set is shared via
// the legacy InHashCacheMap.
func EvalInHashValue(e *PS.InExpr, target Value, row *Row, params []any) (Value, error) {
	vr, err := EvalInHash(e, target.ToAny(), row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	return DT.ValueFromAny(vr), nil
}

// REQ000817: evalInHash builds a cached hash set for O(1) IN-list probing.
// For int64-only lists, uses an int64 map to avoid Value boxing.
func EvalInHash(e *PS.InExpr, target any, row *Row, params []any) (any, error) {
	cached := InHashCacheMap[e]
	if cached == nil {
		cached = &InHashCache{
			set:      make(map[any]struct{}, len(e.List)),
			Int64Set: make(map[int64]struct{}, len(e.List)),
		}
		int64Only := true
		for _, item := range e.List {
			v, err := evalFallbackEvalValue(item, row, params)
			if err != nil {
				return nil, err
			}
			if v.Kind == KindNull {
				cached.hadNull = true
				continue
			}
			cached.set[v.ToAny()] = struct{}{}
			if v.Kind == KindInt {
				cached.Int64Set[v.I64] = struct{}{}
			} else {
				int64Only = false
			}
		}
		if !int64Only {
			cached.Int64Set = nil
		}
		InHashCacheMap[e] = cached
	}
	if cached.Int64Set != nil {
		if t, ok := target.(int64); ok {
			if _, found := cached.Int64Set[t]; found {
				return true, nil
			}
			if cached.hadNull {
				return nil, nil
			}
			return false, nil
		}
	}
	if _, ok := cached.set[target]; ok {
		return true, nil
	}
	if cached.hadNull {
		return nil, nil
	}
	return false, nil
}

func evalInSubquery(target any, subq PS.Stmt, outer *Row, params []any) (any, error) {
	pl := getSubqueryPlanner(outer)
	if pl == nil {
		return nil, ErrSubquery
	}
	rows, err := pl.ExecuteSubquery(context.Background(), subq, outer, params)
	if err != nil {
		return nil, err
	}
	// REQ001059: three-valued IN/NOT IN with NULL in subquery.
	// Per SQL standard:
	//   NULL IN (non-empty set) → NULL (all comparisons UNKNOWN)
	//   NULL IN (empty set)    → FALSE (no element could match)
	// Previous code returned FALSE when RHS had no NULLs, which
	// conflates "all comparisons are UNKNOWN" with "no NULLs in RHS".
	if target == nil {
		if len(rows) == 0 {
			return false, nil
		}
		return nil, nil
	}
	hadNull := false
	for _, row := range rows {
		if len(row.Cols) == 0 {
			continue
		}
		if row.Data[0].IsNull() {
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
	v, err := EvalValue(e, row, params)
	if err != nil {
		return nil, err
	}
	return v.ToAny(), nil
}

// getSubqueryPlanner returns the query planner from the outer row's
// execution context. Priority order:
//  1. Row's ExecContext.Planner (REQ000586)
//  2. Row's outer-chain planner (REQ000366)
func getSubqueryPlanner(outer *Row) PL.QueryPlanner {
	if ec := DT.ExecContextFromRow(outer); ec != nil && ec.Planner != nil {
		return ec.Planner
	}
	if p := outer.GetPlanner(); p != nil {
		return p
	}
	return nil
}

func evalExists(e *PS.ExistsExpr, outer *Row, params []any) (any, error) {
	pl := getSubqueryPlanner(outer)
	if pl == nil {
		return nil, ErrSubquery
	}
	// REQ001073: short-circuit existential check — stop scanning the
	// subquery after the first matching row instead of materializing
	// all rows. The type assertion checks for the Planner's
	// ExecuteSubqueryFirstMatch method (which lives in EX/planner.go);
	// non-EX planners fall through to the legacy materialization path.
	if sc, ok := pl.(interface {
		ExecuteSubqueryFirstMatch(ctx context.Context, stmt PS.Stmt, outer *Row, params []any) (bool, error)
	}); ok {
		return sc.ExecuteSubqueryFirstMatch(context.Background(), e.Subquery, outer, params)
	}
	rows, err := pl.ExecuteSubquery(context.Background(), e.Subquery, outer, params)
	if err != nil {
		return nil, err
	}
	return len(rows) > 0, nil
}

func evalScalarSubquery(e *PS.SubqueryExpr, outer *Row, params []any) (Value, error) {
	if _, ok := e.Subquery.(*PS.Select); !ok {
		return DT.NullValue(), ErrSubquery
	}

	// Compute cache key from the serialized statement.
	// Non-correlated subqueries (no outer column references)
	// are cached globally to avoid O(N) re-evaluations.
	// REQ001202-idx: cachedSubqueryKey memoizes the serialized key
	// per SubqueryExpr pointer, avoiding repeated AST hashing.
	key := cachedSubqueryKey(e)

	// Try global cache first — only safe when outer is nil
	// (no outer columns the subquery could reference).
	if outer == nil {
		if cached, ok := globalSubqueryCache.Load(key); ok {
			if v, ok := cached.(Value); ok {
				return v, nil
			}
			return DT.ValueFromAny(cached), nil
		}
	} else {
		// REQ001228: Correlated subquery cache — keyed by planKey
		// + the values of only the correlated columns.
		correlatedCols := cachedCorrelatedCols(e)
		if len(correlatedCols) > 0 {
			correlatedKey := key + ":" + serializeCorrelatedValues(outer, correlatedCols)
			if v, ok := correlatedSubqueryCache.Get(correlatedKey); ok {
				return v, nil
			}
		} else {
			// No correlated columns detected — same result for all
			// outer rows, cache globally.
			if cached, ok := globalSubqueryCache.Load(key); ok {
				if v, ok := cached.(Value); ok {
					return v, nil
				}
				return DT.ValueFromAny(cached), nil
			}
		}
	}

	pl := getSubqueryPlanner(outer)
	if pl == nil {
		return DT.NullValue(), ErrSubquery
	}
	rows, err := pl.ExecuteSubquery(context.Background(), e.Subquery, outer, params)
	if err != nil {
		return DT.NullValue(), err
	}
	var result Value
	if len(rows) == 0 || len(rows[0].Data) == 0 {
		result = DT.NullValue()
	} else {
		result = rows[0].Data[0]
	}
	// Cache the result: globally for non-correlated, LRU for correlated.
	if outer == nil {
		globalSubqueryCache.Store(key, result)
	} else {
		correlatedCols := cachedCorrelatedCols(e)
		if len(correlatedCols) > 0 {
			// Correlated subquery cache with fine-grained keying
			correlatedKey := key + ":" + serializeCorrelatedValues(outer, correlatedCols)
			correlatedSubqueryCache.Put(correlatedKey, result)
		} else {
			// Non-correlated: cache globally
			globalSubqueryCache.Store(key, result)
		}
	}
	return result, nil
}

func evalInterval(e *PS.IntervalLiteral) (any, error) {
	n, unit, ok := UT.ParseInterval(e.Value + " " + e.Unit)
	if !ok {
		return nil, fmt.Errorf("invalid interval: %s %s", e.Value, e.Unit)
	}
	return &UT.IntervalValue{Amount: n, Unit: unit}, nil
}

func evalCast(e *PS.CastExpr, row *Row, params []any) (Value, error) {
	v, err := evalFallbackEvalValue(e.Expr, row, params)
	if err != nil {
		return DT.NullValue(), err
	}
	if v.Kind == KindNull {
		return DT.NullValue(), nil
	}
	if e.Type == nil {
		return v, nil
	}
	switch LX.TokenType(e.Type.Type) {
	case LX.T_INT_KW, LX.T_BIGINT:
		switch v.Kind {
		case KindInt:
			return v, nil
		case KindFloat:
			return DT.NewIntValue(int64(v.F64)), nil
		case KindText:
			n, err := strconv.ParseInt(v.S, 10, 64)
			if err != nil {
				return DT.NullValue(), fmt.Errorf("ex: cast %q to int: %w", v.S, err)
			}
			return DT.NewIntValue(n), nil
		case KindBool:
			if v.Bo {
				return DT.NewIntValue(1), nil
			}
			return DT.NewIntValue(0), nil
		}
	case LX.T_FLOAT_KW:
		switch v.Kind {
		case KindInt:
			return DT.NewFloatValue(float64(v.I64)), nil
		case KindFloat:
			return v, nil
		case KindText:
			f, err := strconv.ParseFloat(v.S, 64)
			if err != nil {
				return DT.NullValue(), fmt.Errorf("ex: cast %q to float: %w", v.S, err)
			}
			return DT.NewFloatValue(f), nil
		}
	case LX.T_TEXT:
		return DT.NewTextValue(v.String()), nil
	case LX.T_DECIMAL, LX.T_NUMERIC:
		r, err := UT.EvalDecimalCast(v.ToAny(), e.Type.Precision, e.Type.Scale)
		if err != nil {
			return DT.NullValue(), err
		}
		return DT.ValueFromAny(r), nil
	case LX.T_BOOL:
		return DT.NewBoolValue(castToBoolValue(v)), nil
	case LX.T_BLOB:
		switch v.Kind {
		case KindText:
			return DT.NewBlobValue([]byte(v.S)), nil
		case KindBlob:
			return v, nil
		default:
			return DT.NewBlobValue([]byte(v.String())), nil
		}
	}
	return DT.NullValue(), ErrEval
}

// castToBoolValue is the Value-typed variant of castToBool (REQ000776).
func castToBoolValue(v Value) bool {
	switch v.Kind {
	case KindNull:
		return false
	case KindBool:
		return v.Bo
	case KindInt:
		return v.I64 != 0
	case KindFloat:
		return v.F64 != 0
	case KindText:
		s := v.S
		if s == "" || s == "0" || s == "false" || s == "FALSE" {
			return false
		}
		return true
	case KindBlob:
		return len(v.B) > 0 && v.B[0] != 0
	}
	return true
}

func evalCase(e *PS.CaseExpr, row *Row, params []any) (Value, error) {
	if e.Expr != nil {
		target, err := evalFallbackEvalValue(e.Expr, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		if target.Kind != KindNull {
			for _, w := range e.WhenList {
				v, err := evalFallbackEvalValue(w.Cond, row, params)
				if err != nil {
					return DT.NullValue(), err
				}
				if PL.EqualValueValue(target, v) {
					return evalFallbackEvalValue(w.Then, row, params)
				}
			}
		}
	} else {
		for _, w := range e.WhenList {
			cond, err := evalFallbackEvalValue(w.Cond, row, params)
			if err != nil {
				return DT.NullValue(), err
			}
			if isValueTruthy(cond) {
				return evalFallbackEvalValue(w.Then, row, params)
			}
		}
	}
	if e.Else != nil {
		return evalFallbackEvalValue(e.Else, row, params)
	}
	return DT.NullValue(), nil
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

func evalAggregate(e *PS.AggregateFunc, row *Row, params []any) (Value, error) {
	if row != nil {
		if _, ok := e.Arg.(*PS.StarExpr); ok {
			name := e.Name + "(*)"
			if v, found := row.Lookup(name); found {
				return DT.ValueFromAny(v), nil
			}
		}
		if ident, ok := e.Arg.(*PS.Ident); ok {
			name := e.Name + "(" + ident.Name + ")"
			if v, found := row.Lookup(name); found {
				return DT.ValueFromAny(v), nil
			}
		}
		if v, found := row.Lookup(e.Name); found {
			return DT.ValueFromAny(v), nil
		}
	}
	switch strings.ToUpper(e.Name) {
	case "COUNT":
		return DT.NewIntValue(0), nil
	case "SUM":
		return DT.NewIntValue(0), nil
	case "AVG":
		return DT.NewFloatValue(0), nil
	case "MIN":
		return DT.NullValue(), nil
	case "MAX":
		return DT.NullValue(), nil
	}
	return DT.NullValue(), ErrEval
}

// valueFromAnyWrap converts (any, error) from a legacy eval helper to
// (Value, error). REQ000776 bridge helper.
func valueFromAnyWrap(v any, err error) (Value, error) {
	if err != nil {
		return DT.NullValue(), err
	}
	return DT.ValueFromAny(v), nil
}

func EvalFunction(e *PS.FunctionCall, row *Row, params []any) (Value, error) {
	// REQ000978: registry-based dispatch. Adding a new function is
	// a one-line registration in function_registry.go's init(),
	// not an edit to a switch block.
	if impl, ok := ScalarFuncRegistry[e.Name]; ok {
		return impl(e.Args, row, params)
	}
	if UT.IsDateTimeFunc(e.Name) {
		args := make([]any, len(e.Args))
		for i, arg := range e.Args {
			v, err := evalFallbackEvalValue(arg, row, params)
			if err != nil {
				return DT.NullValue(), err
			}
			args[i] = v.ToAny()
		}
		v, err := UT.EvalDateTimeFunc(e.Name, args)
		if err != nil {
			return DT.NullValue(), err
		}
		return DT.ValueFromAny(v), nil
	}
	if UT.IsJSONFunc(e.Name) {
		args := make([]any, len(e.Args))
		for i, arg := range e.Args {
			v, err := evalFallbackEvalValue(arg, row, params)
			if err != nil {
				return DT.NullValue(), err
			}
			args[i] = v.ToAny()
		}
		v, err := UT.EvalJSONFunc(e.Name, args)
		if err != nil {
			return DT.NullValue(), err
		}
		return DT.ValueFromAny(v), nil
	}
	return DT.NullValue(), ErrEval
}

// ErrTriggerAbort is returned by RAISE(ABORT, ...) evaluation to
// signal that the trigger action should abort with an error message.
// REQ000560.
var ErrTriggerAbort = errors.New("ex: trigger abort")

func evalRaise(e *PS.RaiseFunc, row *Row, params []any) (Value, error) {
	action := strings.ToUpper(e.Action)
	if action == "IGNORE" {
		return DT.NullValue(), ErrIgnoreRow
	}
	var msg string
	if e.Message != nil {
		v, err := evalFallbackEvalValue(e.Message, row, params)
		if err != nil {
			return DT.NullValue(), err
		}
		if v.Kind == KindText {
			msg = v.S
		}
	}
	return DT.NullValue(), fmt.Errorf("%w: %s", ErrTriggerAbort, msg)
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

func EvalAbs(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	switch v.Kind {
	case KindInt:
		if v.I64 == math.MinInt64 {
			return nil, fmt.Errorf("abs: integer overflow")
		}
		if v.I64 < 0 {
			return -v.I64, nil
		}
		return v.I64, nil
	case KindFloat:
		if v.F64 < 0 {
			return -v.F64, nil
		}
		return v.F64, nil
	}
	return 0.0, nil
}

func evalHex(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	switch v.Kind {
	case KindInt:
		// SQLite converts the integer to its text form first,
		// then hex-encodes that text. HEX(255) → "323535"
		// (the hex of the three ASCII digits).
		// REQ000763: use strconv.AppendInt + hex.Encode to
		// avoid intermediate []byte allocation from FormatInt.
		buf := make([]byte, 0, 24)
		buf = strconv.AppendInt(buf, v.I64, 10)
		h := make([]byte, hex.EncodedLen(len(buf)))
		hex.Encode(h, buf)
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	case KindFloat:
		buf := make([]byte, 0, 32)
		buf = strconv.AppendFloat(buf, v.F64, 'g', -1, 64)
		h := make([]byte, hex.EncodedLen(len(buf)))
		hex.Encode(h, buf)
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	case KindBlob:
		// REQ001031: use hex.Encode + in-place uppercase to avoid
		// the intermediate string allocation from EncodeToString.
		h := make([]byte, hex.EncodedLen(len(v.B)))
		hex.Encode(h, v.B)
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	case KindText:
		// REQ001031: hex-encode the string bytes directly.
		h := make([]byte, hex.EncodedLen(len(v.S)))
		hex.Encode(h, []byte(v.S))
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	default:
		s := DT.ValueToString(v)
		h := make([]byte, hex.EncodedLen(len(s)))
		hex.Encode(h, []byte(s))
		for i, c := range h {
			if c >= 'a' && c <= 'f' {
				h[i] = c - 32
			}
		}
		return string(h), nil
	}
}

func evalRound(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	// REQ000772: int64 fast path when no places arg is given or
	// places==0. Avoid the float64 conversion in numericFloat for
	// the common case of ROUND(int_col).
	if len(args) == 1 {
		if v.Kind == KindInt {
			return v.I64, nil
		}
	}
	var x float64
	var ok bool
	if v.Kind == KindInt {
		x = float64(v.I64)
		ok = true
	} else if v.Kind == KindFloat {
		x = v.F64
		ok = true
	}
	if !ok {
		return 0.0, nil
	}
	places := int64(0)
	if len(args) == 2 {
		pv, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if pv.Kind != KindNull {
			if pv.Kind == KindInt {
				if pv.I64 < 0 {
					places = 0
				} else {
					places = pv.I64
				}
			} else if pv.Kind == KindFloat {
				p := int64(pv.F64)
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
	rawStr, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000606: SUBSTR(NULL, ...) and SUBSTR(s, NULL, ...) must
	// return NULL, not "<nil>" (the fmt.Sprint result).
	if rawStr.Kind == KindNull {
		return nil, nil
	}
	// REQ001040: fast path for text values — avoid boxing + fmt.Sprint.
	var s string
	switch rawStr.Kind {
	case KindText:
		s = rawStr.S
	case KindBlob:
		s = string(rawStr.B)
	default:
		s = rawStr.String()
	}
	startV, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if startV.Kind == KindNull {
		return nil, nil
	}
	var start int64
	var startOk bool
	if startV.Kind == KindInt {
		start = startV.I64
		startOk = true
	} else if startV.Kind == KindFloat {
		start = int64(startV.F64)
		startOk = true
	}
	if !startOk {
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
		lenV, err := evalFallbackEvalValue(args[2], row, params)
		if err != nil {
			return nil, err
		}
		var length int64
		var lenOk bool
		if lenV.Kind == KindInt {
			length = lenV.I64
			lenOk = true
		} else if lenV.Kind == KindFloat {
			length = int64(lenV.F64)
			lenOk = true
		}
		if !lenOk {
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
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			return nil, nil // Any NULL arg → NULL result
		}
		var n int64
		if v.Kind == KindInt {
			n = v.I64
		} else if v.Kind == KindFloat {
			n = int64(v.F64)
		} else {
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
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			return nil, nil // Any NULL → NULL result
		}
		sb.WriteString(DT.ValueToString(v))
	}
	return sb.String(), nil
}

// evalConcatWS concatenates with separator. First arg is separator.
// SEP=NULL → NULL. Skips NULL values. REQ000388.
func evalConcatWS(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 2 {
		return nil, ErrEval
	}
	sep, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if sep.Kind == KindNull {
		return nil, nil // NULL separator → NULL result
	}
	sepStr := DT.ValueToString(sep)
	var sb strings.Builder
	first := true
	for i := 1; i < len(args); i++ {
		v, err := evalFallbackEvalValue(args[i], row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue // Skip NULL values
		}
		if !first {
			sb.WriteString(sepStr)
		}
		sb.WriteString(DT.ValueToString(v))
		first = false
	}
	return sb.String(), nil
}

// evalFormat implements printf-style formatting. REQ000389.
func evalFormat(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	fmtV, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if fmtV.Kind == KindNull {
		return nil, nil
	}
	fmtStr := fmtV.S
	if fmtV.Kind != KindText {
		fmtStr = fmtV.String()
	}
	// Convert remaining args to any for fmt.Sprintf
	fmtArgs := make([]any, len(args)-1)
	for i := 1; i < len(args); i++ {
		v, err := evalFallbackEvalValue(args[i], row, params)
		if err != nil {
			return nil, err
		}
		fmtArgs[i-1] = v.ToAny()
	}
	return fmt.Sprintf(fmtStr, fmtArgs...), nil
}

// evalLtrim trims leading characters. Default trim chars are spaces.
// REQ000397.
func evalLtrim(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
	if len(args) >= 2 {
		trimV, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV.Kind != KindNull {
			return strings.TrimLeft(s, DT.ValueToString(trimV)), nil
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
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
	if len(args) >= 2 {
		trimV, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV.Kind != KindNull {
			return strings.TrimRight(s, DT.ValueToString(trimV)), nil
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
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
	if len(args) >= 2 {
		trimV, err := evalFallbackEvalValue(args[1], row, params)
		if err != nil {
			return nil, err
		}
		if trimV.Kind != KindNull {
			return strings.Trim(s, DT.ValueToString(trimV)), nil
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
	x, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if x.Kind == KindNull {
		return nil, nil
	}
	y, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	z, err := evalFallbackEvalValue(args[2], row, params)
	if err != nil {
		return nil, err
	}
	xs := DT.ValueToString(x)
	if y.Kind == KindNull {
		return xs, nil // NULL pattern → return X unchanged
	}
	ys := DT.ValueToString(y)
	zs := ""
	if z.Kind != KindNull {
		zs = DT.ValueToString(z)
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
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return "NULL", nil
	}
	switch v.Kind {
	case KindText:
		// Escape single quotes by doubling
		escaped := strings.ReplaceAll(v.S, "'", "''")
		return "'" + escaped + "'", nil
	case KindInt, KindFloat, KindBool:
		// Numbers and booleans are not quoted
		return DT.ValueToString(v), nil
	default:
		return "'" + strings.ReplaceAll(DT.ValueToString(v), "'", "''") + "'", nil
	}
}

// evalTypeof returns the type name of X: "null", "integer", "real",
// "text", or "blob". REQ000412.
func evalTypeof(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return "null", nil
	}
	switch v.Kind {
	case KindInt, KindBool:
		return "integer", nil
	case KindFloat:
		return "real", nil
	case KindText:
		return "text", nil
	case KindBlob:
		return "blob", nil
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
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	// REQ000621: for []byte return the raw byte length, not the
	// fmt.Sprint representation (which yields "[104 101 ...]" for
	// "Hello"). For strings, return the byte length directly too
	// rather than going through fmt.Sprint.
	if v.Kind == KindBlob {
		return int64(len(v.B)), nil
	}
	if v.Kind == KindText {
		return int64(len(v.S)), nil
	}
	return int64(len(DT.ValueToString(v))), nil
}

// evalUnicode returns the Unicode code point of the first character.
// REQ000414.
func evalUnicode(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 1 {
		return nil, ErrEval
	}
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if v.Kind == KindNull {
		return nil, nil
	}
	s := DT.ValueToString(v)
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
	cond, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if isValueTruthy(cond) {
		return evalFallbackEvalValue(args[1], row, params)
	}
	return evalFallbackEvalValue(args[2], row, params)
}

// evalInstr returns the 1-based position of Y in X, or 0 if not found.
// REQ000393.
func evalInstr(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) != 2 {
		return nil, ErrEval
	}
	x, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000620: NULL on either side must return NULL, not 0.
	if x.Kind == KindNull {
		return nil, nil
	}
	y, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if y.Kind == KindNull {
		return nil, nil
	}
	xs := DT.ValueToString(x)
	ys := DT.ValueToString(y)
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
	v, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	// REQ000619: SIGN(NULL) must return NULL, not 0.
	if v.Kind == KindNull {
		return nil, nil
	}
	// REQ000772: int64 fast path — SIGN on int64 only needs a
	// single comparison instead of the float64 conversion in
	// numericFloat.
	if v.Kind == KindInt {
		if v.I64 < 0 {
			return int64(-1), nil
		} else if v.I64 > 0 {
			return int64(1), nil
		}
		return int64(0), nil
	}
	if v.Kind == KindFloat {
		if v.F64 < 0 {
			return int64(-1), nil
		} else if v.F64 > 0 {
			return int64(1), nil
		}
		return int64(0), nil
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
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		val := v.ToAny()
		if maxV == nil {
			maxV = val
			continue
		}
		// Compare with current max
		if compare(val, maxV) > 0 {
			maxV = val
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
		v, err := evalFallbackEvalValue(arg, row, params)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindNull {
			continue
		}
		val := v.ToAny()
		if minV == nil {
			minV = val
			continue
		}
		if compare(val, minV) < 0 {
			minV = val
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
	nV, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	var n int64
	if nV.Kind == KindInt {
		n = nV.I64
	} else if nV.Kind == KindFloat {
		n = int64(nV.F64)
	} else {
		return nil, nil
	}
	if n < 0 {
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
	nV, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	var n int64
	if nV.Kind == KindInt {
		n = nV.I64
	} else if nV.Kind == KindFloat {
		n = int64(nV.F64)
	} else {
		return nil, nil
	}
	if n < 0 {
		return nil, nil
	}
	return make([]byte, n), nil
}

func toInt64(v any) (int64, bool) {
	if val, ok := v.(Value); ok {
		v = val.ToAny()
	}
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
	if av, ok := a.(Value); ok {
		a = av.ToAny()
	}
	if bv, ok := b.(Value); ok {
		b = bv.ToAny()
	}
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


// numericArithValue is the Value-based variant of numericArith that
// operates directly on the tagged-union Value type (REQ000776). It
// switches on a.Kind to avoid the interface conversion path. Returns
// a new Value or NULL for overflow/division by zero.
func NumericArithValue(a, b Value, op rune) (Value, error) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue(), nil
	}
	// int64-int64 fast path.
	if a.Kind == KindInt && b.Kind == KindInt {
		ai, bi := a.I64, b.I64
		switch op {
		case '+':
			if (bi > 0 && ai > math.MaxInt64-bi) || (bi < 0 && ai < math.MinInt64-bi) {
				return DT.NullValue(), nil
			}
			return DT.NewIntValue(ai + bi), nil
		case '-':
			if (bi < 0 && ai > math.MaxInt64+bi) || (bi > 0 && ai < math.MinInt64+bi) {
				return DT.NullValue(), nil
			}
			return DT.NewIntValue(ai - bi), nil
		case '*':
			if ai == 0 || bi == 0 {
				return DT.NewIntValue(0), nil
			}
			if ai == -1 && bi == math.MinInt64 {
				return DT.NullValue(), nil
			}
			if bi == -1 && ai == math.MinInt64 {
				return DT.NullValue(), nil
			}
			if ai > 0 && bi > 0 && ai > math.MaxInt64/bi {
				return DT.NullValue(), nil
			}
			if ai < 0 && bi < 0 && ai < math.MaxInt64/bi {
				return DT.NullValue(), nil
			}
			if (ai > 0 && bi < 0 && bi < math.MinInt64/ai) ||
				(ai < 0 && bi > 0 && ai < math.MinInt64/bi) {
				return DT.NullValue(), nil
			}
			return DT.NewIntValue(ai * bi), nil
		case '/':
			if bi == 0 {
				return DT.NullValue(), nil
			}
			return DT.NewIntValue(ai / bi), nil
		}
	}
	// Float path for mixed/float.
	var af, bf float64
	if a.Kind == KindFloat {
		af = a.F64
	} else if a.Kind == KindInt {
		af = float64(a.I64)
	} else {
		return DT.NullValue(), nil
	}
	if b.Kind == KindFloat {
		bf = b.F64
	} else if b.Kind == KindInt {
		bf = float64(b.I64)
	} else {
		return DT.NullValue(), nil
	}
	switch op {
	case '+':
		return DT.NewFloatValue(af + bf), nil
	case '-':
		return DT.NewFloatValue(af - bf), nil
	case '*':
		return DT.NewFloatValue(af * bf), nil
	case '/':
		if bf == 0 {
			return DT.NullValue(), nil
		}
		return DT.NewFloatValue(af / bf), nil
	}
	return DT.NullValue(), nil
}

// Value-native helpers for binary ops (REQ000776).

func bandValue(a, b Value) Value {
	if (a.Kind == KindBool && !a.Bo) || (b.Kind == KindBool && !b.Bo) {
		return DT.NewBoolValue(false)
	}
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue()
	}
	return DT.NewBoolValue(true)
}

func borValue(a, b Value) Value {
	if (a.Kind == KindBool && a.Bo) || (b.Kind == KindBool && b.Bo) {
		return DT.NewBoolValue(true)
	}
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue()
	}
	return DT.NewBoolValue(false)
}

// modValue implements Value-native modulo (REQ000776).
func modValue(a, b Value) (Value, error) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue(), nil
	}
	if a.Kind == KindInt && b.Kind == KindInt {
		if b.I64 == 0 {
			return DT.NullValue(), nil
		}
		return DT.NewIntValue(a.I64 % b.I64), nil
	}
	af, aok := toFloat64(a)
	bf, bok := toFloat64(b)
	if !aok || !bok {
		return DT.NullValue(), nil
	}
	if bf == 0 {
		return DT.NullValue(), nil
	}
	return DT.NewFloatValue(math.Mod(af, bf)), nil
}

// divValue implements integer division (DIV). REQ001193.
// Returns NULL for NULL inputs or division by zero.
func divValue(a, b Value) (Value, error) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue(), nil
	}
	if a.Kind == KindInt && b.Kind == KindInt {
		if b.I64 == 0 {
			return DT.NullValue(), nil
		}
		return DT.NewIntValue(a.I64 / b.I64), nil
	}
	af, aok := toFloat64(a)
	bf, bok := toFloat64(b)
	if !aok || !bok {
		return DT.NullValue(), nil
	}
	if bf == 0 {
		return DT.NullValue(), nil
	}
	return DT.NewIntValue(int64(af / bf)), nil
}

func toFloat64(v Value) (float64, bool) {
	switch v.Kind {
	case KindInt:
		return float64(v.I64), true
	case KindFloat:
		return v.F64, true
	}
	return 0, false
}

func bitandValue(a, b Value) (Value, error) {
	if a.Kind != KindInt || b.Kind != KindInt {
		return DT.NullValue(), nil
	}
	return DT.NewIntValue(a.I64 & b.I64), nil
}

func bitorValue(a, b Value) (Value, error) {
	if a.Kind != KindInt || b.Kind != KindInt {
		return DT.NullValue(), nil
	}
	return DT.NewIntValue(a.I64 | b.I64), nil
}

func bitxorValue(a, b Value) (Value, error) {
	if a.Kind != KindInt || b.Kind != KindInt {
		return DT.NullValue(), nil
	}
	return DT.NewIntValue(a.I64 ^ b.I64), nil
}

func lshiftValue(a, b Value) (Value, error) {
	if a.Kind != KindInt || b.Kind != KindInt {
		return DT.NullValue(), nil
	}
	if b.I64 < 0 || b.I64 > 63 {
		return DT.NullValue(), nil
	}
	return DT.NewIntValue(a.I64 << b.I64), nil
}

func rshiftValue(a, b Value) (Value, error) {
	if a.Kind != KindInt || b.Kind != KindInt {
		return DT.NullValue(), nil
	}
	if b.I64 < 0 || b.I64 > 63 {
		return DT.NullValue(), nil
	}
	return DT.NewIntValue(a.I64 >> b.I64), nil
}

func ConcatValue(a, b Value) (Value, error) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue(), nil
	}
	if a.Kind == KindText && b.Kind == KindText {
		return DT.NewTextValue(a.S + b.S), nil
	}
	return DT.NewTextValue(a.String() + b.String()), nil
}

func likeValue(a, b Value, escape string) (Value, error) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue(), nil
	}
	if a.Kind != KindText || b.Kind != KindText {
		return DT.NewBoolValue(false), nil
	}
	return DT.NewBoolValue(MatchLike(b.S, a.S, escape)), nil
}

func GlobValue(a, b Value) (Value, error) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NullValue(), nil
	}
	if a.Kind != KindText {
		return DT.NullValue(), fmt.Errorf("ex: GLOB pattern must be string, got Kind %d", a.Kind)
	}
	if b.Kind != KindText {
		return DT.NullValue(), fmt.Errorf("ex: GLOB operand must be string, got Kind %d", b.Kind)
	}
	return DT.NewBoolValue(globMatch(a.S, b.S)), nil
}

func isValue(a, b Value) (Value, error) {
	if a.Kind == KindNull && b.Kind == KindNull {
		return DT.NewBoolValue(true), nil
	}
	if a.Kind == KindNull || b.Kind == KindNull {
		return DT.NewBoolValue(false), nil
	}
	if a.Kind != b.Kind {
		return DT.NewBoolValue(false), nil
	}
	switch a.Kind {
	case KindInt:
		return DT.NewBoolValue(a.I64 == b.I64), nil
	case KindFloat:
		return DT.NewBoolValue(a.F64 == b.F64), nil
	case KindText:
		return DT.NewBoolValue(a.S == b.S), nil
	case KindBool:
		return DT.NewBoolValue(a.Bo == b.Bo), nil
	case KindBlob:
		if len(a.B) != len(b.B) {
			return DT.NewBoolValue(false), nil
		}
		for i := range a.B {
			if a.B[i] != b.B[i] {
				return DT.NewBoolValue(false), nil
			}
		}
		return DT.NewBoolValue(true), nil
	}
	return DT.NewBoolValue(false), nil
}

func numericFloat(v any) (float64, bool) {
	if val, ok := v.(Value); ok {
		v = val.ToAny()
	}
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

func MatchLike(pattern, s, escape string) bool {
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

func equalValue(a, b any) bool {
	if av, ok := a.(Value); ok {
		a = av.ToAny()
	}
	if bv, ok := b.(Value); ok {
		b = bv.ToAny()
	}
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
	pattern, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if pattern.Kind == KindNull {
		return nil, nil
	}
	str, err := evalFallbackEvalValue(args[1], row, params)
	if err != nil {
		return nil, err
	}
	if str.Kind == KindNull {
		return nil, nil
	}
	if pattern.Kind != KindText {
		return nil, nil
	}
	if str.Kind != KindText {
		return nil, nil
	}
	if globMatch(pattern.S, str.S) {
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
	return evalFallbackEvalValue(args[0], row, params)
}

// evalLikely implements likely(X) — no-op pass-through.
// REQ000396.
func evalLikely(args []PS.Expr, row *Row, params []any) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	return evalFallbackEvalValue(args[0], row, params)
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
	val, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val.Kind == KindNull {
		return nil, nil
	}
	if val.Kind != KindText {
		return "?000", nil
	}
	s := val.S
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
	val, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val.Kind == KindNull {
		return nil, nil
	}
	if val.Kind != KindText {
		return nil, nil
	}
	s := val.S

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
	val, err := evalFallbackEvalValue(args[0], row, params)
	if err != nil {
		return nil, err
	}
	if val.Kind == KindNull {
		return nil, nil
	}
	if val.Kind != KindText {
		return nil, nil
	}
	s := val.S

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
	return evalFallbackEvalValue(args[0], row, params)
}

// Local comparison helpers.
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
