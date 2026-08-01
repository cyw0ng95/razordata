package DT

// OperatorFactory builds operator trees. SQO uses it to construct
// new operator nodes (e.g. when a pass replaces a SeqScan with an
// IndexScan) without importing SQB/OP. The concrete implementation
// lives in SQB/OP (REQ001435) and is passed in via Context or
// directly into the Optimizer.
//
// All methods return Operator (the core interface in types.go),
// never a concrete type. This is the seam that lets SQO compose
// trees without depending on SQB.
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
// supplied at call time; DT owns only the shape.
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

// Resettable is an optional interface an Operator can implement to
// support cursor state reuse without operator tree deallocation.
