package PL

import (
	"context"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// OperatorFactory builds operator trees. SQO uses it to construct
// new operator nodes (e.g. when a pass replaces a SeqScan with an
// IndexScan) without importing SQB/OP. The concrete implementation
// lives in SQB/OP (REQ001435) and is passed in via Context or
// directly into the Optimizer.
//
// All methods return Operator (the core interface in types.go),
// never a concrete type. This is the seam that lets SQO compose
// trees without depending on SQB.
//
// REQ001434: scaffold. No concrete implementation yet — see
// REQ001435 for SQB/OP/factory.go. No callers yet.
type OperatorFactory interface {
	NewSeqScan(table string, schema []string) Operator
	NewIndexScan(table, index string, schema []string) Operator
	NewIndexOnlyScan(table, index string, schema []string) Operator
	NewFilter(child Operator, predicate interface{}) Operator
	NewProject(child Operator, cols []string, exprs []interface{}) Operator
	NewFilterProject(child Operator, predicate interface{}, cols []string, exprs []interface{}) Operator
	NewHashJoin(left, right Operator, leftKey, rightKey string, joinType JoinType) Operator
	NewNestedLoopJoin(left, right Operator, predicate interface{}, joinType JoinType) Operator
	NewAggregate(child Operator, groupCols []string, aggs []AggregateSpec) Operator
	NewHashAggregate(child Operator, groupCols []string, aggs []AggregateSpec) Operator
	NewSort(child Operator, orderBy []OrderSpec) Operator
	NewLimit(child Operator, limit, offset int64) Operator
	NewDistinct(child Operator) Operator
	NewSetOp(left, right Operator, op SetOpType) Operator
	NewValues(rows [][]interface{}) Operator
}

// JoinType enumerates the join variants a factory can build.
// Matches SQB/OP's enum surface so callers can use either name.
type JoinType int

const (
	InnerJoin JoinType = iota
	LeftJoin
	RightJoin
	FullJoin
	SemiJoin
	AntiJoin
)

// SetOpType enumerates compound set operations (UNION, INTERSECT, …).
type SetOpType int

const (
	UnionAllOp SetOpType = iota
	UnionOp
	IntersectOp
	ExceptOp
)

// AggregateSpec describes a single aggregate function in a
// NewAggregate / NewHashAggregate call. The concrete values are
// supplied at call time; PL owns only the shape.
type AggregateSpec struct {
	FuncName string
	Arg      string // column name, or "" for COUNT(*)
	Distinct bool
	Alias    string
}

// OrderSpec describes a single ORDER BY column in NewSort.
type OrderSpec struct {
	Col  string
	Desc bool
}

// Optional capability interfaces. Passes probe for these with
// type assertion: `if cp, ok := op.(pl.PredicateCarrier); ok { … }`.
// Operators that satisfy an interface expose the corresponding
// capability; those that don't simply fall through to the default
// behavior in the pass.
//
// REQ001434: scaffold. Implementations land in REQ001435.
//
// Parent — operator with a single child. Children() and
// SetChild() let passes rewrite subtrees (e.g. push predicate
// into a scan).
type Parent interface {
	Child() Operator
	SetChild(op Operator)
}

// Children2 — operator with two children (e.g. joins, set ops).
// Distinct from Parent so a pass that expects exactly one child
// fails fast on a two-child operator.
type Children2 interface {
	Left() Operator
	SetLeft(op Operator)
	Right() Operator
	SetRight(op Operator)
}

// ColPrunable — scan that can limit which columns it reads from
// the underlying store. Used by column_pruning pass.
type ColPrunable interface {
	UsedCols() []string
	SetUsedCols(cols []string)
}

// PredicateCarrier — operator that holds a WHERE-style predicate
// and accepts a new one. Used by predicate_pushdown pass.
type PredicateCarrier interface {
	Predicate() interface{}
	SetPredicate(p interface{})
	HasPredicate() bool
}

// RelationSource — operator that reads from a named table.
// Used by index_selection pass to look up indexes.
type RelationSource interface {
	Table() string
}

// IndexInfo — operator that reads from a secondary index.
// Used by index_selection pass to recognize the index chosen.
type IndexInfo interface {
	IndexName() string
	IndexColumns() []string
	IndexTableID() uint64
}

// AggregateInfo — operator that performs GROUP BY aggregation.
// Used by column_pruning / aggregate fusion passes. GroupCols
// returns []PS.Expr to match the AG-side storage shape; passes
// that need column names project via SQF/PS helper functions.
type AggregateInfo interface {
	GroupCols() []PS.Expr
	Aggregates() []AggregateSpec
}

// SortInfo — operator that orders its output.
type SortInfo interface {
	OrderBy() []OrderSpec
}

// LimitInfo — operator that caps output rows. Used by limit
// pushdown to detect TopN opportunities.
type LimitInfo interface {
	Limit() int64
	Offset() int64
	IsTopN() bool
	SetTopN(b bool)
}

// ColumnSchema — minimal column information for a node. Used
// by the resolve_slots / pass chain to know what columns a
// node produces.
type ColumnSchema interface {
	Columns() []string
	ColumnIndex(name string) int
}

// ProjectInfo — operator that projects output expressions.
// Used by FilterProjectFusion to extract columns and exprs.
type ProjectInfo interface {
	Cols() []PS.Expr
}

// Compile-time checks: the optional interfaces are empty
// placeholders until REQ001435. These constants are unused
// until then; they exist so the types are part of the PL
// surface and grep finds them.
var (
	_ Parent         = (Parent)(nil)
	_ Children2      = (Children2)(nil)
	_ ColPrunable    = (ColPrunable)(nil)
	_ PredicateCarrier = (PredicateCarrier)(nil)
	_ RelationSource = (RelationSource)(nil)
	_ IndexInfo      = (IndexInfo)(nil)
	_ AggregateInfo  = (AggregateInfo)(nil)
	_ SortInfo       = (SortInfo)(nil)
	_ LimitInfo      = (LimitInfo)(nil)
	_ ColumnSchema   = (ColumnSchema)(nil)
	_ ProjectInfo    = (ProjectInfo)(nil)
)

// Ensure unused-import suppression if all these stubs are
// dropped in a future commit.
var _ = context.Background
