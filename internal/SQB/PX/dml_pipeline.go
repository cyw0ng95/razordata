package PX

import (
	"errors"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
)

// BuildDMLPipelineSpec creates a PipelineSpec for a DML writer operator
// (WT.Insert/Update/Delete). Returns an error for unsupported types.
// REQ002142.
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
		return nil, errors.New("px: BuildDMLPipelineSpec: unsupported operator type")
	}
}