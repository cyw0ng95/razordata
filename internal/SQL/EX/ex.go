package EX

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	LX "github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// sessionCountersProvider is an optional callback set by SYS/SE to
// provide per-session counter accessors for changes(), last_insert_rowid(),
// and total_changes() eval functions. This avoids an import cycle between
// EX and SE packages. REQ000385/394/411.
type SessionCounterAccessor interface {
	GetChangesCount(sessionID uint64) int64
	GetLastInsertRowID(sessionID uint64) int64
	GetTotalChangesCount(sessionID uint64) int64
}

var (
	sessionCounterMu       sync.RWMutex
	sessionCounterAccessor SessionCounterAccessor
)

// currentSessionID is the package-level current session ID for evalFunction.
// It's stored atomically to avoid races with concurrent sessions.
var currentSessionID atomic.Uint64

// SetSessionCounterAccessor sets the callback for reading per-session
// counters. Called once during SYS initialization.
func SetSessionCounterAccessor(acc SessionCounterAccessor) {
	sessionCounterMu.Lock()
	defer sessionCounterMu.Unlock()
	sessionCounterAccessor = acc
}

// getSessionCounterAccessor returns the current accessor (may be nil).
func getSessionCounterAccessor() SessionCounterAccessor {
	sessionCounterMu.RLock()
	defer sessionCounterMu.RUnlock()
	return sessionCounterAccessor
}


var ErrNotImplemented = errors.New("ex: not implemented")
var ErrNoRows = errors.New("ex: no rows")
var ErrClosed = errors.New("ex: operator closed")

type Operator interface {
	Next(ctx context.Context) (Row, error)
	Close() error
}

type Row struct {
	Cols  []string
	Types []int
	Data  []interface{}
	Outer *Row
	// planner is set by the executor when materializing a row
	// from the main plan. Subquery eval functions read it to
	// plan their nested queries with the same store, catalog,
	// and stats catalog. See REQ000366.
	planner *Planner
}

// Planner returns the planner associated with this row (or any
// of its outer parents). Returns nil if no planner was threaded
// through. REQ000366.
func (r *Row) Planner() *Planner {
	for cur := r; cur != nil; cur = cur.Outer {
		if cur.planner != nil {
			return cur.planner
		}
	}
	return nil
}

func (r *Row) Lookup(name string) (interface{}, bool) {
	for cur := r; cur != nil; cur = cur.Outer {
		for i, c := range cur.Cols {
			if c == name {
				if i < len(cur.Data) {
					return cur.Data[i], true
				}
				return nil, false
			}
		}
	}
	return nil, false
}

type Result struct {
	RowsAffected int64
	LastInsertID uint64
}

type Rows struct {
	Cols  []string
	Types []int
}

type ColInfo struct {
	Name     string
	Typ      int
	Nullable bool    // default true; false means NOT NULL
	Default  PS.Expr // nil means no DEFAULT clause
	PK       bool    // true means primary key (implies NOT NULL)
}

type Executor struct {
	planner    *Planner
	store      Store
	txWriter   TxWriter
	snapshotTS uint64 // REQ000255: per-statement snapshot timestamp for read-committed
	sessionID  uint64 // REQ000385/394/411: current session ID for counter access
}

// TxWriter is the optional hook an Executor notifies on every key
// write. Implementations record the pre-write value so ROLLBACK can
// restore. The SYS layer wires this for transactional sessions.
type TxWriter interface {
	RecordWrite(key []byte, newValue []byte)
}

// SetTxWriter installs w as the current transaction's write hook. Pass
// nil to disable. Not safe to call concurrently with Exec; the
// caller (a Session) is responsible for serialization.
func (e *Executor) SetTxWriter(w TxWriter) { e.txWriter = w }

// ClearTxWriter resets the write hook to nil. Pair with SetTxWriter.
func (e *Executor) ClearTxWriter() { e.txWriter = nil }

// SetSnapshot sets the per-statement snapshot timestamp for read-committed
// isolation (REQ000255). When non-zero, reads filter to versions visible at
// this timestamp. Pass 0 to disable snapshot filtering.
func (e *Executor) SetSnapshot(ts uint64) { e.snapshotTS = ts }

// GetSnapshot returns the current snapshot timestamp.
func (e *Executor) GetSnapshot() uint64 { return e.snapshotTS }

// SetSessionID sets the current session ID for counter access.
// REQ000385/394/411. Uses atomic store for the global currentSessionID
// to avoid races with concurrent sessions sharing one Executor.
func (e *Executor) SetSessionID(id uint64) {
	// Don't write e.sessionID - the Executor is shared across sessions.
	// Only update the atomic global that eval functions read.
	currentSessionID.Store(id)
}

// GetSessionID returns the current session ID.
func (e *Executor) GetSessionID() uint64 { return e.sessionID }

// getCurrentSessionID returns the package-level session ID for eval.
func getCurrentSessionID() uint64 {
	return currentSessionID.Load()
}

func NewExecutor() *Executor {
	return &Executor{planner: NewPlanner()}
}

func NewExecutorWithPlanner(pl *Planner) *Executor {
	return &Executor{planner: pl}
}

// NewExecutorWithEngine wires the executor to a real storage engine. When
// store is non-nil, SeqScan / Insert / Update / Delete route through it
// instead of the in-memory tables map. Pass nil to revert to in-memory
// mode.
func NewExecutorWithEngine(store Store) *Executor {
	return &Executor{planner: NewPlannerWithStore(store), store: store}
}

// ExtractParamTypes parses sql and returns the SQL column type
// of each `?` placeholder in left-to-right order. Entries are
// ls.CTInt / ls.CTVarchar / etc. when the placeholder can be
// resolved to a known column; -1 otherwise. This is the ST
// layer's source of truth for per-placeholder Go-type
// validation (R16-3, R16-4).
func (e *Executor) ExtractParamTypes(sql string) []int {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil
	}
	out := []int{}
	walkPlaceholderTypes(stmt, &out)
	return out
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
//
// The walker must NOT double-count a Param: each placeholder
// is appended exactly once even when it appears in a
// comparison that is itself walked recursively.
func walkExprTypes(table string, expr PS.Expr, out *[]int) {
	switch e := expr.(type) {
	case *PS.Param:
		// The BinaryExpr handler matched this Param against its
		// peer column and already appended the column's type.
		// Do not double-append.
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
	for _, ss := range storeSchemas {
		for i, c := range ss.cols {
			if c == name && i < len(ss.colTypes) {
				return lxTokenToColumnType(ss.colTypes[i])
			}
		}
	}
	// Fall back: walk the legacy schemas map and best-effort
	// match by name. We treat any col with a Type==0 (the
	// pre-iter-16 default) as TEXT so downstream coercibility
	// checks still produce a meaningful verdict.
	for _, cols := range schemas {
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
func lxTokenToColumnType(tok int) int {
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

func (e *Executor) RegisterTable(name string, schema []string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, "")
	RegisterTableSchema(name, schema)
	if e.store != nil {
		// REQ000367: API-level registration without a PK
		// also enables hidden-PK mode so the table can be
		// written to the engine store.
		if id := registerStoreSchema(name, schema, ""); id != 0 {
			storeMu.Lock()
			if ss, ok := storeSchemas[id]; ok {
				ss.hiddenPK = true
			}
			storeMu.Unlock()
		}
	}
}

func (e *Executor) RegisterTableWithPK(name string, schema []string, pk string) {
	cols := make([]ColInfo, len(schema))
	for i, n := range schema {
		cols[i] = ColInfo{Name: n, Typ: 1}
	}
	e.planner.RegisterTable(name, cols, pk)
	RegisterTableSchema(name, schema)
	if e.store != nil {
		registerStoreSchema(name, schema, pk)
	}
}

func (e *Executor) RegisterIndex(table, index string, cols []string) {
	e.planner.RegisterIndex(table, index, cols)
}

func (e *Executor) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return Result{}, err
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		// Use Query path for RETURNING
		_, err := e.Query(ctx, sql, args...)
		if err != nil {
			return Result{}, err
		}
		return Result{RowsAffected: 1}, nil
	}

	op, err := e.buildWriterOp(stmt)
	if err != nil {
		return Result{}, err
	}
	// R16-1: thread args down to the operator tree so `?`
	// placeholders resolve. Writers (INSERT/UPDATE/DELETE) also
	// support placeholders (e.g. INSERT ... VALUES (?,?)).
	propagateParams(op, args)
	defer op.Close()
	if _, err := op.Next(ctx); err != nil && err != ErrNoRows {
		return Result{}, err
	}
	return extractResult(op)
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

func (e *Executor) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		propagateParams(op, args)
		defer op.Close()
		// Collect all RETURNING rows
		var out []Row
		for {
			row, err := op.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				return nil, err
			}
			out = append(out, row)
		}
		if len(out) == 0 {
			return &Rows{}, nil
		}
		return &Rows{Cols: append([]string(nil), out[0].Cols...), Types: append([]int(nil), out[0].Types...)}, nil
	}

	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	// R16-1: thread args down to the operator tree so `?`
	// placeholders resolve during Eval.
	propagateParams(plan.root, args)
	defer plan.root.Close()
	row, err := plan.root.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			return &Rows{}, nil
		}
		return nil, err
	}
	rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]int(nil), row.Types...)}
	return rs, nil
}

func (e *Executor) QueryAll(ctx context.Context, sql string, args ...any) ([]Row, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	propagateParams(plan.root, args)
	// REQ000366: thread the main-plan planner so SeqScan rows
	// carry it into subquery evals. propagatePlanner is a
	// depth-first walk that calls WithPlanner on every node
	// that supports it.
	propagatePlanner(plan.root, e.planner)
	// Also expose the planner to subqueries that have no
	// outer row (e.g. top-level `SELECT EXISTS(...)`).
	currentSubqueryPlanner = e.planner
	defer func() { currentSubqueryPlanner = nil }()
	defer plan.root.Close()
	var out []Row
	for {
		row, err := plan.root.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// propagatePlanner walks the operator tree rooted at root and
// calls WithPlanner(p) on every node that supports it. See
// REQ000366.
func propagatePlanner(root Operator, p *Planner) {
	if root == nil {
		return
	}
	if w, ok := root.(interface{ WithPlanner(*Planner) Operator }); ok {
		w.WithPlanner(p)
	}
	type childer interface {
		Child() Operator
	}
	if c, ok := root.(childer); ok {
		propagatePlanner(c.Child(), p)
	}
}

// propagateParams walks the operator tree rooted at root and
// calls WithParams(args) on every node that supports it
// (R16-1..2). The walk is depth-first, children-first so the
// args reach every leaf operator. Operators without a
// WithParams method are skipped silently.
func propagateParams(root Operator, args []any) {
	if root == nil {
		return
	}
	if args == nil {
		return
	}
	p := asAnySlice(args)
	if w, ok := root.(interface{ WithParams([]interface{}) Operator }); ok {
		w.WithParams(p)
	}
	// Walk children via the Child() convention used elsewhere
	// in this package (explain.go).
	type childer interface {
		Child() Operator
	}
	if c, ok := root.(childer); ok {
		propagateParams(c.Child(), args)
	}
	// Some operators expose children via a `child` field; we
	// rely on the explain.go walk for those via Child(). Operators
	// with multiple children (HashAggregate, Join) define
	// their own WithParams and walk internally.
}

// asAnySlice converts []any to []interface{} for type-stability
// across the WithParams interface boundary. Avoids an allocation
// when the slice is already nil.
func asAnySlice(args []any) []interface{} {
	if args == nil {
		return nil
	}
	out := make([]interface{}, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}

// Explain plans the statement and returns a human-readable
// description of the operator tree. The plan is closed before
// returning, so Explain does not run the query.
func (e *Executor) Explain(sql string) (string, error) {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return "", err
	}
	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return "", err
	}
	if plan == nil || plan.root == nil {
		return "", errors.New("ex: plan produced no root")
	}
	defer plan.root.Close()
	return explainOperator(plan.root, 0), nil
}

func (e *Executor) buildWriterOp(stmt PS.Stmt) (Operator, error) {
	switch s := stmt.(type) {
	case *PS.Insert:
		if e.store != nil {
			op, err := NewInsertWithStore(e.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict), nil
	case *PS.Update:
		var scan Operator = NewSeqScan(s.Table)
		if e.store != nil {
			ssc, err := NewSeqScanWithStore(e.store, s.Table)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := NewFilter(scan, s.Where)
		if e.store != nil {
			op, err := NewUpdateWithStore(e.store, s.Table, s.Set, s.Where, filter, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewUpdate(s.Table, s.Set, s.Where, filter, s.Returning), nil
	case *PS.Delete:
		var scan Operator = NewSeqScan(s.Table)
		if e.store != nil {
			ssc, err := NewSeqScanWithStore(e.store, s.Table)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := NewFilter(scan, s.Where)
		if e.store != nil {
			op, err := NewDeleteWithStore(e.store, s.Table, s.Where, filter, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewDelete(s.Table, s.Where, filter, s.Returning), nil
	case *PS.CreateTable:
		return NewCreateTable(s), nil
	case *PS.DropTable:
		return NewDropTable(s), nil
	case *PS.CreateIndexStmt:
		return NewCreateIndex(s), nil
	case *PS.DropIndexStmt:
		return NewDropIndex(s), nil
	case *PS.CreateViewStmt:
		return NewCreateView(s), nil
	}
	return nil, errors.New("ex: not a writable statement")
}

func extractResult(op Operator) (Result, error) {
	type affected interface {
		RowsAffected() int64
	}
	if a, ok := op.(affected); ok {
		return Result{RowsAffected: a.RowsAffected()}, nil
	}
	return Result{}, nil
}
