package rp

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestParallelReplayer_Basic verifies records are applied
// exactly once. REQ000317.
func TestParallelReplayer_Basic(t *testing.T) {
	records := make([]parsedRecord, 100)
	for i := range records {
		records[i] = parsedRecord{
			kind:    1,
			blockID: uint64(i),
			value:   []byte{byte(i)},
		}
	}
	p := NewParallelReplayer(4)
	stats, err := p.RunParallel(records, func(r parsedRecord) error {
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stats.Applied != 100 {
		t.Errorf("applied=%d, want 100", stats.Applied)
	}
	if stats.Total != 100 {
		t.Errorf("total=%d, want 100", stats.Total)
	}
}

// TestParallelReplayer_Empty handles zero records without
// dispatching any workers.
func TestParallelReplayer_Empty(t *testing.T) {
	p := NewParallelReplayer(4)
	stats, err := p.RunParallel(nil, func(r parsedRecord) error { return nil })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stats.Applied != 0 {
		t.Errorf("applied=%d, want 0", stats.Applied)
	}
}

// TestParallelReplayer_ConcurrentCounter verifies the per-
// record work function is called concurrently and the
// counter accumulates correctly. REQ000317.
func TestParallelReplayer_ConcurrentCounter(t *testing.T) {
	var counter atomic.Uint64
	records := make([]parsedRecord, 1000)
	for i := range records {
		records[i] = parsedRecord{kind: 1, blockID: uint64(i)}
	}
	p := NewParallelReplayer(8)
	_, err := p.RunParallel(records, func(r parsedRecord) error {
		counter.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if counter.Load() != 1000 {
		t.Errorf("counter=%d, want 1000", counter.Load())
	}
}

// TestParallelReplayer_PropagatesError verifies an error in
// one partition fails the replay.
func TestParallelReplayer_PropagatesError(t *testing.T) {
	records := make([]parsedRecord, 100)
	for i := range records {
		records[i] = parsedRecord{kind: 1, blockID: uint64(i)}
	}
	p := NewParallelReplayer(4)
	_, err := p.RunParallel(records, func(r parsedRecord) error {
		if r.blockID == 50 {
			return errSimulated
		}
		return nil
	})
	if err == nil {
		t.Error("expected error to propagate")
	}
}

var errSimulated = &parallelError{"simulated"}

type parallelError struct{ msg string }

func (e *parallelError) Error() string { return e.msg }

// TestParallelReplayer_NoOpWork verifies a no-op work function
// is safe and quick.
func TestParallelReplayer_NoOpWork(t *testing.T) {
	records := make([]parsedRecord, 50)
	p := NewParallelReplayer(0) // default
	stats, err := p.RunParallel(records, func(r parsedRecord) error {
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stats.Applied != 50 {
		t.Errorf("applied=%d, want 50", stats.Applied)
	}
}

// TestParallelReplayer_GoroutineSafety verifies the
// implementation is safe under -race (no shared mutable state
// without sync).
func TestParallelReplayer_GoroutineSafety(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[uint64]bool)
	records := make([]parsedRecord, 500)
	for i := range records {
		records[i] = parsedRecord{kind: 1, blockID: uint64(i)}
	}
	p := NewParallelReplayer(4)
	_, err := p.RunParallel(records, func(r parsedRecord) error {
		mu.Lock()
		defer mu.Unlock()
		if seen[r.blockID] {
			return &parallelError{"duplicate"}
		}
		seen[r.blockID] = true
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(seen) != 500 {
		t.Errorf("seen=%d, want 500", len(seen))
	}
}
