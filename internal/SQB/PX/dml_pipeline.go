package PX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
)

// BuildDMLPipelineSpec creates a PipelineSpec for a DML writer operator
// (WT.Insert/Update/Delete) or any DDL/admin writer (CreateTable, DropTable,
// CreateIndex, DropIndex, Trigger, Pragma, etc.). REQ002142.
//
// For Insert/Update/Delete, it emits the dedicated InsertStageSpec/
// UpdateStageSpec/DeleteStageSpec.
//
// For all other operators (DDL/admin), it emits a ScanStageSpec wrapping
// the operator in a RowOperatorAsProducer — the same path used by
// decomposePlan for DDL operators (REQ002201-002208). REQ002217: this
// removes the LegacyBatchStageSpec fallback that previously wrapped DDL
// operators in a full-pipeline SpecializeFunc call.
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
		// REQ002217: DDL/admin operators (CreateTable/DropTable/
		// CreateIndex/DropIndex/Trigger/Pragma/Attach/Detach/View/
		// MatView/AlterTable/Explain/Truncate/Reindex/UnsupportedOp).
		// Emit a native ScanStageSpec wrapping the operator as a
		// BatchProducer. RowOperatorAsProducer.NextBatch calls op.Next
		// once and returns a single-row batch, then EOF on the next
		// call — matching LegacyBatchStageSpec's row-by-row behavior
		// without the SpecializeFunc overhead.
		return &PipelineSpec{
			Stages: []StageSpec{
				&ScanStageSpec{
					NewProducer: func() UT.BatchProducer {
						if bp, ok := op.(UT.BatchProducer); ok {
							return bp
						}
						return NewRowOperatorAsProducer(op)
					},
				},
			},
			RootIdx: 0,
		}, nil
	}
}