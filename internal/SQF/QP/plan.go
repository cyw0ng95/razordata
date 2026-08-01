// Package QP defines the QueryPlan DAG — the intermediate plan representation
// that the planner builds from a parsed PS.Stmt and the PipelineExecutor
// (PX) lowers into executable pipeline stages.
//
// REQ002267 foundation: the QueryPlan DAG data model with full node-type
// coverage, including DML nodes (OpInsert/OpUpdate/OpDelete). The live engine
// currently runs on the legacy DT.Operator (OP.*) tree; this package is the
// target representation that later phases (PX consuming the DAG, pl.Operator
// removal) will route through. This step is additive: it does not change the
// execution path.
package QP

import (
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// NodeOp enumerates every plan-node shape. The DML ops (OpInsert/OpUpdate/
// OpDelete) are part of this enum so the same DAG can represent write
// pipelines, satisfying REQ002267.
type NodeOp int

const (
	// Scan / access paths.
	OpSeqScan NodeOp = iota
	OpIndexScan
	OpIndexOnlyScan
	OpFusedScan
	OpBitmapHeapScan

	// Relational operators.
	OpFilter
	OpProject
	OpFilterProject
	OpSort
	OpTopNSort
	OpLimit
	OpOffset
	OpHashJoin
	OpHashCrossJoin
	OpNestedLoopJoin
	OpMergeJoin
	OpDistinct
	OpCompound
	OpConstRow
	OpValues
	OpValuesRows

	// DML (REQ002267).
	OpInsert
	OpUpdate
	OpDelete
)

// String returns a stable, human-readable name for the node op.
func (o NodeOp) String() string {
	switch o {
	case OpSeqScan:
		return "SeqScan"
	case OpIndexScan:
		return "IndexScan"
	case OpIndexOnlyScan:
		return "IndexOnlyScan"
	case OpFusedScan:
		return "FusedScan"
	case OpBitmapHeapScan:
		return "BitmapHeapScan"
	case OpFilter:
		return "Filter"
	case OpProject:
		return "Project"
	case OpFilterProject:
		return "FilterProject"
	case OpSort:
		return "Sort"
	case OpTopNSort:
		return "TopNSort"
	case OpLimit:
		return "Limit"
	case OpOffset:
		return "Offset"
	case OpHashJoin:
		return "HashJoin"
	case OpHashCrossJoin:
		return "HashCrossJoin"
	case OpNestedLoopJoin:
		return "NestedLoopJoin"
	case OpMergeJoin:
		return "MergeJoin"
	case OpDistinct:
		return "Distinct"
	case OpCompound:
		return "Compound"
	case OpConstRow:
		return "ConstRow"
	case OpValues:
		return "Values"
	case OpValuesRows:
		return "ValuesRows"
	case OpInsert:
		return "Insert"
	case OpUpdate:
		return "Update"
	case OpDelete:
		return "Delete"
	default:
		return "Unknown"
	}
}

// PlanNode is a single node in the QueryPlan DAG. It is a fixed-size struct
// (no map[string]interface{}) so the data path stays allocation-light and the
// fields are self-describing. Not every field applies to every NodeOp; each
// constructor sets only the fields relevant to its op.
type PlanNode struct {
	// Op is the operator kind for this node.
	Op NodeOp

	// Children are the subtree roots feeding this node (left-to-right).
	// For joins, Children[0] is the outer/left side and Children[1] the
	// inner/right side. DML nodes carry their source scan as Children[0].
	Children []*PlanNode

	// OutputCols/OutputTypes describe the column names and LX token types
	// emitted by this node.
	OutputCols  []string
	OutputTypes []LX.TokenType

	// Scan fields.
	Table string // base table name for scan / DML target
	Alias string // relation alias (FROM alias, JOIN alias)

	// Projection / filter payload.
	RequestedCols []string  // columns requested from a scan
	Pred          PS.Expr   // filter predicate
	Exprs         []PS.Expr // projection / compute expressions

	// Join payload.
	On         PS.Expr // JOIN ON predicate
	JoinKind   int     // join kind discriminator (see PL join kinds)
	LeftAlias  string  // left side alias (joins)
	RightAlias string  // right side alias (joins)

	// Aggregation payload.
	GroupBy []PS.Expr
	Having  PS.Expr
	Distinct bool

	// DML payload.
	Cols      []string     // INSERT column list
	Set       []PS.Pair    // UPDATE assignments
	Values    [][]PS.Expr  // INSERT literal value rows
	Where     PS.Expr      // UPDATE/DELETE condition
	Returning []PS.Expr    // DML RETURNING list

	// Pagination payload (SELECT LIMIT/OFFSET, also UPDATE/DELETE).
	LimitExpr  PS.Expr
	OffsetExpr PS.Expr
}

// QueryPlan is the root of a single statement's plan DAG.
type QueryPlan struct {
	Root *PlanNode
}

// AddChild appends a child node and returns the parent so calls can be
// chained. It is safe to call with a nil child (no-op) so builders can write
// qp.AddChild(maybeChild) without an explicit nil check.
func (n *PlanNode) AddChild(c *PlanNode) *PlanNode {
	if c == nil {
		return n
	}
	n.Children = append(n.Children, c)
	return n
}
