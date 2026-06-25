package EX

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// sessionCountersProvider is an optional callback set by SYS/SE to
// provide per-session counter accessors for changes(), last_insert_rowid(),
// and total_changes() eval functions. This avoids an import cycle between
// EX and SE packages. REQ000385/394/411.
type SessionCounterAccessor interface {
	ChangesCount(sessionID uint64) int64
	LastInsertRowID(sessionID uint64) int64
	TotalChangesCount(sessionID uint64) int64
}

var (
	sessionCounterMu       sync.RWMutex
	sessionCounterAccessor SessionCounterAccessor
)

// currentSessionID is the package-level current session ID for evalFunction.
// It's stored atomically to avoid races with concurrent sessions.
var currentSessionID atomic.Uint64

// currentTxWriter is the package-level current TxWriter. Set by
// Executor.SetTxWriter and read by Insert/Update/Delete operators
// (both in-memory and store-backed) so that transactions can
// capture pre-write state for rollback. REQ000588.
var currentTxWriter atomic.Pointer[TxWriter]

// SetCurrentTxWriter stores w in the package-level slot.
func SetCurrentTxWriter(w TxWriter) {
	var boxed *TxWriter
	if w != nil {
		boxed = &w
	}
	currentTxWriter.Store(boxed)
}

// CurrentTxWriter returns the package-level current TxWriter.
func CurrentTxWriter() TxWriter {
	if p := currentTxWriter.Load(); p != nil {
		return *p
	}
	return nil
}

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

// Value kind constants for the tagged-union Value type (REQ000776).
const (
	KindNull ValueKind = iota
	KindInt
	KindFloat
	KindText
	KindBlob
	KindBool
)

// ValueKind is the type discriminator for Value.
type ValueKind uint8

// Value is a tagged-union that stores SQL values inline without boxing.
// The zero value (Kind=0, all fields zero) represents SQL NULL.
type Value struct {
	Kind ValueKind
	I64  int64
	F64  float64
	S    string
	B    []byte
	Bo   bool
}

// NewIntValue creates a Value from an int64.
func NewIntValue(v int64) Value { return Value{Kind: KindInt, I64: v} }

// NewFloatValue creates a Value from a float64.
func NewFloatValue(v float64) Value { return Value{Kind: KindFloat, F64: v} }

// NewTextValue creates a Value from a string.
func NewTextValue(v string) Value { return Value{Kind: KindText, S: v} }

// NewBlobValue creates a Value from a byte slice.
func NewBlobValue(v []byte) Value { return Value{Kind: KindBlob, B: v} }

// NewBoolValue creates a Value from a bool.
func NewBoolValue(v bool) Value { return Value{Kind: KindBool, Bo: v} }

// NullValue returns a NULL Value.
func NullValue() Value { return Value{Kind: KindNull} }

// IsNull returns true if this Value represents SQL NULL.
func (v Value) IsNull() bool { return v.Kind == KindNull }

// AsInt returns the int64 value (0 if not int).
func (v Value) AsInt() int64 { return v.I64 }

// AsFloat returns the float64 value (0 if not float).
func (v Value) AsFloat() float64 { return v.F64 }

// AsString returns the string value ("" if not text).
func (v Value) AsString() string { return v.S }

// AsBlob returns the []byte value (nil if not blob).
func (v Value) AsBlob() []byte { return v.B }

// AsBool returns the bool value (false if not bool).
func (v Value) AsBool() bool { return v.Bo }

// String returns a human-readable representation of the ValueKind.
func (k ValueKind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindInt:
		return "int64"
	case KindFloat:
		return "float64"
	case KindText:
		return "string"
	case KindBlob:
		return "blob"
	case KindBool:
		return "bool"
	default:
		return "unknown"
	}
}

// ToAny converts a Value to the boxed any representation.
// Used for backward compatibility during the migration.
func (v Value) ToAny() any {
	switch v.Kind {
	case KindNull:
		return nil
	case KindInt:
		return v.I64
	case KindFloat:
		return v.F64
	case KindText:
		return v.S
	case KindBlob:
		return v.B
	case KindBool:
		return v.Bo
	default:
		return nil
	}
}

// Equal compares two Values for equality. REQ000776 — supports
// all kinds including []byte (which is not directly comparable
// with ==). Two NULLs are equal. A NULL and non-NULL are not equal.
func (v Value) Equal(other Value) bool {
	if v.Kind != other.Kind {
		return false
	}
	switch v.Kind {
	case KindNull:
		return true
	case KindInt:
		return v.I64 == other.I64
	case KindFloat:
		return v.F64 == other.F64
	case KindText:
		return v.S == other.S
	case KindBlob:
		if len(v.B) != len(other.B) {
			return false
		}
		for i := range v.B {
			if v.B[i] != other.B[i] {
				return false
			}
		}
		return true
	case KindBool:
		return v.Bo == other.Bo
	}
	return false
}

// valueFromAny creates a Value from a boxed any. Inverse of ToAny.
func valueFromAny(a any) Value {
	if a == nil {
		return NullValue()
	}
	if v, ok := a.(Value); ok {
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

// valueFromAnySlice converts a []any to []Value.
func valueFromAnySlice(a []any) []Value {
	if a == nil {
		return nil
	}
	out := make([]Value, len(a))
	for i, v := range a {
		out[i] = valueFromAny(v)
	}
	return out
}

// valueSliceToAny converts a []Value to []any.
func valueSliceToAny(v []Value) []any {
	if v == nil {
		return nil
	}
	out := make([]any, len(v))
	for i, val := range v {
		out[i] = val.ToAny()
	}
	return out
}

// ValueSliceToAny is the exported version of valueSliceToAny for
// callers outside the EX package (e.g. SYS/AP bridging).
func ValueSliceToAny(v []Value) []any { return valueSliceToAny(v) }


type Operator interface {
	Next(ctx context.Context) (Row, error)
	Close() error
}

type Row struct {
	Cols  []string
	Types []int
	Data  []Value
	Outer *Row
	// planner is set by the executor when materializing a row
	// from the main plan. Subquery eval functions read it to
	// plan their nested queries with the same store, catalog,
	// and stats catalog. See REQ000366.
	planner *Planner
	// colIndex is a pre-built O(1) lookup from column name to
	// column index, built lazily on first Lookup call. REQ000544.
	colIndex map[string]int
	// storeKey holds the raw key from the LSM iterator when this
	// row was read from the engine store. Populated by SeqScan
	// and used by Update/Delete to preserve the original key.
	storeKey []byte
	// execCtx carries per-execution state (planner, session ID,
	// tx writer) through the operator tree. REQ000586.
	execCtx *ExecContext
	// tableName identifies which table this row was read from.
	// Set by SeqScan/IndexScan when producing rows so correlated
	// subquery eval can resolve QualifiedName references (e.g.
	// t.g) against the correct table. REQ000700.
	tableName string
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

func (r *Row) Lookup(name string) (any, bool) {
	// REQ000770: try fast path first (caller pre-lowered the name at parse time).
	// Fall back to ToLower for backward compatibility with programmatic callers.
	// REQ000816: also pre-check if name contains a dot to avoid the
	// dotted-name linear scan when not needed.
	lname := name
	hasUpper := false
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			lname = strings.ToLower(name)
			hasUpper = true
			break
		}
	}
	hasDot := false
	for _, c := range name {
		if c == '.' {
			hasDot = true
			break
		}
	}
	for cur := r; cur != nil; cur = cur.Outer {
		if cur.colIndex == nil {
			cur.buildColIndex()
		}
		if idx, ok := cur.colIndex[lname]; ok {
			if idx < len(cur.Data) {
				return cur.Data[idx].ToAny(), true
			}
			return nil, false
		}
		// REQ000816: skip the dotted-name fallback scan when the
		// caller-provided name has no dot — those calls cannot match
		// any table.col-style column.
		if !hasDot {
			continue
		}
		for j, c := range cur.Cols {
			if i := strings.LastIndexByte(c, '.'); i >= 0 && i < len(c)-1 {
				if !hasUpper {
					// Fast path: both name and Cols are lowercase.
					if c[i+1:] == name && j < len(cur.Data) {
						return cur.Data[j].ToAny(), true
					}
				} else if strings.EqualFold(c[i+1:], name) && j < len(cur.Data) {
					return cur.Data[j].ToAny(), true
				}
			}
		}
	}
	return nil, false
}

// buildColIndex builds the O(1) column name → index map. REQ000544.
// REQ000816: skip strings.ToLower when Cols are already lowercase
// (the common case — Cols from RegisterTable are stored lowercase).
func (r *Row) buildColIndex() {
	r.colIndex = make(map[string]int, len(r.Cols))
	allLower := true
	for _, c := range r.Cols {
		if c != "" && (c[0] < 'a' || c[0] > 'z') && c[0] != '_' && c[0] != '.' {
			// Quick check: if first char is uppercase, we need ToLower.
			for j := 0; j < len(c); j++ {
				if c[j] >= 'A' && c[j] <= 'Z' {
					allLower = false
					break
				}
			}
			if !allLower {
				break
			}
		}
	}
	for i, c := range r.Cols {
		if allLower {
			r.colIndex[c] = i
		} else {
			r.colIndex[strings.ToLower(c)] = i
		}
	}
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

// stmtCacheEntry holds a cached parsed statement with LRU metadata.
type stmtCacheEntry struct {
	stmt PS.Stmt
}

// Executor holds the core execution state.
type Executor struct {
	planner    *Planner
	store      Store
	txWriter   TxWriter
	snapshotTS uint64 // REQ000255: per-statement snapshot timestamp for read-committed
	sessionID  uint64 // REQ000385/394/411: current session ID for counter access
	// lastChanges tracks rows modified by the most recent DML statement.
	// Persisted across Exec/Query calls so CHANGES() reports the correct
	// value even after intervening non-DML statements (REQ000812).
	lastChanges int64
	// totalChanges tracks cumulative DML row count across all statements
	// in the session. Copied into/out of ExecContext for each Exec/Query
	// call so TOTAL_CHANGES() is correct across statements (REQ000812).
	totalChanges int64
	// txnDebugger tracks MVCC/transaction statistics for EXPLAIN ANALYZE.
	// REQ000792: MVCC debugging.
	txnDebugger *TxnDebugger
	// stmtCache caches parsed statements keyed by SQL text to avoid
	// re-parsing on repeated queries. LRU eviction, default 256 entries.
	stmtCache struct {
		mu      sync.Mutex
		entries map[string]*stmtCacheEntry
		lru     []*stmtCacheEntry
		maxSize int
	}
}

// TxWriter is the optional hook an Executor notifies on every key
// write. Implementations record the pre-write value so ROLLBACK can
// restore. The SYS layer wires this for transactional sessions.
type TxWriter interface {
	RecordWrite(key []byte, newValue []byte)
	// RecordInMemoryTable captures the pre-tx snapshot of an
	// in-memory table before the first mutation. On rollback the
	// implementation restores the table to this snapshot.
	// REQ000641.
	InMemoryTxWriter
}

// InMemoryTxWriter is the optional hook for in-memory table
// rollback support. Separated so callers can type-assert
// independently of the store-backed TxWriter.
type InMemoryTxWriter interface {
	RecordInMemoryTable(table string, snapshot []Row)
}

// SetTxWriter installs w as the current transaction's write hook. Pass
// nil to disable. Not safe to call concurrently with Exec; the
// caller (a Session) is responsible for serialization.
func (e *Executor) SetTxWriter(w TxWriter) {
	e.txWriter = w
	SetCurrentTxWriter(w)
}

// ClearTxWriter resets the write hook to nil. Pair with SetTxWriter.
func (e *Executor) ClearTxWriter() {
	e.txWriter = nil
	SetCurrentTxWriter(nil)
}

// ShallowCopy returns a new Executor that shares Planner and Store with the
// original but has its own per-request mutable state (txWriter, snapshotTS,
// sessionID). Callers use this to avoid races when the shared Executor is
// used concurrently by multiple sessions (REQ000611).
// The statement cache is re-initialized (not shared) since it contains a Mutex.
func (e *Executor) ShallowCopy() *Executor {
	e2 := &Executor{
		planner:     e.planner,
		store:       e.store,
		txnDebugger: NewTxnDebugger(),
	}
	e2.initStmtCache(e.stmtCache.maxSize)
	return e2
}

// SetSnapshot sets the per-statement snapshot timestamp for read-committed
// isolation (REQ000255). When non-zero, reads filter to versions visible at
// this timestamp. Pass 0 to disable snapshot filtering.
func (e *Executor) SetSnapshot(ts uint64) { e.snapshotTS = ts }

// Snapshot returns the current snapshot timestamp.
func (e *Executor) Snapshot() uint64 { return e.snapshotTS }

// SetSessionID sets the current session ID for counter access.
// REQ000385/394/411. Uses atomic store for the global currentSessionID
// to avoid races with concurrent sessions sharing one Executor.
func (e *Executor) SetSessionID(id uint64) {
	// Don't write e.sessionID - the Executor is shared across sessions.
	// Only update the atomic global that eval functions read.
	currentSessionID.Store(id)
}

// SessionID returns the current session ID.
func (e *Executor) SessionID() uint64 { return e.sessionID }

// getCurrentSessionID returns the package-level session ID for eval.
func getCurrentSessionID() uint64 {
	return currentSessionID.Load()
}

func NewExecutor() *Executor {
	e := &Executor{
		planner:     NewPlanner(),
		txnDebugger: NewTxnDebugger(),
	}
	e.initStmtCache(256)
	return e
}

func NewExecutorWithPlanner(pl *Planner) *Executor {
	e := &Executor{
		planner:     pl,
		txnDebugger: NewTxnDebugger(),
	}
	e.initStmtCache(256)
	return e
}

// WithStmtCache enables statement caching with the given max size.
// Call on a newly created Executor before concurrent use.
func (e *Executor) WithStmtCache(maxSize int) *Executor {
	e.initStmtCache(maxSize)
	return e
}

// initStmtCache initializes the statement cache. Must be called before use.
func (e *Executor) initStmtCache(maxSize int) {
	if maxSize <= 0 {
		maxSize = 256
	}
	e.stmtCache.entries = make(map[string]*stmtCacheEntry, maxSize)
	e.stmtCache.lru = make([]*stmtCacheEntry, 0, maxSize)
	e.stmtCache.maxSize = maxSize
}

// getCachedStmt looks up a cached parsed statement. Returns nil if not found.
func (e *Executor) getCachedStmt(sql string) PS.Stmt {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	ent, ok := e.stmtCache.entries[sql]
	if !ok {
		return nil
	}
	// Move to front of LRU
	for i, entry := range e.stmtCache.lru {
		if entry == ent {
			e.stmtCache.lru = append(e.stmtCache.lru[:i], e.stmtCache.lru[i+1:]...)
			break
		}
	}
	e.stmtCache.lru = append([]*stmtCacheEntry{ent}, e.stmtCache.lru...)
	return ent.stmt
}

// putCachedStmt stores a parsed statement in the cache.
func (e *Executor) putCachedStmt(sql string, stmt PS.Stmt) {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	if ent, ok := e.stmtCache.entries[sql]; ok {
		// Already cached, move to front
		for i, entry := range e.stmtCache.lru {
			if entry == ent {
				e.stmtCache.lru = append(e.stmtCache.lru[:i], e.stmtCache.lru[i+1:]...)
				break
			}
		}
		e.stmtCache.lru = append([]*stmtCacheEntry{ent}, e.stmtCache.lru...)
		return
	}
	ent := &stmtCacheEntry{stmt: stmt}
	e.stmtCache.entries[sql] = ent
	e.stmtCache.lru = append([]*stmtCacheEntry{ent}, e.stmtCache.lru...)
	// Evict LRU if over capacity
	for len(e.stmtCache.lru) > e.stmtCache.maxSize {
		oldest := e.stmtCache.lru[len(e.stmtCache.lru)-1]
		e.stmtCache.lru = e.stmtCache.lru[:len(e.stmtCache.lru)-1]
		for key, val := range e.stmtCache.entries {
			if val == oldest {
				delete(e.stmtCache.entries, key)
				break
			}
		}
	}
}

// clearStmtCache clears the statement cache. Used in tests.
func (e *Executor) clearStmtCache() {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	e.stmtCache.entries = nil
	e.stmtCache.lru = nil
}

// NewExecutorWithEngine wires the executor to a real storage engine. When
// store is non-nil, SeqScan / Insert / Update / Delete route through it
// instead of the in-memory tables map. Pass nil to revert to in-memory
// mode.
func NewExecutorWithEngine(store Store) *Executor {
	e := &Executor{planner: NewPlannerWithStore(store), store: store}
	e.initStmtCache(256)
	return e
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
	tablesMu.Lock()
	tablePKs[name] = pk
	tablesMu.Unlock()
	registerInMemorySchema(name, schema, pk)
	if e.store != nil {
		registerStoreSchema(name, schema, pk)
	}
}

func (e *Executor) RegisterIndex(table, index string, cols []string) {
	e.planner.RegisterIndex(table, index, cols)
}

func (e *Executor) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			// Check if this is a DML with RETURNING clause
			if hasReturning(stmt) {
				op, err := e.buildWriterOp(stmt)
				if err != nil {
					return Result{}, err
				}
				propagateParams(op, args)
				defer op.Close()
				var count int64
				for {
					_, err := op.Next(ctx)
					if err != nil {
						if err == ErrNoRows {
							break
						}
						return Result{}, err
					}
					count++
				}
				return Result{RowsAffected: count}, nil
			}

			op, err := e.buildWriterOp(stmt)
			if err != nil {
				return Result{}, err
			}
			propagateParams(op, args)
			propagatePlanner(op, e.planner)
			execCtx := &ExecContext{Planner: e.planner, SessionID: getCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
			propagateExecContext(op, execCtx)
			defer op.Close()
			if _, err := op.Next(ctx); err != nil && err != ErrNoRows {
				return Result{}, err
			}
			e.lastChanges = execCtx.LastChanges
			e.totalChanges = execCtx.TotalChanges
			return extractResult(op)
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return Result{}, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return Result{}, err
		}
		propagateParams(op, args)
		defer op.Close()
		var count int64
		for {
			_, err := op.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				return Result{}, err
			}
			count++
		}
		return Result{RowsAffected: count}, nil
	}

	op, err := e.buildWriterOp(stmt)
	if err != nil {
		return Result{}, err
	}
	// R16-1: thread args down to the operator tree so `?`
	// placeholders resolve. Writers (INSERT/UPDATE/DELETE) also
	// support placeholders (e.g. INSERT ... VALUES (?,?)).
	propagateParams(op, args)
	propagatePlanner(op, e.planner)
	execCtx := &ExecContext{Planner: e.planner, SessionID: getCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
	propagateExecContext(op, execCtx)
	defer op.Close()
	if _, err := op.Next(ctx); err != nil && err != ErrNoRows {
		return Result{}, err
	}
	e.lastChanges = execCtx.LastChanges
	e.totalChanges = execCtx.TotalChanges
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
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			// Check if this is a DML with RETURNING clause
			if hasReturning(stmt) {
				op, err := e.buildWriterOp(stmt)
				if err != nil {
					return nil, err
				}
				propagateParams(op, args)
				defer op.Close()
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
			propagateParams(plan.root, args)
			propagatePlanner(plan.root, e.planner)
			execCtx := &ExecContext{Planner: e.planner, SessionID: getCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
			propagateExecContext(plan.root, execCtx)
			defer plan.root.Close()
			row, err := plan.root.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					return &Rows{}, nil
				}
				return nil, err
			}
			WithExecContext(&row, execCtx)
			rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]int(nil), row.Types...)}
			return rs, nil
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
	}

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		propagateParams(op, args)
		defer op.Close()
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
	propagatePlanner(plan.root, e.planner)
	execCtx := &ExecContext{Planner: e.planner, SessionID: getCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.root, execCtx)
	defer plan.root.Close()
	row, err := plan.root.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			return &Rows{}, nil
		}
		return nil, err
	}
	WithExecContext(&row, execCtx)
	rs := &Rows{Cols: append([]string(nil), row.Cols...), Types: append([]int(nil), row.Types...)}
	return rs, nil
}

func (e *Executor) QueryAll(ctx context.Context, sql string, args ...any) ([]Row, error) {
	// Try cache first (P0: StmtCache wiring, saves 12.70% CPU on parsing)
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt := cached
			plan, err := e.planner.Plan(stmt)
			if err != nil {
				return nil, err
			}
			if plan == nil || plan.root == nil {
				return nil, errors.New("ex: plan produced no root")
			}
			propagateParams(plan.root, args)
			propagatePlanner(plan.root, e.planner)
			execCtx := &ExecContext{Planner: e.planner, SessionID: getCurrentSessionID(), TxWriter: e.txWriter, LastChanges: 0, TotalChanges: e.totalChanges}
			propagateExecContext(plan.root, execCtx)
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
				WithExecContext(&row, execCtx)
				out = append(out, row)
			}
			return out, nil
		}
	}

	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return nil, err
	}
	// Cache the parsed statement
	if e.stmtCache.entries != nil {
		e.putCachedStmt(sql, stmt)
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
	// REQ000586: thread ExecContext through rows to eliminate
	// the global currentSubqueryPlanner.
	execCtx := &ExecContext{Planner: e.planner, SessionID: getCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.root, execCtx)
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
		WithExecContext(&row, execCtx)
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
	type leftRighter interface {
		LeftChild() Operator
		RightChild() Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagatePlanner(lr.LeftChild(), p)
		propagatePlanner(lr.RightChild(), p)
	}
}

// propagateExecContext walks the operator tree and sets execCtx
// on operators that evaluate expressions (Filter, Project, etc.)
// so that subquery eval can find the planner via ExecContextFromRow.
// Also propagates execCtx to Insert/Update/Delete for change
// tracking (REQ000812).
func propagateExecContext(root Operator, ec *ExecContext) {
	if root == nil || ec == nil {
		return
	}
	if f, ok := root.(*Filter); ok {
		f.execCtx = ec
	}
	if p, ok := root.(*Project); ok {
		p.execCtx = ec
	}
	if ins, ok := root.(*Insert); ok {
		ins.execCtx = ec
	}
	if upd, ok := root.(*Update); ok {
		upd.execCtx = ec
	}
	if del, ok := root.(*Delete); ok {
		del.execCtx = ec
	}
	type childer interface {
		Child() Operator
	}
	if c, ok := root.(childer); ok {
		propagateExecContext(c.Child(), ec)
	}
	type leftRighter interface {
		LeftChild() Operator
		RightChild() Operator
	}
	if lr, ok := root.(leftRighter); ok {
		propagateExecContext(lr.LeftChild(), ec)
		propagateExecContext(lr.RightChild(), ec)
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
	if w, ok := root.(interface{ WithParams([]any) Operator }); ok {
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

// asAnySlice converts []any to []any for type-stability
// across the WithParams interface boundary. Avoids an allocation
// when the slice is already nil.
func asAnySlice(args []any) []any {
	if args == nil {
		return nil
	}
	out := make([]any, len(args))
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
	// Use formatPlanTree with default ExplainNormal mode for text output
	nodes := buildPlanNodeTree(plan.root, e.planner)
	var b strings.Builder
	var walk func(node *PlanNode, depth int)
	walk = func(node *PlanNode, depth int) {
		if node == nil {
			return
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(node.Type)
		if node.Table != "" {
			b.WriteString(" ")
			b.WriteString(node.Table)
		}
		if node.Detail != "" {
			b.WriteString(" ")
			b.WriteString(node.Detail)
		}
		b.WriteByte('\n')
		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}
	walk(nodes, 0)
	return b.String(), nil
}

func (e *Executor) buildWriterOp(stmt PS.Stmt) (Operator, error) {
	switch s := stmt.(type) {
	case *PS.Insert:
		// REQ000707: INSERT INTO t SELECT ...
		if s.Select != nil {
			selPlan, err := e.planner.Plan(s.Select)
			if err != nil {
				return nil, err
			}
			if selPlan == nil || selPlan.root == nil {
				return nil, fmt.Errorf("ex: INSERT SELECT: plan produced no root")
			}
			op := NewInsert(s.Table, s.Cols, nil, s.Returning, s.OnConflict)
			op.selectPlan = selPlan.root
			op.conflictAction = s.ConflictAction
			propagatePlanner(selPlan.root, e.planner)
			return op, nil
		}

		op, iErr := func() (*Insert, error) {
			if e.store != nil {
				return NewInsertWithStore(e.store, s.Table, s.Cols, s.Values, s.Returning, s.OnConflict)
			}
			return NewInsert(s.Table, s.Cols, s.Values, s.Returning, s.OnConflict), nil
		}()
		if iErr != nil {
			return nil, iErr
		}
		op.conflictAction = s.ConflictAction
		op.defaultValues = s.DefaultValues
		return op, nil
	case *PS.Update:
		targetTable := s.Table
		if viewSel := LookupView(targetTable); viewSel != nil {
			targetTable = viewSel.From
		}
		var scan Operator = NewSeqScan(targetTable)
		if e.store != nil {
			ssc, err := NewSeqScanWithStore(e.store, targetTable)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := NewFilter(scan, s.Where)
		// REQ000558: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before updating.
		var current Operator = filter
		if len(s.OrderBy) > 0 {
			current = NewSort(current, s.OrderBy)
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := NewUpdateWithStore(e.store, targetTable, s.Set, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewUpdate(targetTable, s.Set, s.Where, current, s.Returning), nil
	case *PS.Delete:
		tableName := s.Table
		if viewSel := LookupView(tableName); viewSel != nil {
			tableName = viewSel.From
		}
		var scan Operator = NewSeqScan(tableName)
		if e.store != nil {
			ssc, err := NewSeqScanWithStore(e.store, tableName)
			if err != nil {
				return nil, err
			}
			scan = ssc
		}
		filter := NewFilter(scan, s.Where)
		// REQ000475: apply ORDER BY / LIMIT / OFFSET to the row
		// selection before deleting.
		var current Operator = filter
		if len(s.OrderBy) > 0 {
			current = NewSort(current, s.OrderBy)
		}
		if s.OffsetFirst {
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
		} else {
			if s.Offset != nil {
				if n, ok := limitInt64(s.Offset); ok && n > 0 {
					current = NewOffset(current, n)
				}
			}
			if s.Limit != nil {
				if n, ok := limitInt64(s.Limit); ok {
					current = NewLimit(current, n)
				}
			}
		}
		if e.store != nil {
			op, err := NewDeleteWithStore(e.store, tableName, s.Where, current, s.Returning)
			if err != nil {
				return nil, err
			}
			return op, nil
		}
		return NewDelete(tableName, s.Where, current, s.Returning), nil
	case *PS.CreateTable:
		if s.Select != nil {
			// CREATE TABLE AS SELECT needs the planner to
			// build the inner SELECT plan. REQ000520.
			op, err := e.planner.Plan(s)
			if err != nil {
				return nil, err
			}
			if op == nil || op.root == nil {
				return nil, errors.New("ex: plan produced no root for CTAS")
			}
			return op.root, nil
		}
		return NewCreateTable(s), nil
	case *PS.DropTable:
		return NewDropTable(s), nil
	case *PS.CreateIndexStmt:
		// Note: the planner's cost-based selection (REQ000156)
		// is keyed off ex.RegisterIndex, not CREATE INDEX.
		// CREATE INDEX only registers the index for writer
		// maintenance; it does not backfill existing rows into
		// the index keyspace. Tests that want cost-based
		// selection should call ex.RegisterIndex explicitly.
		return NewCreateIndex(s), nil
	case *PS.DropIndexStmt:
		return NewDropIndex(s), nil
	case *PS.CreateViewStmt:
		return NewCreateView(s), nil
	case *PS.CreateMatViewStmt:
		return NewCreateMatView(s.Name, s.As, e.store), nil
	case *PS.DropMatViewStmt:
		return NewDropMatView(s.Name, e.store), nil
	case *PS.RefreshMatViewStmt:
		// Lookup the matview definition from registry
		sel := LookupMatView(s.Name)
		if sel == nil {
			return nil, fmt.Errorf("ex: materialized view %q not found", s.Name)
		}
		return NewRefreshMatView(s.Name, sel, e.store, e.planner), nil
	case *PS.VacuumStmt:
		return NewVacuum(s), nil
	case *PS.AnalyzeStmt:
		return NewAnalyze(s), nil
	case *PS.AlterTableStmt:
		return NewAlterTable(s), nil
	case *PS.TriggerStmt:
		return NewTrigger(s), nil
	case *PS.DropViewStmt:
		return NewDropView(s), nil
	case *PS.DropTriggerStmt:
		return NewDropTrigger(s), nil
	case *PS.PragmaStmt:
		return NewPragma(s), nil
	case *PS.ExplainStmt:
		return NewExplain(s), nil
	case *PS.TruncateStmt:
		return NewTruncate(s), nil
	case *PS.ReindexStmt:
		return NewReindex(s), nil
	case *PS.BeginTX:
		return NewNoop(), nil
	case *PS.CommitTX:
		return NewNoop(), nil
	case *PS.ValuesStmt:
		return newValuesRowsOp(s.Rows), nil
	case *PS.AttachStmt:
		// REQ000557: ATTACH DATABASE — parser accepts the
		// syntax but the v1 executor refuses to run it.
		return NewUnsupportedOp(s, ErrMultiDatabaseNotSupported.Error()), nil
	case *PS.DetachStmt:
		// REQ000557: DETACH DATABASE — same as ATTACH.
		return NewUnsupportedOp(s, ErrMultiDatabaseNotSupported.Error()), nil
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

// QueryStream runs a SELECT and returns a streaming iterator that
// yields rows one at a time. The caller MUST call Close on the
// returned iterator to release the underlying plan resources.
// REQ000348.
func (e *Executor) QueryStream(ctx context.Context, sql string, args ...any) (*streamIterator, error) {
	// REQ000771: try the in-Executor cache before parsing. The
	// ST.Stmt.Query hot path goes through here, and avoiding the
	// parser pass on repeated queries reclaims the 12% CPU that
	// parsing was costing.
	var stmt PS.Stmt
	if e.stmtCache.entries != nil {
		if cached := e.getCachedStmt(sql); cached != nil {
			stmt = cached
		}
	}
	if stmt == nil {
		parser := PS.NewParser(sql)
		parsed, err := parser.Parse()
		if err != nil {
			return nil, err
		}
		stmt = parsed
		if e.stmtCache.entries != nil {
			e.putCachedStmt(sql, stmt)
		}
	}
	return e.QueryStreamFromAST(ctx, stmt, args...)
}

// QueryStreamFromAST runs a pre-parsed statement through the
// planner and returns a streaming iterator. Skips the parser pass
// — used by REQ000771's StmtCache to avoid re-parsing on repeated
// queries. The caller must ensure `stmt` is the AST for the same
// SQL that produced this entry (or accept semantic divergence if
// DDL changed schema).
func (e *Executor) QueryStreamFromAST(ctx context.Context, stmt PS.Stmt, args ...any) (*streamIterator, error) {

	// Check if this is a DML with RETURNING clause
	if hasReturning(stmt) {
		op, err := e.buildWriterOp(stmt)
		if err != nil {
			return nil, err
		}
		propagateParams(op, args)
		firstRow, firstErr := op.Next(ctx)
		if firstErr != nil && firstErr != ErrNoRows {
			op.Close()
			return nil, firstErr
		}
		if firstErr == ErrNoRows {
			// No RETURNING rows; return empty iterator
			op.Close()
			return &streamIterator{
				cols:  nil,
				types: nil,
				rowCh: nil,
				done:  true,
			}, nil
		}
		// Wrap in a buffered channel so the caller can pull rows
		// sequentially after the first.
		rowCh := make(chan Row, 16)
		rowCh <- firstRow
		closed := false
		var closeOnce sync.Once
		closer := func() error {
			closeOnce.Do(func() {
				closed = true
			})
			return op.Close()
		}
		go func() {
			defer close(rowCh)
			for {
				if closed {
					return
				}
				row, err := op.Next(ctx)
				if err != nil {
					return
				}
				select {
				case rowCh <- row:
				case <-ctx.Done():
					return
				}
			}
		}()
		return &streamIterator{
			cols:   append([]string(nil), firstRow.Cols...),
			types:  append([]int(nil), firstRow.Types...),
			rowCh:  rowCh,
			closer: closer,
		}, nil
	}

	plan, err := e.planner.Plan(stmt)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.root == nil {
		return nil, errors.New("ex: plan produced no root")
	}
	propagateParams(plan.root, args)
	propagatePlanner(plan.root, e.planner)
	// REQ000586: thread ExecContext to eliminate global.
	execCtx := &ExecContext{Planner: e.planner, SessionID: getCurrentSessionID(), TxWriter: e.txWriter, LastChanges: e.lastChanges, TotalChanges: e.totalChanges}
	propagateExecContext(plan.root, execCtx)

	// Read first row to discover schema
	row, err := plan.root.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			plan.root.Close()
			return &streamIterator{
				cols:  nil,
				types: nil,
				rowCh: nil,
				done:  true,
			}, nil
		}
		plan.root.Close()
		return nil, err
	}
	WithExecContext(&row, execCtx)
	cols := append([]string(nil), row.Cols...)
	types := append([]int(nil), row.Types...)

	rowCh := make(chan Row, 16)
	rowCh <- row
	closed := false
	var closeMu sync.Mutex
	closer := func() error {
		closeMu.Lock()
		defer closeMu.Unlock()
		if closed {
			return nil
		}
		closed = true
		return plan.root.Close()
	}
	go func() {
		defer close(rowCh)
		for {
			closeMu.Lock()
			if closed {
				closeMu.Unlock()
				return
			}
			closeMu.Unlock()
			r, err := plan.root.Next(ctx)
			if err != nil {
				return
			}
			WithExecContext(&r, execCtx)
			select {
			case rowCh <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &streamIterator{
		cols:   cols,
		types:  types,
		rowCh:  rowCh,
		closer: closer,
	}, nil
}

// streamIterator is the streaming row iterator returned by
// Executor.QueryStream. It buffers one row at a time so the caller can
// discover the schema before draining the rest.
// REQ000348.
type streamIterator struct {
	cols   []string
	types  []int
	rowCh  chan Row
	closer func() error
	done   bool
	mu     sync.Mutex
}

func (s *streamIterator) Cols() []string { return s.cols }
func (s *streamIterator) Types() []int   { return s.types }
func (s *streamIterator) Next() (Row, error) {
	if s == nil || s.done || s.rowCh == nil {
		return Row{}, ErrNoRows
	}
	r, ok := <-s.rowCh
	if !ok {
		s.done = true
		return Row{}, ErrNoRows
	}
	return r, nil
}
func (s *streamIterator) Close() error {
	if s == nil {
		return nil
	}
	s.done = true
	if s.closer != nil {
		return s.closer()
	}
	return nil
}

// Noop is a no-op operator that returns ErrNoRows on Next.
// Used for statements like BEGIN that affect state but produce no results.
type Noop struct{}

var _ Operator = (*Noop)(nil)

func NewNoop() *Noop {
	return &Noop{}
}

func (n *Noop) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNoRows
}

func (n *Noop) Close() error {
	return nil
}

func (n *Noop) WithParams(p []any) Operator {
	return n
}

// TxnDebugger returns the executor's transaction debugger.
// REQ000792: MVCC debugging.
func (e *Executor) TxnDebugger() *TxnDebugger {
	return e.txnDebugger
}

// StmtCacheStats returns the statement cache statistics.
// REQ000793: Plan cache analysis.
func (e *Executor) StmtCacheStats() *CacheStats {
	e.stmtCache.mu.Lock()
	defer e.stmtCache.mu.Unlock()
	// Count entries
	size := len(e.stmtCache.entries)
	return &CacheStats{
		Hits:      0, // tracked separately if needed
		Misses:    0,
		Evictions: 0,
		MaxSize:   size,
	}
}
