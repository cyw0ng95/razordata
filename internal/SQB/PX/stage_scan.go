package PX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// ScanStageSpec creates ScanStage instances. A ScanStage wraps any
// UT.BatchProducer that reads from a table store or in-memory table.
// The wrapped producer is provided at spec construction time (not at
// runtime), so the spec captures the scan configuration (table name,
// schema, column indices, predicate pushdown, etc.) and produces a
// fresh ScanStage on each NewRuntime call.
type ScanStageSpec struct {
	// NewProducer creates a fresh UT.BatchProducer for the scan.
	// Called once per NewRuntime(). This function captures all scan
	// configuration (table, schema, pushdown predicates, column
	// pruning) in its closure.
	NewProducer func() UT.BatchProducer
}

// NewRuntime creates a ScanStage that reads batches from the producer.
func (s *ScanStageSpec) NewRuntime() Stage {
	return &ScanStage{
		producer: s.NewProducer(),
	}
}

// Category returns CatSource.
func (s *ScanStageSpec) Category() StageCategory { return CatSource }

// ScanStage is a Source-stage that reads columnar batches from an
// underlying BatchProducer (e.g., VectorizedSeqScan, FusedBatchScan,
// or any scan that implements UT.BatchProducer).
//
// It adapts the existing BatchProducer interface to the Stage interface
// by adding Reset support. Since scan producers typically cannot be
// reset (they hold iterators over the table store), Reset returns
// ErrResetNotSupported and the Pipeline recreates the stage from its
// ScanStageSpec.
type ScanStage struct {
	producer UT.BatchProducer
	execCtx  *DT.ExecContext
	done     bool
}

// NextBatch returns the next batch from the scan producer.
// Returns (nil, nil) at EOF. The caller owns the returned batch.
func (s *ScanStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.done {
		return nil, nil
	}
	batch, err := s.producer.NextBatch(ctx)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		s.done = true
		return nil, nil
	}
	if s.execCtx != nil {
		batch.ExecCtx = s.execCtx
	}
	return batch, nil
}

// PropagateExecContext stores per-execution context for subquery
// evaluation and row arena. REQ002148.
func (s *ScanStage) PropagateExecContext(ec *DT.ExecContext) {
	s.execCtx = ec
	if ep, ok := s.producer.(ExecContextPropagator); ok {
		ep.PropagateExecContext(ec)
	}
}

// Reset returns ErrResetNotSupported because scan producers hold
// iterators that cannot be rewound. The Pipeline handles this by
// recreating the ScanStage from its ScanStageSpec.
func (s *ScanStage) Reset(_ context.Context) error {
	return ErrResetNotSupported
}

// Close releases the scan producer.
func (s *ScanStage) Close() error {
	if s.producer != nil {
		return s.producer.Close()
	}
	return nil
}
