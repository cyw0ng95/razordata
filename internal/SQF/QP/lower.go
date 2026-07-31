package QP

import (
	"context"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Lower converts a QueryPlan DAG back into a PipelineSpec.
// This is a mechanical 1:1 mapping — no fusion, no pattern matching,
// no optimization — just switching on NodeType. REQ002255.
func Lower(qp *QueryPlan) (*PX.PipelineSpec, error) {
	if qp == nil || qp.RootIdx < 0 || qp.RootIdx >= len(qp.Nodes) {
		return nil, fmt.Errorf("qp: invalid plan root idx %d", qp.RootIdx)
	}
	l := &lowerer{spec: qp}
	stageIdx := l.lowerNode(qp.RootIdx)
	if stageIdx < 0 {
		return nil, fmt.Errorf("qp: failed to lower root node %d", qp.RootIdx)
	}

	var outputCols []string
	var outputTypes []LX.TokenType
	if sch := qp.Nodes[qp.RootIdx].Schema; sch != nil {
		outputCols = sch.Cols
		outputTypes = sch.ColTypes
	}

	return &PX.PipelineSpec{
		Stages:      l.stages,
		Edges:       l.edges,
		RootIdx:     stageIdx,
		OutputCols:  outputCols,
		OutputTypes: outputTypes,
	}, nil
}

type lowerer struct {
	spec   *QueryPlan
	stages []PX.StageSpec
	edges  []PX.EdgeSpec
}

func (l *lowerer) lowerNode(idx int) int {
	node := &l.spec.Nodes[idx]
	switch node.Type {
	case NodeSeqScan, NodeIndexScan, NodeFusedScan, NodeBitmapHeapScan:
		return l.lowerSource(node)
	case NodeConstRow, NodeValues, NodeValuesRows, NodeMergeJoin, NodeFilterProject:
		return l.lowerSource(node)
	case NodeSource:
		return l.lowerSource(node)
	case NodeFilter:
		return l.lowerFilter(node)
	case NodeProject:
		return l.lowerProject(node)
	case NodeSort:
		return l.lowerSort(node)
	case NodeLimit:
		return l.lowerLimit(node)
	case NodeOffset:
		return l.lowerOffset(node)
	case NodeDistinct:
		return l.lowerDistinct(node)
	case NodeHashJoin, NodeHashCrossJoin:
		return l.lowerHashJoin(node)
	case NodeNestedLoopJoin:
		return l.lowerNestedLoopJoin(node)
	case NodeAggregate:
		return l.lowerAggregate(node)
	case NodeWindow:
		return l.lowerWindow(node)
	case NodeCompoundOp:
		return l.lowerCompound(node)
	case NodeInsert, NodeUpdate, NodeDelete, NodeDML:
		return l.lowerDML(node)
	default:
		return -1
	}
}

func (l *lowerer) addStage(spec PX.StageSpec, schema *NodeSchema) int {
	idx := len(l.stages)
	l.stages = append(l.stages, spec)
	_ = schema
	return idx
}

func (l *lowerer) addEdge(from, to int, side PX.ChildSide) {
	l.edges = append(l.edges, PX.EdgeSpec{From: from, To: to, Side: side})
}

// --- Source nodes ---

func (l *lowerer) lowerSource(node *PlanNode) int {
	return l.addStage(&PX.ScanStageSpec{
		NewProducer: func() UT.BatchProducer {
			return PX.NewRowOperatorAsProducer(newDummyOp(node))
		},
	}, node.Schema)
}

// --- Transform nodes ---

func (l *lowerer) lowerFilter(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	var pred PS.Expr
	if len(node.Exprs) > 0 {
		pred = node.Exprs[0]
	}
	return l.addStage(&PX.FilterStageSpec{Pred: pred}, node.Schema)
}

func (l *lowerer) lowerProject(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	return l.addStage(&PX.ProjectStageSpec{Exprs: node.Exprs, Names: node.Schema.Cols}, node.Schema)
}

func (l *lowerer) lowerSort(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	return l.addStage(&PX.SortStageSpec{}, node.Schema)
}

func (l *lowerer) lowerLimit(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	return l.addStage(&PX.LimitStageSpec{}, node.Schema)
}

func (l *lowerer) lowerOffset(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	return l.addStage(&PX.OffsetStageSpec{}, node.Schema)
}

func (l *lowerer) lowerDistinct(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	keyCols := make([]int, len(node.Schema.Cols))
	for i := range keyCols {
		keyCols[i] = i
	}
	return l.addStage(&PX.DistinctStageSpec{KeyCols: keyCols}, node.Schema)
}

func (l *lowerer) lowerHashJoin(node *PlanNode) int {
	leftIdx := l.lowerNode(node.Children[0])
	rightIdx := l.lowerNode(node.Children[1])
	l.addEdge(len(l.stages), leftIdx, PX.LeftChild)
	l.addEdge(len(l.stages), rightIdx, PX.RightChild)
	return l.addStage(&PX.HashJoinStageSpec{}, node.Schema)
}

func (l *lowerer) lowerNestedLoopJoin(node *PlanNode) int {
	leftIdx := l.lowerNode(node.Children[0])
	rightIdx := l.lowerNode(node.Children[1])
	l.addEdge(len(l.stages), leftIdx, PX.LeftChild)
	l.addEdge(len(l.stages), rightIdx, PX.RightChild)
	return l.addStage(&PX.ScanStageSpec{
		NewProducer: func() UT.BatchProducer {
			return PX.NewRowOperatorAsProducer(newDummyOp(node))
		},
	}, node.Schema)
}

func (l *lowerer) lowerAggregate(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	return l.addStage(&PX.AggregateStageSpec{}, node.Schema)
}

func (l *lowerer) lowerWindow(node *PlanNode) int {
	childIdx := l.lowerNode(node.Children[0])
	l.addEdge(len(l.stages), childIdx, PX.SingleChild)
	return l.addStage(&PX.WindowStageSpec{}, node.Schema)
}

func (l *lowerer) lowerCompound(node *PlanNode) int {
	leftIdx := l.lowerNode(node.Children[0])
	rightIdx := l.lowerNode(node.Children[1])
	l.addEdge(len(l.stages), leftIdx, PX.LeftChild)
	l.addEdge(len(l.stages), rightIdx, PX.RightChild)
	return l.addStage(&PX.CompoundStageSpec{}, node.Schema)
}

func (l *lowerer) lowerDML(node *PlanNode) int {
	return l.addStage(&PX.ScanStageSpec{
		NewProducer: func() UT.BatchProducer {
			return PX.NewRowOperatorAsProducer(newDummyOp(node))
		},
	}, node.Schema)
}

// newDummyOp creates a minimal DT.Operator wrapper around plan node metadata
// sufficient for RowOperatorAsProducer to emit schema-correct batches.
func newDummyOp(node *PlanNode) DT.Operator {
	return &dummyOp{schema: node.Schema}
}

type dummyOp struct {
	schema *NodeSchema
	done   bool
}

func (d *dummyOp) Next(ctx context.Context) (DT.Row, error) {
	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true
	if d.schema == nil {
		return DT.Row{}, nil
	}
	data := make([]DT.Value, len(d.schema.Cols))
	types := make([]LX.TokenType, len(d.schema.Cols))
	copy(types, d.schema.ColTypes)
	return DT.Row{Cols: d.schema.Cols, Types: types, Data: data}, nil
}

func (d *dummyOp) Close() error { return nil }
