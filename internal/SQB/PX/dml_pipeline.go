package PX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
)

// BuildDMLPipelineSpec creates a PipelineSpec for a DML writer operator
// (WT.Insert/Update/Delete). For other operator types (DDL, etc.) it
// falls back to LegacyBatchStageSpec so the pipeline can still execute
// the operator without type-specific stage wrapping. REQ002142.
func BuildDMLPipelineSpec(op DT.Operator) (*PipelineSpec, error) {
	switch o := op.(type) {
	case *WT.Insert:
		return &PipelineSpec{
			Stages:  []StageSpec{&InsertStageSpec{Insert: o}},
			RootIdx: 0,
		}, nil
	case *WT.Update:
		return &PipelineSpec{
			Stages:  []StageSpec{&UpdateStageSpec{Update: o}},
			RootIdx: 0,
		}, nil
	case *WT.Delete:
		return &PipelineSpec{
			Stages:  []StageSpec{&DeleteStageSpec{Delete: o}},
			RootIdx: 0,
		}, nil
	default:
		// Fallback: wrap the operator in LegacyBatchStageSpec. This
		// handles DDL (CreateTable/DropTable/CreateIndex/etc.) and any
		// other WT operators that don't have dedicated DML stages.
		return &PipelineSpec{
			Stages: []StageSpec{
				&LegacyBatchStageSpec{
					Root: op,
					Specialize: func(root DT.Operator, _ pl.QueryPlanner) UT.BatchProducer {
						if bp, ok := root.(UT.BatchProducer); ok {
							return bp
						}
						return UT.NewBatchToRowAdapter(NewRowOperatorAsProducer(root))
					},
				},
			},
			RootIdx: 0,
		}, nil
	}
}