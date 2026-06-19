package SY

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestShutdown_5RunStability pins R14-19: the full Open -> 100 inserts
// -> Close loop runs cleanly 5 times consecutively with no leaks and
// no flakes. A failure here signals a race in the shutdown sequence.
// Note: a concurrent-readers variant is not included because it
// exposes a pre-existing data race in the ENG/LS engine
// (activeMem is written by flushActiveMemtable without locking
// while NewIterator reads it; tracked under iter-12b REQ000186-189).
// The shutdown sequence itself is race-clean; the race is in the
// storage layer it tears down.
func TestShutdown_5RunStability(t *testing.T) {
	for run := 0; run < 5; run++ {
		runtime.GC()
		executor.UnregisterAll()
		dir := filepath.Join(t.TempDir(), "db")
		eng, err := Open(context.Background(), dir, AP.Options{
			PageSize:     4096,
			MemTableSize: 1 << 20,
			BufferPoolMB: 64,
			WALSizeMB:    16,
			MaxLevel:     3,
			LogLevel:     8,
			LogFormat:    "text",
		})
		if err != nil {
			t.Fatalf("run %d: open: %v", run, err)
		}
		s, err := eng.Begin(context.Background())
		if err != nil {
			t.Fatalf("run %d: begin: %v", run, err)
		}
		if _, err := s.Exec(context.Background(), "CREATE TABLE t (id INTEGER, val TEXT, PRIMARY KEY (id))"); err != nil {
			t.Fatalf("run %d: create: %v", run, err)
		}
		for j := 0; j < 100; j++ {
			if _, err := s.Exec(context.Background(), fmt.Sprintf("INSERT INTO t VALUES (%d, 'x')", j)); err != nil {
				t.Fatalf("run %d: insert %d: %v", run, j, err)
			}
		}
		if err := eng.Close(context.Background()); err != nil {
			t.Fatalf("run %d: close: %v", run, err)
		}
		stats := eng.Stats()
		if stats.LastShutdown.At.IsZero() {
			t.Errorf("run %d: LastShutdown.At zero", run)
		}
		if stats.LastShutdown.ForceAborted != 0 {
			t.Errorf("run %d: LastShutdown.ForceAborted: want 0, got %d", run, stats.LastShutdown.ForceAborted)
		}
	}
}

// TestShutdown_NoGoroutineLeak pins the long-term goroutine
// stability: 20 Open/Close cycles, with a final goroutine count
// that is bounded by a reasonable delta. The Phase 4 stop work
// should drive most of the goroutines to zero.
func TestShutdown_NoGoroutineLeak(t *testing.T) {
	// Warm up: one cycle to settle the runtime's baseline.
	{
		eng := openForCloseTest(t)
		_ = eng.Close(context.Background())
	}
	runtime.GC()
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		eng := openForCloseTest(t)
		if err := eng.Close(context.Background()); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		runtime.Gosched()
		runtime.GC()
	}
	// 20 cycles * 2 goroutines (compaction + flush) = 40
	// theoretical max if the stop work didn't fire; the actual
	// delta should be much lower. 25 is a generous ceiling to
	// avoid flakiness from the globalEpochManager which runs
	// outside the engine's control (see commit message of
	// iter-14 commit 3).
	if delta := runtime.NumGoroutine() - before; delta > 25 {
		t.Errorf("goroutine leak: delta=%d after 20 open/close cycles", delta)
	}
}
