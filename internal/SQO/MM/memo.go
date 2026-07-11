package MM

import (
	"container/list"
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

const DefaultMaxMemoEntries = 4096

const (
	tagSelect      = 1
	tagInsert      = 2
	tagUpdate      = 3
	tagDelete      = 4
	tagCreateTable = 5
	tagDropTable   = 6
	tagCompound    = 7

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

type Plan struct {
	Cost      float64
	MemoKey   string
	SchemaVer uint64
}

type lruNode struct {
	key  string
	plan Plan
}

type Memo struct {
	mu            sync.RWMutex
	schemaVersion uint64
	entries       map[string]*list.Element
	lru           *list.List
	maxEntries    int
}

func NewMemo(capacity int) *Memo {
	if capacity <= 0 {
		capacity = DefaultMaxMemoEntries
	}
	return &Memo{
		entries:    make(map[string]*list.Element),
		lru:        list.New(),
		maxEntries: capacity,
	}
}

func NewMemoWithCapacity(maxEntries int) *Memo {
	return NewMemo(maxEntries)
}

func (m *Memo) SchemaVersion() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.schemaVersion
}

func (m *Memo) BumpSchemaVersion() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schemaVersion++
	return m.schemaVersion
}

func (m *Memo) Get(key string) (*Plan, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.entries[key]
	if !ok {
		return nil, false
	}
	m.lru.MoveToFront(el)
	p := el.Value.(*lruNode).plan
	return &p, true
}

func (m *Memo) Put(key string, p *Plan) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.entries[key]; ok {
		el.Value.(*lruNode).plan = *p
		m.lru.MoveToFront(el)
		return
	}
	el := m.lru.PushFront(&lruNode{key: key, plan: *p})
	m.entries[key] = el
	if m.lru.Len() > m.maxEntries {
		m.evictOldestLocked()
	}
}

func (m *Memo) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

func (m *Memo) MaxEntries() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxEntries
}

func (m *Memo) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]*list.Element)
	m.lru.Init()
}

func (m *Memo) Invalidate(schemaVersion uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, el := range m.entries {
		if el.Value.(*lruNode).plan.SchemaVer < schemaVersion {
			delete(m.entries, k)
			m.lru.Remove(el)
		}
	}
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
	e.writeUvarint(uint64(op))
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

type xxhash64 struct {
	v uint64
}

const (
	xxhashSeed uint64 = 0x9E3779B97F4A7C15
	xxhashMul  uint64 = 0x100000001B3
)

func newXXHash64() *xxhash64 {
	return &xxhash64{v: xxhashSeed}
}

func (h *xxhash64) Write(b []byte) {
	for _, c := range b {
		h.v ^= uint64(c)
		h.v *= xxhashMul
	}
}

func (h *xxhash64) WriteUvarint(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	h.Write(tmp[:n])
}

func (h *xxhash64) Sum64() uint64 {
	x := h.v
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

func SerializeKey(stmt PS.Stmt, schemaVersion uint64) string {
	e := &enc{}
	e.writeStmt(stmt)
	return serializeKeyBytes(e.buf, schemaVersion)
}

func serializeKeyBytes(buf []byte, schemaVersion uint64) string {
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

var defaultSchemaVersion atomic.Uint64

var globalLearned = NewLearnedModel()

func Learned() *LearnedModel { return globalLearned }

func BumpDefaultSchemaVersion() uint64 {
	return defaultSchemaVersion.Add(1)
}

func DefaultSchemaVersion() uint64 {
	return defaultSchemaVersion.Load()
}

type LearnedModel struct {
	mu            sync.RWMutex
	trained       bool
	trainingCount int
	correlations  map[pairKey]float64
}

type pairKey struct {
	table  string
	c1, c2 string
}

func NewLearnedModel() *LearnedModel {
	return &LearnedModel{
		correlations: make(map[pairKey]float64),
	}
}

func (lm *LearnedModel) IsTrained() bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.trained
}

func (lm *LearnedModel) TrainingCount() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.trainingCount
}

func (lm *LearnedModel) PredicateFeatures(
	predicateType int,
	histogramSelectivity, rowCount, distinctCount, nullCount float64,
) [6]float64 {
	logRow := math.Log(rowCount + 1)
	if logRow > 20 {
		logRow = 20
	}
	distinctFrac := 1.0
	if rowCount > 0 {
		distinctFrac = distinctCount / rowCount
	}
	nullFrac := 0.0
	if rowCount > 0 {
		nullFrac = nullCount / rowCount
	}
	return [6]float64{
		float64(predicateType) / 4.0,
		histogramSelectivity,
		logRow / 20.0,
		distinctFrac,
		nullFrac,
		0.5,
	}
}

func (lm *LearnedModel) Predict(features [6]float64) float64 {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	if !lm.trained {
		return features[1]
	}
	return features[1]
}

func (lm *LearnedModel) Record(predSig string, actual, estimated int64) {}

func (lm *LearnedModel) Apply(predSig string) float64 {
	return 1.0
}

func (lm *LearnedModel) GetCorrelation(table, c1, c2 string) float64 {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.correlations[pairKey{table: table, c1: c1, c2: c2}]
}

func (lm *LearnedModel) SetCorrelation(table, c1, c2 string, val float64) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if val < -1 {
		val = -1
	}
	if val > 1 {
		val = 1
	}
	lm.correlations[pairKey{table: table, c1: c1, c2: c2}] = val
	lm.correlations[pairKey{table: table, c1: c2, c2: c1}] = val
	lm.trained = true
}

func (lm *LearnedModel) AdjustMultiPredicate(baseSelectivity float64, correlations []float64) float64 {
	if len(correlations) == 0 {
		return baseSelectivity
	}
	avgCorr := 0.0
	for _, c := range correlations {
		avgCorr += math.Abs(c)
	}
	avgCorr /= float64(len(correlations))
	factor := 1.0 + avgCorr
	adjusted := baseSelectivity * factor
	if adjusted > 1.0 {
		adjusted = 1.0
	}
	return adjusted
}

func (lm *LearnedModel) BootstrapFromHistograms(
	buckets []struct {
		Count         int64
		Lower, Upper  []byte
		TotalRows     int64
		DistinctCount int64
		NullCount     int64
	},
) {
	if len(buckets) < 4 {
		return
	}
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.trained = true
	lm.trainingCount += len(buckets)
}

func (lm *LearnedModel) MultiPredicateFeatures(
	predicates []struct {
		Table, Col string
		HistSel    float64
		RowCount   float64
		DistCount  float64
		NullCount  float64
	},
) [6]float64 {
	baseSel := 1.0
	for _, p := range predicates {
		baseSel *= p.HistSel
	}
	if baseSel < 0 {
		baseSel = 0
	}
	totalRows := 0.0
	if len(predicates) > 0 {
		totalRows = predicates[0].RowCount
	}
	return [6]float64{
		float64(len(predicates)) / 10.0,
		baseSel,
		math.Log(totalRows+1) / 20.0,
		0.5,
		0,
		0.5,
	}
}

func isComparisonOp(op LX.TokenType) bool {
	switch op {
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE:
		return true
	}
	return false
}

func isColumnRef(expr PS.Expr) bool {
	switch expr.(type) {
	case *PS.Ident, *PS.QualifiedName:
		return true
	}
	return false
}

func isLiteral(expr PS.Expr) bool {
	switch expr.(type) {
	case *PS.NumberLiteral, *PS.FloatLiteral, *PS.StringLiteral, *PS.BoolLiteral:
		return true
	}
	return false
}

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

func cloneExprForMemo(expr PS.Expr, params *[]any) PS.Expr {
	if expr == nil {
		return nil
	}
	switch v := expr.(type) {
	case *PS.BinaryExpr:
		if isComparisonOp(v.Op) {
			if isColumnRef(v.Left) && isLiteral(v.Right) {
				idx := len(*params)
				*params = append(*params, literalToAny(v.Right))
				return &PS.BinaryExpr{
					Op:    v.Op,
					Left:  v.Left,
					Right: &PS.Param{Index: idx},
				}
			}
			if isLiteral(v.Left) && isColumnRef(v.Right) {
				idx := len(*params)
				*params = append(*params, literalToAny(v.Left))
				return &PS.BinaryExpr{
					Op:    v.Op,
					Left:  &PS.Param{Index: idx},
					Right: v.Right,
				}
			}
		}
		return &PS.BinaryExpr{
			Op:    v.Op,
			Left:  cloneExprForMemo(v.Left, params),
			Right: cloneExprForMemo(v.Right, params),
		}
	case *PS.UnaryExpr:
		return &PS.UnaryExpr{
			Op:      v.Op,
			Operand: cloneExprForMemo(v.Operand, params),
		}
	case *PS.InExpr:
		items := make([]PS.Expr, len(v.List))
		for i, item := range v.List {
			items[i] = cloneExprForMemo(item, params)
		}
		return &PS.InExpr{
			Expr:     cloneExprForMemo(v.Expr, params),
			List:     items,
			Subquery: v.Subquery,
		}
	case *PS.BetweenExpr:
		return &PS.BetweenExpr{
			Expr: cloneExprForMemo(v.Expr, params),
			Low:  cloneExprForMemo(v.Low, params),
			High: cloneExprForMemo(v.High, params),
		}
	case *PS.FunctionCall:
		args := make([]PS.Expr, len(v.Args))
		for i, a := range v.Args {
			args[i] = cloneExprForMemo(a, params)
		}
		return &PS.FunctionCall{Name: v.Name, Args: args}
	case *PS.CaseExpr:
		whenList := make([]PS.WhenClause, len(v.WhenList))
		for i, w := range v.WhenList {
			whenList[i] = PS.WhenClause{
				Cond: cloneExprForMemo(w.Cond, params),
				Then: cloneExprForMemo(w.Then, params),
			}
		}
		return &PS.CaseExpr{
			Expr:     cloneExprForMemo(v.Expr, params),
			WhenList: whenList,
			Else:     cloneExprForMemo(v.Else, params),
		}
	case *PS.CastExpr:
		return &PS.CastExpr{
			Expr: cloneExprForMemo(v.Expr, params),
			Type: v.Type,
		}
	case *PS.ListExpr:
		items := make([]PS.Expr, len(v.Items))
		for i, item := range v.Items {
			items[i] = cloneExprForMemo(item, params)
		}
		return &PS.ListExpr{Items: items}
	case *PS.AliasedExpr:
		return &PS.AliasedExpr{
			Expr:  cloneExprForMemo(v.Expr, params),
			Alias: v.Alias,
		}
	case *PS.SubqueryExpr:
		return v
	case *PS.ExistsExpr:
		return v
	}
	return expr
}

func cloneStmtForMemo(stmt PS.Stmt, params *[]any) PS.Stmt {
	if stmt == nil {
		return nil
	}
	switch v := stmt.(type) {
	case *PS.Select:
		cols := make([]PS.Expr, len(v.Cols))
		for i, c := range v.Cols {
			cols[i] = cloneExprForMemo(c, params)
		}
		joins := make([]PS.JoinClause, len(v.Joins))
		for i, j := range v.Joins {
			joins[i] = PS.JoinClause{
				Kind:  j.Kind,
				Right: j.Right,
				On:    cloneExprForMemo(j.On, params),
			}
		}
		groupBy := make([]PS.Expr, len(v.GroupBy))
		for i, g := range v.GroupBy {
			groupBy[i] = cloneExprForMemo(g, params)
		}
		return &PS.Select{
			Distinct:      v.Distinct,
			Cols:          cols,
			From:          v.From,
			FromAlias:     v.FromAlias,
			Where:         cloneExprForMemo(v.Where, params),
			OrderBy:       v.OrderBy,
			Joins:         joins,
			GroupBy:       groupBy,
			Having:        cloneExprForMemo(v.Having, params),
			SubqueryFrom:  cloneStmtForMemo(v.SubqueryFrom, params),
			Limit:         cloneExprForMemo(v.Limit, params),
			Offset:        cloneExprForMemo(v.Offset, params),
		}
	case *PS.CompoundStmt:
		left := cloneStmtForMemo(v.Left, params)
		right := cloneStmtForMemo(v.Right, params)
		return &PS.CompoundStmt{
			Op:    v.Op,
			Left:  left,
			Right: right,
		}
	}
	return stmt
}

func NormalizeForMemo(stmt PS.Stmt) (PS.Stmt, []any) {
	var params []any
	cloned := cloneStmtForMemo(stmt, &params)
	return cloned, params
}