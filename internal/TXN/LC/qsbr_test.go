package LC

import (
	"sync"
	"testing"
)

// TestQSBR_EnterExit verifies the per-shard flag toggles.
// REQ000308.
func TestQSBR_EnterExit(t *testing.T) {
	q := newQSBRManager()
	defer q.Stop()
	// Initially quiescent.
	if !q.IsQuiescent() {
		t.Error("expected initial quiescence")
	}
	_, shard := q.Enter()
	if q.IsQuiescent() {
		t.Error("expected non-quiescent after Enter")
	}
	q.Exit(shard)
	if !q.IsQuiescent() {
		t.Error("expected quiescent after Exit")
	}
}

// TestQSBR_WaitQuiescent verifies the spin loop returns when
// all readers have exited. REQ000308.
func TestQSBR_WaitQuiescent(t *testing.T) {
	q := newQSBRManager()
	defer q.Stop()
	_, shard := q.Enter()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Release the reader after a brief delay.
		for i := 0; i < 100; i++ {
			if q.IsQuiescent() {
				return
			}
		}
	}()
	// Main thread exits the critical section after a moment.
	q.Exit(shard)
	wg.Wait()
	if !q.IsQuiescent() {
		t.Error("expected quiescent after both threads exit")
	}
}

// TestQSBR_BumpEpoch verifies the global epoch increments.
// REQ000308.
func TestQSBR_BumpEpoch(t *testing.T) {
	q := newQSBRManager()
	defer q.Stop()
	ep1 := q.BumpEpoch()
	ep2 := q.BumpEpoch()
	if ep2 != ep1+1 {
		t.Errorf("expected monotonic epochs: ep1=%d ep2=%d", ep1, ep2)
	}
	if q.CurrentEpoch() != ep2 {
		t.Errorf("CurrentEpoch=%d, want %d", q.CurrentEpoch(), ep2)
	}
}

// TestQSBR_Stats verifies the stats snapshot. REQ000308.
func TestQSBR_Stats(t *testing.T) {
	q := newQSBRManager()
	defer q.Stop()
	stats := q.Stats()
	if stats.Shards != qsbrShardCount {
		t.Errorf("Shards=%d, want %d", stats.Shards, qsbrShardCount)
	}
	if stats.Active != 0 {
		t.Errorf("Active=%d, want 0 (quiescent)", stats.Active)
	}
	_, shard := q.Enter()
	stats = q.Stats()
	if stats.Active < 1 {
		t.Errorf("Active=%d, want >=1 after Enter", stats.Active)
	}
	q.Exit(shard)
}

// TestQSBR_ConcurrentReaders verifies multiple goroutines
// entering/exiting concurrently. REQ000308.
func TestQSBR_ConcurrentReaders(t *testing.T) {
	q := newQSBRManager()
	defer q.Stop()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_, shard := q.Enter()
				q.Exit(shard)
			}
		}()
	}
	wg.Wait()
	if !q.IsQuiescent() {
		t.Error("expected quiescent after all readers exit")
	}
}
