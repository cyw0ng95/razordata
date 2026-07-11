package EX

import (
	"errors"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQB/AD"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// NewIntValue creates a Value from an int64.
func NewIntValue(v int64) DT.Value { return AP.NewIntValue(v) }

// NewFloatValue creates a Value from a float64.
func NewFloatValue(v float64) DT.Value { return AP.NewFloatValue(v) }

// NewTextValue creates a Value from a string.
func NewTextValue(v string) DT.Value { return AP.NewTextValue(v) }

// NewBlobValue creates a Value from a byte slice.
func NewBlobValue(v []byte) DT.Value { return AP.NewBlobValue(v) }

// NewBoolValue creates a Value from a bool.
func NewBoolValue(v bool) DT.Value { return AP.NewBoolValue(v) }

// NullValue returns a NULL Value.
func NullValue() DT.Value { return AP.NullValue() }

// valueToString converts a Value to its string representation without
// going through fmt.Sprint (no reflection, no boxing). REQ001015.
// REQ001066: delegates to Value.String() for the per-kind switch.
func valueToString(v DT.Value) string {
	return v.String()
}

// valueFromAny creates a Value from a boxed any. Inverse of ToAny.
func valueFromAny(a any) DT.Value {
	if a == nil {
		return NullValue()
	}
	if v, ok := a.(DT.Value); ok {
		return v
	}
	switch x := a.(type) {
	case int64:
		return NewIntValue(x)
	case float64:
		return NewFloatValue(x)
	case string:
		return NewTextValue(x)
	case bool:
		return NewBoolValue(x)
	case int:
		return NewIntValue(int64(x))
	case []byte:
		return NewBlobValue(x)
	default:
		return NewTextValue(fmt.Sprint(x))
	}
}

// valueFromAnySlice converts a []any to []DT.Value.
func valueFromAnySlice(a []any) []DT.Value {
	if a == nil {
		return nil
	}
	out := make([]DT.Value, len(a))
	for i, v := range a {
		out[i] = valueFromAny(v)
	}
	return out
}

// valueSliceToAny converts a []DT.Value to []any.
func valueSliceToAny(v []DT.Value) []any {
	if v == nil {
		return nil
	}
	out := make([]any, len(v))
	for i, val := range v {
		out[i] = val.ToAny()
	}
	return out
}

// getCachedStmt looks up a cached parsed statement. Returns nil if not found.
// REQ001286: allocation-free hot path — one map lookup + counter increment.
func (e *Executor) getCachedStmt(sql string) PS.Stmt {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	ent, ok := e.stmtCache.entries[sql]
	if !ok {
		return nil
	}
	e.stmtCache.accessCounter++
	ent.lastAccess = e.stmtCache.accessCounter
	return ent.stmt
}

// putCachedStmt stores a parsed statement in the cache.
func (e *Executor) putCachedStmt(sql string, stmt PS.Stmt) {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	if ent, ok := e.stmtCache.entries[sql]; ok {
		ent.stmt = stmt
		e.stmtCache.accessCounter++
		ent.lastAccess = e.stmtCache.accessCounter
		return
	}
	e.stmtCache.accessCounter++
	ent := &stmtCacheEntry{stmt: stmt, lastAccess: e.stmtCache.accessCounter}
	e.stmtCache.entries[sql] = ent
	if len(e.stmtCache.entries) > e.stmtCache.maxSize {
		var oldestKey string
		var oldestAccess uint64 = ^uint64(0)
		for k, v := range e.stmtCache.entries {
			if v.lastAccess < oldestAccess {
				oldestAccess = v.lastAccess
				oldestKey = k
			}
		}
		delete(e.stmtCache.entries, oldestKey)
	}
}

// clearStmtCache clears the statement cache. Used in tests.
func (e *Executor) clearStmtCache() {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	e.stmtCache.entries = make(map[string]*stmtCacheEntry, 1024)
	e.stmtCache.accessCounter = 0
}

// getCachedPlan looks up a cached compiled plan by memo key.
// Returns nil if not found. REQ001011.
func (e *Executor) getCachedPlan(key string) *pl.PlanResult {
	e.planCache.mu.Lock()
	defer e.planCache.mu.Unlock()
	ent, ok := e.planCache.entries[key]
	if !ok {
		return nil
	}
	// Move to front of LRU
	for i, entry := range e.planCache.lru {
		if entry == ent {
			e.planCache.lru = append(e.planCache.lru[:i], e.planCache.lru[i+1:]...)
			break
		}
	}
	e.planCache.lru = append([]*planCacheEntry{ent}, e.planCache.lru...)
	return ent.result
}

// putCachedPlan stores a compiled plan in the cache.
// REQ001011.
func (e *Executor) putCachedPlan(key string, result *pl.PlanResult) {
	e.planCache.mu.Lock()
	defer e.planCache.mu.Unlock()
	if ent, ok := e.planCache.entries[key]; ok {
		for i, entry := range e.planCache.lru {
			if entry == ent {
				e.planCache.lru = append(e.planCache.lru[:i], e.planCache.lru[i+1:]...)
				break
			}
		}
		e.planCache.lru = append([]*planCacheEntry{ent}, e.planCache.lru...)
		return
	}
	ent := &planCacheEntry{result: result}
	e.planCache.entries[key] = ent
	e.planCache.lru = append([]*planCacheEntry{ent}, e.planCache.lru...)
	for len(e.planCache.lru) > e.planCache.maxSize {
		oldest := e.planCache.lru[len(e.planCache.lru)-1]
		e.planCache.lru = e.planCache.lru[:len(e.planCache.lru)-1]
		for key, val := range e.planCache.entries {
			if val == oldest {
				delete(e.planCache.entries, key)
				break
			}
		}
	}
}

// clearPlanCache clears the plan cache. Used in tests.
func (e *Executor) clearPlanCache() {
	e.planCache.mu.Lock()
	defer e.planCache.mu.Unlock()
	e.planCache.entries = nil
	e.planCache.lru = nil
}

// replaceLiteralsOnTree walks the operator tree and calls
// ReplaceLiterals on every node with comparison-literals (Filter).
// Params are consumed in DFS tree-walk order, matching the
// extraction order of NormalizeForMemo. REQ001195.
func replaceLiteralsOnTree(root DT.Operator, vals []any) {
	type filterNode interface {
		CountComparisonLiterals() int
		ReplaceLiterals([]any)
	}
	var walk func(DT.Operator, *[]any)
	walk = func(op DT.Operator, remaining *[]any) {
		if op == nil || len(*remaining) == 0 {
			return
		}
		if fn, ok := op.(filterNode); ok {
			n := fn.CountComparisonLiterals()
			if n > 0 && n <= len(*remaining) {
				fn.ReplaceLiterals((*remaining)[:n])
				*remaining = (*remaining)[n:]
			}
		}
		type childer interface{ Child() DT.Operator }
		if c, ok := op.(childer); ok {
			walk(c.Child(), remaining)
		}
		type leftRighter interface {
			LeftChild() DT.Operator
			RightChild() DT.Operator
		}
		if lr, ok := op.(leftRighter); ok {
			walk(lr.LeftChild(), remaining)
			walk(lr.RightChild(), remaining)
		}
	}
	walk(root, &vals)
}

// hasReturning reports whether the statement has a RETURNING clause.
func hasReturning(stmt PS.Stmt) bool {
	switch s := stmt.(type) {
	case *PS.Insert:
		return len(s.Returning) > 0
	case *PS.Update:
		return len(s.Returning) > 0
	case *PS.Delete:
		return len(s.Returning) > 0
	}
	return false
}

// isUpdatableView checks if a view is updatable (simple single-table
// select without aggregation, DISTINCT, GROUP BY, HAVING, ORDER BY,
// LIMIT, or subqueries). REQ001061.
func isUpdatableView(sel *PS.Select) bool {
	if sel == nil {
		return false
	}
	// Must be a simple single-table select
	if sel.From == "" {
		return false
	}
	// No joins (compound views are not updatable)
	if len(sel.Joins) > 0 {
		return false
	}
	// No aggregation
	if sel.Having != nil {
		return false
	}
	// No GROUP BY
	if len(sel.GroupBy) > 0 {
		return false
	}
	// No DISTINCT
	if sel.Distinct {
		return false
	}
	// No ORDER BY
	if len(sel.OrderBy) > 0 {
		return false
	}
	// No LIMIT
	if sel.Limit != nil {
		return false
	}
	// No OFFSET
	if sel.Offset != nil {
		return false
	}
	// No subquery in FROM
	if sel.SubqueryFrom != nil {
		return false
	}
	// No aggregation functions in SELECT list
	for _, col := range sel.Cols {
		if col == nil {
			continue
		}
		if hasAggFunc(col) {
			return false
		}
	}
	return true
}

// hasAggFunc checks if an expression contains an aggregate function.
func hasAggFunc(expr PS.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *PS.AggregateFunc:
		return true
	case *PS.FunctionCall:
		switch e.Name {
		// These are also aggregate functions in some contexts
		case "count", "sum", "avg", "min", "max", "group_concat":
			return true
		}
	case *PS.BinaryExpr:
		return hasAggFunc(e.Left) || hasAggFunc(e.Right)
	case *PS.UnaryExpr:
		return hasAggFunc(e.Operand)
	case *PS.CaseExpr:
		for _, when := range e.WhenList {
			if hasAggFunc(when.Cond) || hasAggFunc(when.Then) {
				return true
			}
		}
		if hasAggFunc(e.Else) {
			return true
		}
	case *PS.CastExpr:
		return hasAggFunc(e.Expr)
	case *PS.StarExpr, *PS.SubqueryExpr, *PS.Param:
		return false
	case *PS.Ident, *PS.QualifiedName:
		return false
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral, *PS.NullLiteral:
		return false
	default:
		return false
	}
	return false
}

// walkPlaceholderTypes walks stmt and appends a column-type
// entry for each *PS.Param encountered. For `column = ?`
// comparisons, the column's SQL type is recorded; other
// placeholders get -1 (skip validation in coerce).
func walkPlaceholderTypes(stmt PS.Stmt, out *[]int) {
	switch s := stmt.(type) {
	case *PS.Select:
		for _, e := range s.Cols {
			walkExprTypes("", e, out)
		}
		if s.Where != nil {
			walkExprTypes("", s.Where, out)
		}
		for _, g := range s.GroupBy {
			walkExprTypes("", g, out)
		}
		if s.Having != nil {
			walkExprTypes("", s.Having, out)
		}
		for _, o := range s.OrderBy {
			walkExprTypes("", o.Expr, out)
		}
		if s.Limit != nil {
			walkExprTypes("", s.Limit, out)
		}
	case *PS.Insert:
		// INSERT values are evaluated as expressions; the column
		// type comes from the target table schema, not from a
		// peer column. Walk them with empty scope.
		for _, row := range s.Values {
			for _, e := range row {
				walkExprTypes("", e, out)
			}
		}
	case *PS.Update:
		for _, p := range s.Set {
			walkExprTypes("", p.Val, out)
		}
		if s.Where != nil {
			walkExprTypes("", s.Where, out)
		}
	case *PS.Delete:
		if s.Where != nil {
			walkExprTypes("", s.Where, out)
		}
	}
}

// walkExprTypes walks expr, appending an entry to out for each
// *PS.Param it finds. If the placeholder is part of a
// `column = ?` (or `? = column`) binary comparison, we record
// the column's SQL type. Otherwise the entry is -1 (skip).
// The walker must NOT double-count a Param: each placeholder
// is appended exactly once even when it appears in a
// comparison that is itself walked recursively.
func walkExprTypes(table string, expr PS.Expr, out *[]int) {
	switch e := expr.(type) {
	case *PS.Param:
		// Standalone placeholder (e.g. INSERT VALUES(?, ?)).
		// No column context to resolve, so mark as unknown (-1).
		*out = append(*out, -1)
		return
	case *PS.BinaryExpr:
		// If one side is an Ident and the other is a Param,
		// the Param is matched: append the column's type
		// and skip the recursive walk to avoid double-counting.
		if col, ok := e.Left.(*PS.Ident); ok {
			if _, isParam := e.Right.(*PS.Param); isParam {
				*out = append(*out, columnTypeFor(col.Name))
				return
			}
		}
		if col, ok := e.Right.(*PS.Ident); ok {
			if _, isParam := e.Left.(*PS.Param); isParam {
				*out = append(*out, columnTypeFor(col.Name))
				return
			}
		}
		// No column-vs-param match; walk both sides to surface
		// any nested placeholders (e.g. `a + ?` against an
		// expression operand).
		walkExprTypes(table, e.Left, out)
		walkExprTypes(table, e.Right, out)
	case *PS.UnaryExpr:
		walkExprTypes(table, e.Operand, out)
	case *PS.BetweenExpr:
		walkExprTypes(table, e.Expr, out)
		walkExprTypes(table, e.Low, out)
		walkExprTypes(table, e.High, out)
	case *PS.InExpr:
		walkExprTypes(table, e.Expr, out)
		for _, item := range e.List {
			walkExprTypes(table, item, out)
		}
	case *PS.CaseExpr:
		for _, w := range e.WhenList {
			walkExprTypes(table, w.Cond, out)
			walkExprTypes(table, w.Then, out)
		}
		if e.Else != nil {
			walkExprTypes(table, e.Else, out)
		}
	case *PS.FunctionCall:
		for _, a := range e.Args {
			walkExprTypes(table, a, out)
		}
	case *PS.AggregateFunc:
		if e.Arg != nil {
			walkExprTypes(table, e.Arg, out)
		}
	case *PS.CastExpr:
		walkExprTypes(table, e.Expr, out)
	case *PS.AliasedExpr:
		walkExprTypes(table, e.Expr, out)
	}
}

// columnTypeFor returns the LS ColumnType for `name` if the
// planner has a registered table with that column. Returns -1
// otherwise (the caller skips validation for that slot).
func columnTypeFor(name string) int {
	for _, ss := range DT.StoreSchemas {
		for i, c := range ss.Cols {
			if c == name && i < len(ss.ColTypes) {
				return lxTokenToColumnType(ss.ColTypes[i])
			}
		}
	}
	// Fall back: walk the legacy DT.Schemas map and best-effort
	// match by name. We treat any col with a Type==0 (the
	// pre-iter-16 default) as TEXT so downstream coercibility
	// checks still produce a meaningful verdict.
	for _, cols := range DT.Schemas {
		for _, c := range cols {
			if c == name {
				return int(ls.CTText)
			}
		}
	}
	return -1
}

// lxTokenToColumnType converts an LX token (T_INT_KW/T_TEXT/...)
// into the corresponding LS ColumnType. Returns -1 for
// unrecognized tokens.
func lxTokenToColumnType(tok LX.TokenType) int {
	switch LX.TokenType(tok) {
	case LX.T_INT_KW:
		return int(ls.CTInt)
	case LX.T_BIGINT:
		return int(ls.CTBigInt)
	case LX.T_FLOAT_KW:
		return int(ls.CTFloat)
	case LX.T_BOOL:
		return int(ls.CTBool)
	case LX.T_TEXT:
		return int(ls.CTText)
	case LX.T_VARCHAR:
		return int(ls.CTVarchar)
	case LX.T_BLOB:
		return int(ls.CTBlob)
	case LX.T_TIMESTAMP:
		return int(ls.CTTimestamp)
	}
	return -1
}

func (e *Executor) buildWriterOp(stmt PS.Stmt) (DT.Operator, error) {
	switch s := stmt.(type) {
	case *PS.Insert:
		// REQ001191: DML on views is not allowed.
		if DT.LookupView(s.Table) != nil {
			return nil, fmt.Errorf("ex: cannot modify view %s", s.Table)
		}
		// REQ000707: INSERT INTO t SELECT ...
		if s.Select != nil {
			selPlan, err := e.planner.Plan(s.Select)
			if err != nil {
				return nil, err
			}
			if selPlan == nil || selPlan.Root == nil {
				return nil, fmt.Errorf("ex: INSERT SELECT: plan produced no root")
			}
			// REQ001129: store-backed INSERT...SELECT needs a
			// store-backed Insert operator so the rows are written
			// to the engine, not just the in-memory table map.
			var op *WT.Insert
			if e.store != nil {
				op, err = WT.NewInsertWithStore(e.store, s.Table, s.Cols, nil, s.Returning, s.OnConflict)
				if err != nil {
					return nil, err
				}
			} else {
				op = WT.NewInsert(s.Table, s.Cols, nil, s.Returning, s.OnConflict)
			}
			op.SetSelectPlan(selPlan.Root)
			op.SetConflictAction(s.ConflictAction)
			propagatePlanner(selPlan.Root, e.planner)
			return op, nil
		}

		op, iErr := func() (*WT.Insert, error) {
			if e.store != nil {
				return WT.NewInsertWithStore(e.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
			}
			return WT.NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict), nil
		}()
		if iErr != nil {
			return nil, iErr
		}
		op.SetConflictAction(s.ConflictAction)
		op.SetDefaultValues(s.DefaultValues)
		return op, nil
	case *PS.Update:
		targetTable := s.Table
		if viewSel := DT.LookupView(targetTable); viewSel != nil {
			// REQ001191: DML on views is not allowed (views are
			// read-only in SQLite unless they have INSTEAD OF triggers,
			// which we don't support yet).
			return nil, fmt.Errorf("ex: cannot modify view %s", targetTable)
		}
		var scan DT.Operator = OP.NewSeqScan(targetTable)
		if e.store != nil {
			ssc, err := OP.NewSeqScanWithStore(e.store, targetTable)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := OP.NewFilter(scan, s.Where, nil)
		// REQ000558: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before updating.
		var current DT.Operator = filter
		if len(s.OrderBy) > 0 {
			so := OP.NewSort(current, s.OrderBy)
			if e.planner.sortBufferSize > 0 {
				so.WithSortBufferSize(e.planner.sortBufferSize)
			}
			current = so
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := WT.NewUpdateWithStore(e.store, targetTable, s.Set, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return WT.NewUpdate(targetTable, s.Set, s.Where, current, s.Returning), nil
	case *PS.Delete:
		tableName := s.Table
		if viewSel := DT.LookupView(tableName); viewSel != nil {
			// REQ001191: DML on views is not allowed (views are
			// read-only in SQLite unless they have INSTEAD OF triggers).
			return nil, fmt.Errorf("ex: cannot modify view %s", tableName)
		}
		var scan DT.Operator = OP.NewSeqScan(tableName)
		if e.store != nil {
			ssc, err := OP.NewSeqScanWithStore(e.store, tableName)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := OP.NewFilter(scan, s.Where, nil)
		// REQ000475: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before deleting.
		var current DT.Operator = filter
		if len(s.OrderBy) > 0 {
			so := OP.NewSort(current, s.OrderBy)
			if e.planner.sortBufferSize > 0 {
				so.WithSortBufferSize(e.planner.sortBufferSize)
			}
			current = so
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = OP.NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = OP.NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := WT.NewDeleteWithStore(e.store, tableName, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return WT.NewDelete(tableName, s.Where, current, s.Returning), nil
	case *PS.CreateTable:
		if s.Select != nil {
			// CREATE TABLE AS SELECT needs the planner to
			// build the inner SELECT plan. REQ000520.
			op, err := e.planner.Plan(s)
			if err != nil {
				return nil, err
			}
			if op == nil || op.Root == nil {
				return nil, errors.New("ex: plan produced no root for CTAS")
			}
			return op.Root, nil
		}
		return WT.NewCreateTable(s), nil
	case *PS.DropTable:
		return WT.NewDropTable(s), nil
	case *PS.CreateIndexStmt:
		// Note: the planner's cost-based selection (REQ000156)
		// is keyed off ex.RegisterIndex, not CREATE INDEX.
		// CREATE INDEX only registers the index for writer
		// maintenance; it does not backfill existing rows into
		// the index keyspace. Tests that want cost-based
		// selection should call ex.RegisterIndex explicitly.
		return WT.NewCreateIndex(s), nil
	case *PS.DropIndexStmt:
		return WT.NewDropIndex(s), nil
	case *PS.CreateViewStmt:
		return WT.NewCreateView(s), nil
	case *PS.CreateMatViewStmt:
		return WT.NewCreateMatView(s.Name, s.As, e.store), nil
	case *PS.DropMatViewStmt:
		return WT.NewDropMatView(s.Name, e.store), nil
	case *PS.RefreshMatViewStmt:
		// Lookup the matview definition from registry
		sel := DT.LookupMatView(s.Name)
		if sel == nil {
			return nil, fmt.Errorf("ex: materialized view %q not found", s.Name)
		}
		return WT.NewRefreshMatView(s.Name, sel, e.store, e.planner), nil
	case *PS.VacuumStmt:
		return UT.NewVacuum(s), nil
	case *PS.AnalyzeStmt:
		if e.store != nil {
			op, err := UT.NewAnalyzeWithStore(e.store, s)
			if err == nil {
				return op, nil
			}
		}
		return UT.NewAnalyze(s), nil
	case *PS.AlterTableStmt:
		return WT.NewAlterTable(s), nil
	case *PS.TriggerStmt:
		return WT.NewTrigger(s), nil
	case *PS.DropViewStmt:
		return WT.NewDropView(s), nil
	case *PS.DropTriggerStmt:
		return WT.NewDropTrigger(s), nil
	case *PS.PragmaStmt:
		return WT.NewPragma(s), nil
	case *PS.ExplainStmt:
		return WT.NewExplain(s), nil
	case *PS.TruncateStmt:
		return WT.NewTruncate(s), nil
	case *PS.ReindexStmt:
		return WT.NewReindex(s), nil
	case *PS.CreateVirtualTableStmt:
		return WT.NewUnsupportedOp(s, "ex: virtual table module not supported in v1: "+s.Module), nil
	case *PS.BeginTX:
		return AD.NewNoop(), nil
	case *PS.CommitTX:
		return AD.NewNoop(), nil
	case *PS.ValuesStmt:
		return OP.NewValuesRowsOp(s.Rows), nil
	case *PS.AttachStmt:
		path, err := extractAttachPath(s.Expr)
		if err != nil {
			return nil, err
		}
		return WT.NewAttachOp(e, s.Name, path), nil
	case *PS.DetachStmt:
		return WT.NewDetachOp(e, s.Name), nil
	}
	return nil, errors.New("ex: not a writable statement")
}

func extractResult(op DT.Operator) (Result, error) {
	type affected interface {
		RowsAffected() int64
	}
	if a, ok := op.(affected); ok {
		return Result{RowsAffected: a.RowsAffected()}, nil
	}
	return Result{}, nil
}

// extractAttachPath extracts the path string from an ATTACH expression.
// Expects a string literal. REQ000908.
func extractAttachPath(expr PS.Expr) (string, error) {
	s, ok := expr.(*PS.StringLiteral)
	if !ok {
		return "", errors.New("ex: ATTACH DATABASE path must be a string literal")
	}
	return s.Val, nil
}