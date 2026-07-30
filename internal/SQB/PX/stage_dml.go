package PX

import (
	"context"
	"sync"

	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// InsertStageSpec creates InsertStage instances for INSERT execution.
type InsertStageSpec struct {
	Insert *WT.Insert
}

// NewRuntime creates an InsertStage from this spec.
func (s *InsertStageSpec) NewRuntime() Stage {
	return &InsertStage{insert: s.Insert}
}

// Category returns CatSource — INSERT is a source stage that produces
// rows (the inserted rows for RETURNING).
func (s *InsertStageSpec) Category() StageCategory { return CatSource }

// InsertStage wraps WT.Insert as a Stage. It delegates NextBatch to
// the underlying Insert operator's NextBatch method.
type InsertStage struct {
	insert *WT.Insert
	mu     sync.Mutex
	closed bool
}

func (s *InsertStage) SetChild(_ ChildSide, child Stage) {}

func (s *InsertStage) PropagateParams(args []any, buf *[]any) {}

func (s *InsertStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil
	}
	return s.insert.NextBatch(ctx)
}

func (s *InsertStage) Reset(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil
}

func (s *InsertStage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.insert.Close()
}

// UpdateStageSpec creates UpdateStage instances for UPDATE execution.
type UpdateStageSpec struct {
	Update *WT.Update
}

// NewRuntime creates an UpdateStage from this spec.
func (s *UpdateStageSpec) NewRuntime() Stage {
	return &UpdateStage{update: s.Update}
}

// Category returns CatSource — UPDATE is a source stage that produces
// rows (the updated rows for RETURNING).
func (s *UpdateStageSpec) Category() StageCategory { return CatSource }

// UpdateStage wraps WT.Update as a Stage.
type UpdateStage struct {
	update *WT.Update
	mu     sync.Mutex
	closed bool
}

func (s *UpdateStage) SetChild(_ ChildSide, child Stage) {}

func (s *UpdateStage) PropagateParams(args []any, buf *[]any) {}

func (s *UpdateStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil
	}
	return s.update.NextBatch(ctx)
}

func (s *UpdateStage) Reset(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil
}

func (s *UpdateStage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.update.Close()
}

// DeleteStageSpec creates DeleteStage instances for DELETE execution.
type DeleteStageSpec struct {
	Delete *WT.Delete
}

// NewRuntime creates a DeleteStage from this spec.
func (s *DeleteStageSpec) NewRuntime() Stage {
	return &DeleteStage{del: s.Delete}
}

// Category returns CatSource — DELETE is a source stage that produces
// rows (the deleted rows for RETURNING).
func (s *DeleteStageSpec) Category() StageCategory { return CatSource }

// DeleteStage wraps WT.Delete as a Stage.
type DeleteStage struct {
	del    *WT.Delete
	mu     sync.Mutex
	closed bool
}

func (s *DeleteStage) SetChild(_ ChildSide, child Stage) {}

func (s *DeleteStage) PropagateParams(args []any, buf *[]any) {}

func (s *DeleteStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil
	}
	return s.del.NextBatch(ctx)
}

func (s *DeleteStage) Reset(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil
}

func (s *DeleteStage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.del.Close()
}

// Ensure compile-time satisfaction of Stage interface.
var (
	_ Stage = (*InsertStage)(nil)
	_ Stage = (*UpdateStage)(nil)
	_ Stage = (*DeleteStage)(nil)
	_ ChildSetter = (*InsertStage)(nil)
	_ ChildSetter = (*UpdateStage)(nil)
	_ ChildSetter = (*DeleteStage)(nil)
	_ StageSpec = (*InsertStageSpec)(nil)
	_ StageSpec = (*UpdateStageSpec)(nil)
	_ StageSpec = (*DeleteStageSpec)(nil)
)