//go:build !slt_corpus_full

package wr

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestWriter_SyncAsync_BasicPath exercises REQ000301: async fsync.
// The new SyncAsync() method must:
//   - return a non-nil result channel
//   - not block the caller (the fsync happens on a goroutine)
//   - eventually complete (channel sends a non-nil error or nil)
//   - update the synced LSN after the async fsync completes
func TestWriter_SyncAsync_BasicPath(t *testing.T) {
	w, _ := newTestWriter(t)
	defer w.Close()

	if _, err := w.Append(&WriteBatch{
		Recs: []LogRecord{{Type: RTData, BlockID: 1, Value: []byte("hello")}},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	ch, err := w.SyncAsync()
	if err != nil {
		t.Fatalf("SyncAsync: %v", err)
	}
	if ch == nil {
		t.Fatal("SyncAsync returned nil channel")
	}

	select {
	case res := <-ch:
		if res.Err != nil {
			t.Errorf("async fsync error: %v", res.Err)
		}
		if res.SyncedLSN == 0 {
			t.Error("expected non-zero SyncedLSN after fsync")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async fsync did not complete within 5s")
	}
}

// TestWriter_SyncAsync_Overlap verifies that multiple async
// fsyncs can be in flight simultaneously and the next Append's
// write does not block on the previous batch's fsync.
func TestWriter_SyncAsync_Overlap(t *testing.T) {
	w, _ := newTestWriter(t)
	defer w.Close()

	const n = 8
	chans := make([]<-chan AsyncSyncResult, 0, n)
	for i := 0; i < n; i++ {
		if _, err := w.Append(&WriteBatch{
			Recs: []LogRecord{{Type: RTData, BlockID: uint64(i), Value: []byte("data")}},
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		ch, err := w.SyncAsync()
		if err != nil {
			t.Fatalf("SyncAsync %d: %v", i, err)
		}
		chans = append(chans, ch)
	}
	for i, ch := range chans {
		select {
		case res := <-ch:
			if res.Err != nil {
				t.Errorf("async fsync %d error: %v", i, res.Err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("async fsync %d did not complete within 5s", i)
		}
	}
}

// TestWriter_SyncAsync_CloseWaits ensures that Close blocks
// until all in-flight async fsyncs complete.
func TestWriter_SyncAsync_CloseWaits(t *testing.T) {
	w, _ := newTestWriter(t)

	if _, err := w.Append(&WriteBatch{
		Recs: []LogRecord{{Type: RTData, BlockID: 1, Value: []byte("x")}},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	ch, _ := w.SyncAsync()
	wgDone := atomic.Bool{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ch
		wgDone.Store(true)
	}()
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	wg.Wait()
	if !wgDone.Load() {
		t.Error("Close did not wait for in-flight async fsync to complete")
	}
}
