package EX

import (
	"testing"
	"time"
)

func TestTxnDebugger_Basic(t *testing.T) {
	td := NewTxnDebugger()
	
	td.SetSnapshot(1000)
	td.RecordVisible()
	td.RecordVisible()
	td.RecordHidden()
	td.RecordLockWait(100 * time.Millisecond)
	
info := td.GetInfo()
	if info.SnapshotTS != 1000 {
		t.Errorf("SnapshotTS = %d, want 1000", info.SnapshotTS)
	}
	if info.VisibleRows != 2 {
		t.Errorf("VisibleRows = %d, want 2", info.VisibleRows)
	}
	if info.HiddenByMVCC != 1 {
		t.Errorf("HiddenByMVCC = %d, want 1", info.HiddenByMVCC)
	}
	if info.LockWaitTimeNS != 100_000_000 {
		t.Errorf("LockWaitTimeNS = %d, want 100000000", info.LockWaitTimeNS)
	}
	if info.IsolationLevel != "read-committed" {
		t.Errorf("IsolationLevel = %q, want %q", info.IsolationLevel, "read-committed")
	}
}

func TestTxnDebugger_Clear(t *testing.T) {
	td := NewTxnDebugger()
	
	td.RecordVisible()
	td.RecordHidden()
	td.RecordLockWait(50 * time.Millisecond)
	
	td.Clear()
	
info := td.GetInfo()
	if info.VisibleRows != 0 {
		t.Errorf("VisibleRows after Clear = %d, want 0", info.VisibleRows)
	}
	if info.HiddenByMVCC != 0 {
		t.Errorf("HiddenByMVCC after Clear = %d, want 0", info.HiddenByMVCC)
	}
	if info.LockWaitTimeNS != 0 {
		t.Errorf("LockWaitTimeNS after Clear = %d, want 0", info.LockWaitTimeNS)
	}
}

func TestTxnDebugger_Concurrency(t *testing.T) {
	td := NewTxnDebugger()
	
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			td.RecordVisible()
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			td.RecordHidden()
		}
		done <- struct{}{}
	}()
	
	<-done
	<-done
	
info := td.GetInfo()
	if info.VisibleRows+info.HiddenByMVCC != 200 {
		t.Errorf("Total rows = %d, want 200", info.VisibleRows+info.HiddenByMVCC)
	}
}
