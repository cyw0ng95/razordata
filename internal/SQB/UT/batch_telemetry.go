package UT

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

)

// BatchTelemetry records per-operator execution metrics for the
// vectorized execution path. REQ001448.
type BatchTelemetry struct {
	mu            sync.Mutex
	OpName        string
	Rows          int64
	Batches       int64
	WallNs        int64
	AllocBytes    int64
	UsedBatchMode bool
}

// TelemetryCollector collects telemetry from all operators in a query.
type TelemetryCollector struct {
	mu       sync.Mutex
	recorded []*BatchTelemetry
}

// NewTelemetryCollector creates a new collector.
func NewTelemetryCollector() *TelemetryCollector {
	return &TelemetryCollector{}
}

// Record adds a telemetry record.
func (tc *TelemetryCollector) Record(bt *BatchTelemetry) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.recorded = append(tc.recorded, bt)
}

// GetRecords returns all recorded telemetry.
func (tc *TelemetryCollector) GetRecords() []*BatchTelemetry {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	out := make([]*BatchTelemetry, len(tc.recorded))
	copy(out, tc.recorded)
	return out
}

// AggregatedTelemetry holds aggregated counters per operator name.
type AggregatedTelemetry struct {
	OpName        string
	TotalRows     int64
	TotalBatches  int64
	TotalWallNs   int64
	AvgRowsPerBat float64
}

// Aggregate returns aggregated telemetry grouped by operator name.
func (tc *TelemetryCollector) Aggregate() []AggregatedTelemetry {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	aggMap := make(map[string]*AggregatedTelemetry)
	for _, r := range tc.recorded {
		a, ok := aggMap[r.OpName]
		if !ok {
			a = &AggregatedTelemetry{OpName: r.OpName}
			aggMap[r.OpName] = a
		}
		a.TotalRows += r.Rows
		a.TotalBatches += r.Batches
		a.TotalWallNs += r.WallNs
	}
	result := make([]AggregatedTelemetry, 0, len(aggMap))
	for _, a := range aggMap {
		if a.TotalBatches > 0 {
			a.AvgRowsPerBat = float64(a.TotalRows) / float64(a.TotalBatches)
		}
		result = append(result, *a)
	}
	return result
}

// TelemetryRecorder is an optional interface operators can implement
// to emit telemetry during execution.
type TelemetryRecorder interface {
	RecordTelemetry(tc *TelemetryCollector)
}

// TelemetryBatchProducer wraps a BatchProducer and records telemetry.
type TelemetryBatchProducer struct {
	child    BatchProducer
	opName   string
	collector *TelemetryCollector
	rows     int64
	batches  int64
	wallStart time.Time
	wallTotal int64
	mu       sync.Mutex
}

// NewTelemetryBatchProducer wraps a BatchProducer with telemetry collection.
func NewTelemetryBatchProducer(child BatchProducer, opName string, collector *TelemetryCollector) *TelemetryBatchProducer {
	return &TelemetryBatchProducer{
		child:     child,
		opName:    opName,
		collector: collector,
	}
}

// NextBatch delegates to child and records metrics.
func (t *TelemetryBatchProducer) NextBatch(ctx context.Context) (*Batch, error) {
	t.mu.Lock()
	if t.wallStart.IsZero() {
		t.wallStart = time.Now()
	}
	t.mu.Unlock()

	start := time.Now()
	batch, err := t.child.NextBatch(ctx)
	elapsed := time.Since(start)

	t.mu.Lock()
	t.wallTotal += elapsed.Nanoseconds()
	if batch != nil {
		t.rows += int64(batch.LogicalSize())
		t.batches++
	}
	t.mu.Unlock()

	if batch == nil && t.batches > 0 {
		// EOF — record final telemetry
		bt := &BatchTelemetry{
			OpName:        t.opName,
			Rows:          t.rows,
			Batches:       t.batches,
			WallNs:        t.wallTotal,
			UsedBatchMode: true,
		}
		if t.collector != nil {
			t.collector.Record(bt)
		}
	}

	return batch, err
}

// Close delegates to child.
func (t *TelemetryBatchProducer) Close() error {
	if t.child != nil {
		return t.child.Close()
	}
	return nil
}

// TelemetryAtomicBatchProducer is a lock-free variant using atomics.
type TelemetryAtomicBatchProducer struct {
	child     BatchProducer
	opName    string
	collector *TelemetryCollector
	rows      atomic.Int64
	batches   atomic.Int64
	wallTotal atomic.Int64
	wallStart atomic.Int64 // nanoseconds since epoch, 0 = unset
	done      atomic.Bool
}

// NewTelemetryAtomicBatchProducer creates a lock-free telemetry wrapper.
func NewTelemetryAtomicBatchProducer(child BatchProducer, opName string, collector *TelemetryCollector) *TelemetryAtomicBatchProducer {
	return &TelemetryAtomicBatchProducer{
		child:     child,
		opName:    opName,
		collector: collector,
	}
}

// NextBatch delegates with atomic telemetry.
func (t *TelemetryAtomicBatchProducer) NextBatch(ctx context.Context) (*Batch, error) {
	startNS := time.Now().UnixNano()
	if t.wallStart.Load() == 0 {
		t.wallStart.Store(startNS)
	}

	batch, err := t.child.NextBatch(ctx)
	elapsed := time.Now().UnixNano() - startNS
	t.wallTotal.Add(elapsed)

	if batch != nil {
		t.rows.Add(int64(batch.LogicalSize()))
		t.batches.Add(1)
	}

	if err != nil || (batch == nil && !t.done.Load()) {
		t.done.Store(true)
		bt := &BatchTelemetry{
			OpName:        t.opName,
			Rows:          t.rows.Load(),
			Batches:       t.batches.Load(),
			WallNs:        t.wallTotal.Load(),
			UsedBatchMode: true,
		}
		if t.collector != nil {
			t.collector.Record(bt)
		}
	}

	return batch, err
}

// Close delegates to child.
func (t *TelemetryAtomicBatchProducer) Close() error {
	if t.child != nil {
		return t.child.Close()
	}
	return nil
}

// TelemetryContextKey is the context key for passing a telemetry collector.
type TelemetryContextKey struct{}

// WithTelemetryCollector returns a context with the collector attached.
func WithTelemetryCollector(ctx context.Context, tc *TelemetryCollector) context.Context {
	return context.WithValue(ctx, TelemetryContextKey{}, tc)
}

// GetTelemetryCollector returns the collector from context, or nil.
func GetTelemetryCollector(ctx context.Context) *TelemetryCollector {
	if tc, ok := ctx.Value(TelemetryContextKey{}).(*TelemetryCollector); ok {
		return tc
	}
	return nil
}
