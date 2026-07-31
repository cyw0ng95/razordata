package QP

import (
	"testing"

	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Helper: build a simple Scan→Filter→Project tree.
func buildTestTree() *OP.Project {
	ss := OP.NewSeqScan("t1")
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 1},
	}
	filter := OP.NewFilter(ss, pred, nil)
	return OP.NewProject(filter, []PS.Expr{
		&PS.Ident{Name: "a"},
		&PS.Ident{Name: "b"},
	})
}

func TestBuildQueryPlan_SeqScanOnly(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	qp := BuildQueryPlan(ss)
	if qp == nil {
		t.Fatal("expected non-nil QueryPlan")
	}
	if qp.RootIdx != 0 {
		t.Fatalf("expected root 0, got %d", qp.RootIdx)
	}
	if qp.NumNodes() != 1 {
		t.Fatalf("expected 1 node, got %d", qp.NumNodes())
	}
	root := qp.Node(0)
	if root.Type != NodeSeqScan {
		t.Fatalf("expected NodeSeqScan, got %v", root.Type)
	}
}

func TestBuildQueryPlan_FilterProjectTree(t *testing.T) {
	tree := buildTestTree()
	qp := BuildQueryPlan(tree)
	if qp == nil {
		t.Fatal("expected non-nil QueryPlan")
	}
	if qp.NumNodes() != 3 {
		t.Fatalf("expected 3 nodes, got %d", qp.NumNodes())
	}
	// Root should be Project
	root := qp.Node(qp.RootIdx)
	if root.Type != NodeProject {
		t.Fatalf("expected root NodeProject, got %v", root.Type)
	}
	// Project → Filter → SeqScan chain
	if len(root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(root.Children))
	}
	filterNode := qp.Node(root.Children[0])
	if filterNode.Type != NodeFilter {
		t.Fatalf("expected NodeFilter, got %v", filterNode.Type)
	}
	if len(filterNode.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(filterNode.Children))
	}
	scanNode := qp.Node(filterNode.Children[0])
	if scanNode.Type != NodeSeqScan {
		t.Fatalf("expected NodeSeqScan, got %v", scanNode.Type)
	}
	// Predicate stored in Filter node
	if len(filterNode.Exprs) != 1 {
		t.Fatalf("expected 1 expr in filter, got %d", len(filterNode.Exprs))
	}
}

func TestLower_SeqScanOnly(t *testing.T) {
	ss := OP.NewSeqScan("t1")
	qp := BuildQueryPlan(ss)
	spec, err := Lower(qp)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if len(spec.Stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(spec.Stages))
	}
	if _, ok := spec.Stages[0].(*PX.ScanStageSpec); !ok {
		t.Fatalf("expected *ScanStageSpec, got %T", spec.Stages[0])
	}
	if spec.RootIdx != 0 {
		t.Fatalf("expected root 0, got %d", spec.RootIdx)
	}
}

func TestLower_FilterProjectTree(t *testing.T) {
	tree := buildTestTree()
	qp := BuildQueryPlan(tree)
	spec, err := Lower(qp)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if len(spec.Stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(spec.Stages))
	}
	if len(spec.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(spec.Edges))
	}
	if spec.RootIdx != 2 {
		t.Fatalf("expected root 2, got %d", spec.RootIdx)
	}
	// Bottom-up: stage 0 = scan, 1 = filter, 2 = project
	if _, ok := spec.Stages[0].(*PX.ScanStageSpec); !ok {
		t.Fatalf("stage 0: expected *ScanStageSpec, got %T", spec.Stages[0])
	}
	if _, ok := spec.Stages[1].(*PX.FilterStageSpec); !ok {
		t.Fatalf("stage 1: expected *FilterStageSpec, got %T", spec.Stages[1])
	}
	if _, ok := spec.Stages[2].(*PX.ProjectStageSpec); !ok {
		t.Fatalf("stage 2: expected *ProjectStageSpec, got %T", spec.Stages[2])
	}
}

func TestLower_ProducesValidPipelineSpec(t *testing.T) {
	tree := buildTestTree()
	qp := BuildQueryPlan(tree)
	spec, err := Lower(qp)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	// Verify NewRuntime succeeds (stages are valid)
	_, err = spec.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
}
