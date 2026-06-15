package codegen

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	"github.com/cyw0ng95/razordata/internal/SQL/EX"
)

type OpType uint8

const (
	OpUnknown OpType = iota
	OpSeqScan
	OpIndexScan
	OpFilter
	OpProject
	OpSort
	OpLimit
	OpNestedLoopJoin
	OpHashJoin
	OpHashAggregate
	OpAggregate
	OpDistinct
	OpCompound
	OpWindow
	OpExplain
	OpCreateView
)

type ExprShape uint8

const (
	ExprUnknown ExprShape = iota
	ExprColEqLit
	ExprColNeLit
	ExprColLtLit
	ExprColGtLit
	ExprColLeLit
	ExprColGeLit
	ExprColEqCol
	ExprColNeCol
	ExprAnd
	ExprOr
	ExprNot
)

type CallSiteSig struct {
	Op         OpType
	ChildOp    OpType
	Expr       ExprShape
	ColTypes   [4]LX.TokenType
	NumCols    uint8
	HasPred    bool
	HasOrderBy bool
	HasGroupBy bool
}

type CompiledFn func(ctx context.Context, batch *EX.Batch, params []any) (*EX.Batch, error)

type icacheEntry struct {
	fn  CompiledFn
	use atomic.Int64
}

type InlineCache struct {
	mu    sync.RWMutex
	m     map[CallSiteSig]*icacheEntry
	limit int
}

func NewInlineCache(limit int) *InlineCache {
	if limit <= 0 {
		limit = 256
	}
	return &InlineCache{
		m:     make(map[CallSiteSig]*icacheEntry),
		limit: limit,
	}
}

func (c *InlineCache) Get(sig CallSiteSig) CompiledFn {
	c.mu.RLock()
	entry, ok := c.m[sig]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	entry.use.Add(1)
	return entry.fn
}

func (c *InlineCache) Put(sig CallSiteSig, fn CompiledFn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.limit {
		var minSig CallSiteSig
		var minUse int64 = 1 << 62
		for s, e := range c.m {
			if u := e.use.Load(); u < minUse {
				minUse = u
				minSig = s
			}
		}
		delete(c.m, minSig)
	}
	c.m[sig] = &icacheEntry{fn: fn}
}

// Len returns the number of cached entries.
func (c *InlineCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}

func OpTypeFromString(s string) OpType {
	switch s {
	case "SeqScan":
		return OpSeqScan
	case "IndexScan":
		return OpIndexScan
	case "Filter":
		return OpFilter
	case "Project":
		return OpProject
	case "Sort":
		return OpSort
	case "Limit":
		return OpLimit
	case "NestedLoopJoin":
		return OpNestedLoopJoin
	case "HashJoin":
		return OpHashJoin
	case "HashAggregate":
		return OpHashAggregate
	case "Aggregate":
		return OpAggregate
	case "Distinct":
		return OpDistinct
	case "CompoundOp":
		return OpCompound
	case "WindowOperator":
		return OpWindow
	case "ExplainStmtOp":
		return OpExplain
	case "CreateViewOperator":
		return OpCreateView
	default:
		return OpUnknown
	}
}

func ExprShapeFromPS(expr PS.Expr) ExprShape {
	if expr == nil {
		return ExprUnknown
	}
	b, ok := expr.(*PS.BinaryExpr)
	if !ok {
		return ExprUnknown
	}
	_, leftIsCol := b.Left.(*PS.Ident)
	_, leftIsQ := b.Left.(*PS.QualifiedName)
	isLeftCol := leftIsCol || leftIsQ
	_, rightIsLit := b.Right.(*PS.NumberLiteral)
	_, rightIsF := b.Right.(*PS.FloatLiteral)
	_, rightIsS := b.Right.(*PS.StringLiteral)
	isRightLit := rightIsLit || rightIsF || rightIsS
	_, rightIsCol := b.Right.(*PS.Ident)
	_, rightIsQ2 := b.Right.(*PS.QualifiedName)
	isRightCol := rightIsCol || rightIsQ2

	switch {
	case isLeftCol && isRightLit:
		switch b.Op {
		case int(LX.T_EQ):
			return ExprColEqLit
		case int(LX.T_NE):
			return ExprColNeLit
		case int(LX.T_LT):
			return ExprColLtLit
		case int(LX.T_GT):
			return ExprColGtLit
		case int(LX.T_LE):
			return ExprColLeLit
		case int(LX.T_GE):
			return ExprColGeLit
		}
	case isLeftCol && isRightCol:
		switch b.Op {
		case int(LX.T_EQ):
			return ExprColEqCol
		case int(LX.T_NE):
			return ExprColNeCol
		}
	}
	return ExprUnknown
}