package QP

import (
	"fmt"
	"testing"

	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PX "github.com/cyw0ng95/razordata/internal/SQB/PX"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestShadowMode_StructurallyEquivalent verifies that BuildQueryPlan + Lower
// produces a PipelineSpec structurally identical to decomposePlan for all
// supported operator shapes. This is the shadow-mode assertion required by
// REQ002256 — both paths run in parallel; the QP path is a verification oracle.
func TestShadowMode_StructurallyEquivalent(t *testing.T) {
	cases := []struct {
		name string
		root OP.Operator
	}{
		{
			name: "SeqScanOnly",
			root: OP.NewSeqScan("t1"),
		},
		{
			name: "FilterSeqScan",
			root: func() OP.Operator {
				ss := OP.NewSeqScan("t1")
				pred := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}
				return OP.NewFilter(ss, pred, nil)
			}(),
		},
		{
			name: "ProjectFilterSeqScan",
			root: func() OP.Operator {
				ss := OP.NewSeqScan("t1")
				pred := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}
				filter := OP.NewFilter(ss, pred, nil)
				return OP.NewProject(filter, []PS.Expr{&PS.Ident{Name: "a"}, &PS.Ident{Name: "b"}})
			}(),
		},
		{
			name: "LimitOffset",
			root: func() OP.Operator {
				ss := OP.NewSeqScan("t1")
				limit := OP.NewLimit(ss, 10)
				return OP.NewOffset(limit, 5)
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Path A: decomposePlan (canonical, via exported PX test helper)
			stagesA, edgesA, rootA, _ := PX.DecomposePlan(tc.root, nil, nil)

			// Path B: BuildQueryPlan → Lower (shadow)
			qp := BuildQueryPlan(tc.root)
			specB, err := Lower(qp)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			stagesB := specB.Stages
			edgesB := specB.Edges
			rootB := specB.RootIdx

			// Structural equivalence assertions
			if len(stagesA) != len(stagesB) {
				t.Fatalf("stage count mismatch: decompose=%d, lower=%d", len(stagesA), len(stagesB))
			}
			if len(edgesA) != len(edgesB) {
				t.Fatalf("edge count mismatch: decompose=%d, lower=%d", len(edgesA), len(edgesB))
			}
			if rootA != rootB {
				t.Fatalf("root idx mismatch: decompose=%d, lower=%d", rootA, rootB)
			}
			for i := range stagesA {
				typeA := fmt.Sprintf("%T", stagesA[i])
				typeB := fmt.Sprintf("%T", stagesB[i])
				if typeA != typeB {
					t.Fatalf("stage[%d] type mismatch: decompose=%s, lower=%s", i, typeA, typeB)
				}
			}
		})
	}
}
