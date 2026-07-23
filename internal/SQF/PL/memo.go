// Package PL hosts the planner cluster: AST fingerprinting (memoization
// keys) and the plan container. The tree-construction logic remains in
// EX because it instantiates executor operators; PL owns the pure-planning
// surface that EX depends on.
package PL

import (
	"container/list"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Statement tags (kept in sync with EX/memo.go tag constants).
const (
	tagSelect      = 1
	tagInsert      = 2
	tagUpdate      = 3
	tagDelete      = 4
	tagCreateTable = 5
	tagDropTable   = 6
	tagCompound    = 7 // REQ000814: UNION/UNION ALL/INTERSECT/EXCEPT

	tagNullLit   = 10
	tagNumberLit = 11
	tagFloatLit  = 12
	tagStringLit = 13
	tagBoolLit   = 14
	tagIdent     = 15
	tagParam     = 16
	tagStar      = 17
	tagUnary     = 18
	tagBinary    = 19
	tagFunc      = 20
	tagAgg       = 21
	tagList      = 22
	tagBetween   = 23
	tagCase      = 24
	tagIn        = 25
	tagAlias     = 26
	tagCast      = 27
	tagExists    = 28
	tagSubq      = 29
	tagQName     = 30
)

// DefaultMaxMemoEntries is the default upper bound for memoized plans.
// REQ000584: bounded growth via LRU eviction.
const DefaultMaxMemoEntries = 4096

// Plan is the planner-side container for a memoized plan. EX wraps it
// with its own concrete root operator; PL only owns the metadata.
type Plan struct {
	Cost    float64
	MemoKey string
}

// lruNode is one entry in the LRU list. The list element value
// references the same key+plan stored in entries so eviction is O(1).
// REQ000584.
type lruNode struct {
	key  string
	plan Plan
}

// Memo caches plans keyed by AST fingerprint with bounded LRU
// eviction and schema-version-aware invalidation. REQ000584.
// Schema invalidation: SchemaVersion() returns the current version,
// and BumpSchemaVersion() increments it. SerializeKey() mixes the
// version into the memo key, so DDL (which calls
// BumpSchemaVersion) implicitly invalidates every cached plan
// because their keys no longer match.
type Memo struct {
	mu            sync.RWMutex
	schemaVersion uint64
	entries       map[string]*list.Element
	lru           *list.List
	maxEntries    int
}

// NewMemo returns an empty memo with the default LRU capacity.
func NewMemo() *Memo {
	return NewMemoWithCapacity(DefaultMaxMemoEntries)
}

// NewMemoWithCapacity returns an empty memo with the given LRU
// capacity. A non-positive capacity falls back to
// DefaultMaxMemoEntries.
func NewMemoWithCapacity(maxEntries int) *Memo {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxMemoEntries
	}
	return &Memo{
		entries:    make(map[string]*list.Element),
		lru:        list.New(),
		maxEntries: maxEntries,
	}
}

// SchemaVersion returns the current schema version. Mixed into
// SerializeKey output so DDL invalidates cached plans. REQ000584.
func (m *Memo) SchemaVersion() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.schemaVersion
}

// BumpSchemaVersion increments the schema version. Caller should
// invoke this on CREATE/DROP/ALTER TABLE/INDEX. REQ000584.
func (m *Memo) BumpSchemaVersion() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schemaVersion++
	return m.schemaVersion
}

// Get returns a cached plan and whether it was present. A hit
// promotes the entry to most-recently-used.
func (m *Memo) Get(key string) (Plan, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.entries[key]
	if !ok {
		return Plan{}, false
	}
	m.lru.MoveToFront(el)
	return el.Value.(*lruNode).plan, true
}

// Put stores a plan, evicting the least-recently-used entry if the
// cache is full. Re-Put of an existing key updates the plan and
// promotes the entry.
func (m *Memo) Put(key string, p Plan) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.entries[key]; ok {
		el.Value.(*lruNode).plan = p
		m.lru.MoveToFront(el)
		return
	}
	el := m.lru.PushFront(&lruNode{key: key, plan: p})
	m.entries[key] = el
	if m.lru.Len() > m.maxEntries {
		m.evictOldestLocked()
	}
}

// Len returns the number of memoized plans.
func (m *Memo) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// MaxEntries returns the configured LRU capacity.
func (m *Memo) MaxEntries() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxEntries
}

// Clear drops all cached plans. Callers (e.g. DDL paths that do
// not want to rely on schema-version invalidation) can invoke
// this directly. REQ000584.
func (m *Memo) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]*list.Element)
	m.lru.Init()
}

func (m *Memo) evictOldestLocked() {
	el := m.lru.Back()
	if el == nil {
		return
	}
	node := el.Value.(*lruNode)
	delete(m.entries, node.key)
	m.lru.Remove(el)
}

type enc struct {
	buf []byte
}

func (e *enc) writeUvarint(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	e.buf = append(e.buf, tmp[:n]...)
}

func (e *enc) writeBool(b bool) {
	if b {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

func (e *enc) writeString(s string) {
	e.writeUvarint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *enc) writeOp(op LX.TokenType) {
	e.writeUvarint(uint64(LX.TokenType(op)))
}

func (e *enc) writeExpr(x PS.Expr) {
	if x == nil {
		e.buf = append(e.buf, 0)
		return
	}
	e.buf = append(e.buf, 1)
	switch v := x.(type) {
	case *PS.NumberLiteral:
		e.buf = append(e.buf, tagNumberLit)
		var tmp [binary.MaxVarintLen64]byte
		n := binary.PutVarint(tmp[:], v.Val)
		e.buf = append(e.buf, tmp[:n]...)
	case *PS.FloatLiteral:
		e.buf = append(e.buf, tagFloatLit)
		bits := uint64(v.Val)
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], bits)
		e.buf = append(e.buf, tmp[:]...)
	case *PS.StringLiteral:
		e.buf = append(e.buf, tagStringLit)
		e.writeString(v.Val)
	case *PS.BoolLiteral:
		e.buf = append(e.buf, tagBoolLit)
		e.writeBool(v.Val)
	case *PS.NullLiteral:
		e.buf = append(e.buf, tagNullLit)
	case *PS.Ident:
		e.buf = append(e.buf, tagIdent)
		e.writeString(v.Name)
	case *PS.QualifiedName:
		e.buf = append(e.buf, tagQName)
		e.writeString(v.Table)
		e.writeString(v.Name)
	case *PS.AliasedExpr:
		e.buf = append(e.buf, tagAlias)
		e.writeExpr(v.Expr)
		e.writeString(v.Alias)
	case *PS.Param:
		e.buf = append(e.buf, tagParam)
		e.writeUvarint(uint64(v.Index))
	case *PS.StarExpr:
		e.buf = append(e.buf, tagStar)
	case *PS.UnaryExpr:
		e.buf = append(e.buf, tagUnary)
		e.writeOp(v.Op)
		e.writeExpr(v.Operand)
	case *PS.BinaryExpr:
		e.buf = append(e.buf, tagBinary)
		e.writeOp(v.Op)
		e.writeExpr(v.Left)
		e.writeExpr(v.Right)
	case *PS.FunctionCall:
		e.buf = append(e.buf, tagFunc)
		e.writeString(v.Name)
		e.writeUvarint(uint64(len(v.Args)))
		for _, a := range v.Args {
			e.writeExpr(a)
		}
	case *PS.AggregateFunc:
		e.buf = append(e.buf, tagAgg)
		e.writeString(v.Name)
		e.writeExpr(v.Arg)
	case *PS.CastExpr:
		e.buf = append(e.buf, tagCast)
		e.writeExpr(v.Expr)
		e.writeUvarint(uint64(v.Type.Type))
	case *PS.ListExpr:
		e.buf = append(e.buf, tagList)
		e.writeUvarint(uint64(len(v.Items)))
		for _, it := range v.Items {
			e.writeExpr(it)
		}
	case *PS.BetweenExpr:
		e.buf = append(e.buf, tagBetween)
		e.writeExpr(v.Expr)
		e.writeExpr(v.Low)
		e.writeExpr(v.High)
	case *PS.CaseExpr:
		e.buf = append(e.buf, tagCase)
		e.writeExpr(v.Expr)
		e.writeUvarint(uint64(len(v.WhenList)))
		for _, w := range v.WhenList {
			e.writeExpr(w.Cond)
			e.writeExpr(w.Then)
		}
		e.writeExpr(v.Else)
	case *PS.InExpr:
		e.buf = append(e.buf, tagIn)
		e.writeExpr(v.Expr)
		e.writeUvarint(uint64(len(v.List)))
		for _, it := range v.List {
			e.writeExpr(it)
		}
		if v.Subquery == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeStmt(v.Subquery)
		}
	case *PS.ExistsExpr:
		e.buf = append(e.buf, tagExists)
		if v.Subquery == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeStmt(v.Subquery)
		}
	case *PS.SubqueryExpr:
		e.buf = append(e.buf, tagSubq)
		if v.Subquery == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeStmt(v.Subquery)
		}
	}
}

func (e *enc) writeColDef(c PS.ColDef) {
	e.writeString(c.Name)
	e.writeUvarint(uint64(c.Type))
	e.writeUvarint(uint64(c.Size))
	e.writeBool(c.Nullable)
	e.writeBool(c.PK)
	e.writeBool(c.Unique)
	e.writeExpr(c.Default)
}

func (e *enc) writeStmt(s PS.Stmt) {
	if s == nil {
		e.buf = append(e.buf, 0)
		return
	}
	e.buf = append(e.buf, 1)
	switch v := s.(type) {
	case *PS.Select:
		e.buf = append(e.buf, tagSelect)
		e.writeBool(v.Distinct)
		e.writeUvarint(uint64(len(v.Cols)))
		for _, c := range v.Cols {
			e.writeExpr(c)
		}
		e.writeString(v.From)
		e.writeString(v.FromAlias)
		e.writeExpr(v.Where)
		e.writeUvarint(uint64(len(v.OrderBy)))
		for _, o := range v.OrderBy {
			e.writeExpr(o.Expr)
			e.writeBool(o.Desc)
		}
		e.writeUvarint(uint64(len(v.Joins)))
		for _, j := range v.Joins {
			e.writeString(j.Kind)
			e.writeString(j.Right)
			e.writeExpr(j.On)
		}
		e.writeUvarint(uint64(len(v.GroupBy)))
		for _, g := range v.GroupBy {
			e.writeExpr(g)
		}
		e.writeExpr(v.Having)
		e.writeStmt(v.SubqueryFrom)
		e.writeExpr(v.Limit)
		e.writeExpr(v.Offset)
	case *PS.Insert:
		e.buf = append(e.buf, tagInsert)
		e.writeString(v.Table)
		e.writeUvarint(uint64(len(v.Cols)))
		for _, c := range v.Cols {
			e.writeString(c)
		}
		e.writeUvarint(uint64(len(v.Values)))
		for _, row := range v.Values {
			e.writeUvarint(uint64(len(row)))
			for _, cell := range row {
				e.writeExpr(cell)
			}
		}
	case *PS.Update:
		e.buf = append(e.buf, tagUpdate)
		e.writeString(v.Table)
		e.writeUvarint(uint64(len(v.Set)))
		for _, p := range v.Set {
			e.writeString(p.Col)
			e.writeExpr(p.Val)
		}
		e.writeExpr(v.Where)
	case *PS.Delete:
		e.buf = append(e.buf, tagDelete)
		e.writeString(v.Table)
		e.writeExpr(v.Where)
	case *PS.CreateTable:
		e.buf = append(e.buf, tagCreateTable)
		e.writeString(v.Name)
		e.writeUvarint(uint64(len(v.Cols)))
		for _, c := range v.Cols {
			e.writeColDef(c)
		}
		if v.PK == nil {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.writeString(*v.PK)
		}
	case *PS.DropTable:
		e.buf = append(e.buf, tagDropTable)
		e.writeString(v.Name)
	case *PS.CompoundStmt:
		e.buf = append(e.buf, tagCompound)
		e.writeStmt(v.Left)
		e.writeUvarint(uint64(v.Op))
		e.writeStmt(v.Right)
		e.writeUvarint(uint64(len(v.OrderBy)))
		for _, o := range v.OrderBy {
			e.writeExpr(o.Expr)
			e.writeBool(o.Desc)
		}
		e.writeExpr(v.Limit)
		e.writeExpr(v.Offset)
	}
}

// xxhash64 is a small, non-cryptographic 64-bit hash used to
// fingerprint ASTs in the memo. The interface mirrors
// github.com/cespare/xxhash/v2's Sum64, but the implementation
// is a FNV-1a style mix: the planner does not need a real
// xxhash, it needs a fast, well-distributed hash without
// security properties. REQ000584.
type xxhash64 struct {
	v uint64
}

const (
	xxhashSeed uint64 = 0x9E3779B97F4A7C15
	xxhashMul  uint64 = 0x100000001B3
)

// newXXHash64 starts a hash with the canonical seed.
func newXXHash64() *xxhash64 {
	return &xxhash64{v: xxhashSeed}
}

// Write mixes b into the running hash.
func (h *xxhash64) Write(b []byte) {
	for _, c := range b {
		h.v ^= uint64(c)
		h.v *= xxhashMul
	}
}

// WriteUvarint mixes an unsigned varint into the running hash so
// lengths and tags participate in the digest.
func (h *xxhash64) WriteUvarint(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	h.Write(tmp[:n])
}

// Sum64 returns the current digest.
func (h *xxhash64) Sum64() uint64 {
	x := h.v
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

// SerializeKey returns a stable 16-hex-character fingerprint of an
// AST, mixing in the current schema version so DDL invalidates
// every cached plan. The hash uses xxhash-style FNV mixing instead
// of SHA-256: memoization does not need cryptographic strength, only
// a fast, well-distributed digest. REQ000584.
func SerializeKey(stmt PS.Stmt) string {
	e := &enc{}
	e.writeStmt(stmt)
	return SerializeKeyBytes(e.buf, SchemaVersion())
}

// SerializeKeyWithSchema is like SerializeKey but mixes the
// supplied schema version into the digest. Callers that already
// hold a version snapshot can use this to avoid racing on the
// global version. REQ000584.
func SerializeKeyWithSchema(stmt PS.Stmt, schemaVersion uint64) string {
	e := &enc{}
	e.writeStmt(stmt)
	return SerializeKeyBytes(e.buf, schemaVersion)
}

// SerializeKeyBytes hashes a pre-encoded AST buffer with a
// schema-version salt. Exposed for tests. REQ000584.
func SerializeKeyBytes(buf []byte, schemaVersion uint64) string {
	h := newXXHash64()
	h.WriteUvarint(schemaVersion)
	h.Write(buf)
	v := h.Sum64()
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = hex[v&0xF]
		v >>= 4
	}
	return string(out)
}

// defaultMemoSchemaVersion is the schema version used by
// SerializeKey. It is a package-level atomic counter so the
// free-function form remains safe without exposing a Memo
// instance. Tests and callers that need fine-grained control
// should use SerializeKeyWithSchema or hold a *Memo and call
// SerializeKeyWithSchema explicitly.
var defaultMemoSchemaVersion atomic.Uint64

// BumpDefaultSchemaVersion increments the package-level schema
// version. Use this from DDL entry points that do not own a
// *Memo. REQ000584.
func BumpDefaultSchemaVersion() uint64 {
	return defaultMemoSchemaVersion.Add(1)
}

// SchemaVersion returns the package-level schema version mixed
// into SerializeKey output. REQ000584.
func SchemaVersion() uint64 {
	return defaultMemoSchemaVersion.Load()
}

// isComparisonOp reports whether op is a comparison operator
// (=, !=, <, >, <=, >=) that can be safely parameterized.
func isComparisonOp(op LX.TokenType) bool {
	switch op {
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return true
	}
	return false
}

// isColumnRef reports whether expr is a column reference
// (Ident or QualifiedName) that could appear on one side of
// a comparison predicate.
func isColumnRef(expr PS.Expr) bool {
	switch expr.(type) {
	case *PS.Ident, *PS.QualifiedName:
		return true
	}
	return false
}

// isLiteral reports whether expr is a literal value that can
// be replaced with a PS.Param placeholder.
func isLiteral(expr PS.Expr) bool {
	switch expr.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral:
		return true
	}
	return false
}

// literalToAny extracts the Go value from a literal AST node.
func literalToAny(expr PS.Expr) any {
	switch v := expr.(type) {
	case *PS.NumberLiteral:
		return v.Val
	case *PS.FloatLiteral:
		return v.Val
	case *PS.StringLiteral:
		return v.Val
	case *PS.BoolLiteral:
		return v.Val
	}
	return nil
}

// cloneExprForMemo returns a shallow clone of expr with literals
// in comparison predicates replaced by PS.Param nodes. Original
// values are appended to params. REQ001195.
//
// This is used ONLY for memo key generation — the original AST
// cloneArena is a bump-pointer allocator for cloned AST nodes used
// by cloneExprForMemo/cloneStmtForMemo. All cloned nodes are short-lived
// (used only for memo key generation, then discarded), so the entire
// arena can be freed at once. REQ001695.
type cloneArena struct {
	slab  []byte
	offset int
	slabs [][]byte
}

func (a *cloneArena) init() {
	slab := make([]byte, 4096)
	a.slab = slab
	a.offset = 0
	a.slabs = append(a.slabs[:0], slab)
}

func (a *cloneArena) alloc(size int, align uintptr) unsafe.Pointer {
	a.offset = int((uintptr(a.offset) + align - 1) &^ (align - 1))
	if a.offset+size > len(a.slab) {
		newCap := len(a.slab) * 2
		if newCap < size {
			newCap = size
		}
		slab := make([]byte, newCap)
		a.slab = slab
		a.offset = 0
		a.slabs = append(a.slabs, slab)
	}
	ptr := unsafe.Pointer(&a.slab[a.offset])
	a.offset += size
	return ptr
}

func cloneAlloc[T any](a *cloneArena, v *T) *T {
	size := int(unsafe.Sizeof(*v))
	align := unsafe.Alignof(*v)
	ptr := a.alloc(size, align)
	*(*T)(ptr) = *v
	return (*T)(ptr)
}

// is still used for plan building. The parameterized key captures
// "same join structure + same comparison operators + same column
// references" regardless of constant values.
func cloneExprForMemo(expr PS.Expr, params *[]any, arena *cloneArena) PS.Expr {
	if expr == nil {
		return nil
	}
	switch v := expr.(type) {
	case *PS.BinaryExpr:
		if isComparisonOp(v.Op) {
			// column = literal → column = Param
			if isColumnRef(v.Left) && isLiteral(v.Right) {
				idx := len(*params)
				*params = append(*params, literalToAny(v.Right))
				return cloneAlloc(arena, &PS.BinaryExpr{
					Op:    v.Op,
					Left:  v.Left,
					Right: &PS.Param{Index: idx},
				})
			}
			// literal = column → Param = column
			if isLiteral(v.Left) && isColumnRef(v.Right) {
				idx := len(*params)
				*params = append(*params, literalToAny(v.Left))
				return cloneAlloc(arena, &PS.BinaryExpr{
					Op:    v.Op,
					Left:  &PS.Param{Index: idx},
					Right: v.Right,
				})
			}
		}
		// Recurse into both sides for nested comparisons
		return cloneAlloc(arena, &PS.BinaryExpr{
			Op:    v.Op,
			Left:  cloneExprForMemo(v.Left, params, arena),
			Right: cloneExprForMemo(v.Right, params, arena),
		})
	case *PS.UnaryExpr:
		return cloneAlloc(arena, &PS.UnaryExpr{
			Op:      v.Op,
			Operand: cloneExprForMemo(v.Operand, params, arena),
		})
	case *PS.InExpr:
		items := make([]PS.Expr, len(v.List))
		for i, item := range v.List {
			items[i] = cloneExprForMemo(item, params, arena)
		}
		return cloneAlloc(arena, &PS.InExpr{
			Expr:     cloneExprForMemo(v.Expr, params, arena),
			List:     items,
			Subquery: v.Subquery, // subqueries handled separately
		})
	case *PS.BetweenExpr:
		return cloneAlloc(arena, &PS.BetweenExpr{
			Expr: cloneExprForMemo(v.Expr, params, arena),
			Low:  cloneExprForMemo(v.Low, params, arena),
			High: cloneExprForMemo(v.High, params, arena),
		})
	case *PS.FunctionCall:
		args := make([]PS.Expr, len(v.Args))
		for i, a := range v.Args {
			args[i] = cloneExprForMemo(a, params, arena)
		}
		return cloneAlloc(arena, &PS.FunctionCall{Name: v.Name, Args: args})
	case *PS.CaseExpr:
		whenList := make([]PS.WhenClause, len(v.WhenList))
		for i, w := range v.WhenList {
			whenList[i] = PS.WhenClause{
				Cond: cloneExprForMemo(w.Cond, params, arena),
				Then: cloneExprForMemo(w.Then, params, arena),
			}
		}
		return cloneAlloc(arena, &PS.CaseExpr{
			Expr:     cloneExprForMemo(v.Expr, params, arena),
			WhenList: whenList,
			Else:     cloneExprForMemo(v.Else, params, arena),
		})
	case *PS.CastExpr:
		return cloneAlloc(arena, &PS.CastExpr{
			Expr: cloneExprForMemo(v.Expr, params, arena),
			Type: v.Type,
		})
	case *PS.ListExpr:
		items := make([]PS.Expr, len(v.Items))
		for i, item := range v.Items {
			items[i] = cloneExprForMemo(item, params, arena)
		}
		return cloneAlloc(arena, &PS.ListExpr{Items: items})
	case *PS.AliasedExpr:
		return cloneAlloc(arena, &PS.AliasedExpr{
			Expr:  cloneExprForMemo(v.Expr, params, arena),
			Alias: v.Alias,
		})
	case *PS.SubqueryExpr:
		return v // subqueries are structure, not parameterizable
	case *PS.ExistsExpr:
		return v
	}
	// Ident, QualifiedName, Param, StarExpr, literals not in comparison — pass through
	return expr
}

// cloneStmtForMemo clones a SELECT statement with literals in
// comparison predicates replaced by PS.Param nodes. Returns the
// parameterized statement and the extracted literal values in
// walk order. REQ001195.
func cloneStmtForMemo(stmt PS.Stmt, params *[]any, arena *cloneArena) PS.Stmt {
	if stmt == nil {
		return nil
	}
	switch v := stmt.(type) {
	case *PS.Select:
		cols := make([]PS.Expr, len(v.Cols))
		for i, c := range v.Cols {
			cols[i] = cloneExprForMemo(c, params, arena)
		}
		joins := make([]PS.JoinClause, len(v.Joins))
		for i, j := range v.Joins {
			joins[i] = PS.JoinClause{
				Kind:  j.Kind,
				Right: j.Right,
				On:    cloneExprForMemo(j.On, params, arena),
			}
		}
		groupBy := make([]PS.Expr, len(v.GroupBy))
		for i, g := range v.GroupBy {
			groupBy[i] = cloneExprForMemo(g, params, arena)
		}
		return cloneAlloc(arena, &PS.Select{
			Distinct:      v.Distinct,
			Cols:          cols,
			From:          v.From,
			FromAlias:     v.FromAlias,
			Where:         cloneExprForMemo(v.Where, params, arena),
			OrderBy:       v.OrderBy, // ORDER BY exprs not parameterized
			Joins:         joins,
			GroupBy:       groupBy,
			Having:        cloneExprForMemo(v.Having, params, arena),
			SubqueryFrom:  cloneStmtForMemo(v.SubqueryFrom, params, arena),
			Limit:         cloneExprForMemo(v.Limit, params, arena),
			Offset:        cloneExprForMemo(v.Offset, params, arena),
		})
	case *PS.CompoundStmt:
		left := cloneStmtForMemo(v.Left, params, arena)
		right := cloneStmtForMemo(v.Right, params, arena)
		return cloneAlloc(arena, &PS.CompoundStmt{
			Op:    v.Op,
			Left:  left,
			Right: right,
		})
	}
	return stmt // INSERT/UPDATE/DELETE — not parameterized
}

// NormalizeForMemo produces a parameterized version of stmt where
// literals in comparison predicates (WHERE, HAVING, ON) are replaced
// with PS.Param placeholders. The original literal values are returned
// in walk order. REQ001195.
//
// Use the parameterized stmt ONLY for memo key generation via
// SerializeKey(paramStmt). The original stmt is used for plan building.
func NormalizeForMemo(stmt PS.Stmt) (PS.Stmt, []any) {
	var params []any
	var arena cloneArena
	arena.init()
	cloned := cloneStmtForMemo(stmt, &params, &arena)
	return cloned, params
}
