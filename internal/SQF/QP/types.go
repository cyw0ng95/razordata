// Package QP defines the unified logical+physical plan representation.
// PlanNode is a registered extension point — adding a new operator type
// requires one registry row, not edits to every switch on the type.
package QP

import (
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// NodeType identifies the operation performed by a PlanNode.
type NodeType uint8

const (
	NodeSeqScan NodeType = iota
	NodeIndexScan
	NodeFilter
	NodeProject
	NodeSort
	NodeLimit
	NodeOffset
	NodeDistinct
	NodeHashJoin
	NodeHashCrossJoin
	NodeNestedLoopJoin
	NodeAggregate
	NodeWindow
	NodeCompoundOp
	NodeValues
	NodeConstRow
	NodeFusedScan
	NodeBitmapHeapScan
	NodeMergeJoin
	NodeFilterProject
	NodeValuesRows
	NodeInsert
	NodeUpdate
	NodeDelete
	NodeDML // catch-all for DML wrappers
	NodeSource // catch-all for native sources (DDL, admin, etc.)
	NodeUnknown
)

// String returns a human-readable name for the node type.
func (t NodeType) String() string {
	switch t {
	case NodeSeqScan:
		return "SeqScan"
	case NodeIndexScan:
		return "IndexScan"
	case NodeFilter:
		return "Filter"
	case NodeProject:
		return "Project"
	case NodeSort:
		return "Sort"
	case NodeLimit:
		return "Limit"
	case NodeOffset:
		return "Offset"
	case NodeDistinct:
		return "Distinct"
	case NodeHashJoin:
		return "HashJoin"
	case NodeHashCrossJoin:
		return "HashCrossJoin"
	case NodeNestedLoopJoin:
		return "NestedLoopJoin"
	case NodeAggregate:
		return "Aggregate"
	case NodeWindow:
		return "Window"
	case NodeCompoundOp:
		return "CompoundOp"
	case NodeValues:
		return "Values"
	case NodeConstRow:
		return "ConstRow"
	case NodeFusedScan:
		return "FusedScan"
	case NodeBitmapHeapScan:
		return "BitmapHeapScan"
	case NodeMergeJoin:
		return "MergeJoin"
	case NodeFilterProject:
		return "FilterProject"
	case NodeInsert:
		return "Insert"
	case NodeUpdate:
		return "Update"
	case NodeDelete:
		return "Delete"
	case NodeDML:
		return "DML"
	case NodeSource:
		return "Source"
	default:
		return "Unknown"
	}
}

// NodeSchema carries the column metadata for a PlanNode's output.
type NodeSchema struct {
	Cols     []string
	ColTypes []LX.TokenType
}

// PlanNode is a single vertex in the QueryPlan DAG.
type PlanNode struct {
	Type        NodeType
	Schema      *NodeSchema // column names + types for this node's output
	Children    []int       // indices into QueryPlan.Nodes; empty for leaves
	Exprs       []PS.Expr   // expressions (predicates, projections, sort keys, agg funcs)
	TableName   string      // table name for scan nodes
	Cost        float64     // estimated cost (populated by cost passes)
	Cardinality float64     // estimated row count (populated by cost passes)

	// Per-node flags populated by decomposition:
	PushedPred bool // predicate was pushed to scan level
}

// QueryPlan is the unified plan representation — a directed acyclic graph
// of PlanNodes rooted at RootIdx.
type QueryPlan struct {
	Nodes   []PlanNode
	RootIdx int
	Stmt    PS.Stmt // original parsed statement
}

// NumNodes returns the number of nodes in the plan.
func (qp *QueryPlan) NumNodes() int { return len(qp.Nodes) }

// Node returns the node at the given index. Panics if out of range.
func (qp *QueryPlan) Node(idx int) *PlanNode { return &qp.Nodes[idx] }

// Child returns the child node at the given side (0 = first child, etc.).
func (qp *QueryPlan) Child(nodeIdx, side int) *PlanNode {
	children := qp.Nodes[nodeIdx].Children
	if side < 0 || side >= len(children) {
		return nil
	}
	return &qp.Nodes[children[side]]
}

// WalkDepthFirst calls fn on each node in depth-first post-order.
// fn receives a pointer so it can mutate the node in place.
// Returns false early if fn returns false.
func (qp *QueryPlan) WalkDepthFirst(fn func(*PlanNode) bool) {
	walkDF(&qp.Nodes[qp.RootIdx], fn, qp.Nodes[:])
}

func walkDF(node *PlanNode, fn func(*PlanNode) bool, allNodes []PlanNode) {
	for _, childIdx := range node.Children {
		walkDF(&allNodes[childIdx], fn, allNodes)
	}
	if !fn(node) {
		return
	}
}
