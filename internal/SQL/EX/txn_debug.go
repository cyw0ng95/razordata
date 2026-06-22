package EX

import (
	"sync"
	"time"
)

// TxnDebugger tracks MVCC/transaction statistics for EXPLAIN ANALYZE.
// REQ000792: MVCC debugging in EXPLAIN output.
type TxnDebugger struct {
	mu              sync.Mutex
	SnapshotTS      uint64
	VisibleRows     int64
	HiddenByMVCC    int64
	LockWaitTimeNS  int64
	IsolationLevel  string
	RowChecks       int64 // total rows checked (visible + hidden)
}

// NewTxnDebugger creates a new TxnDebugger.
func NewTxnDebugger() *TxnDebugger {
	return &TxnDebugger{
		IsolationLevel: "read-committed",
	}
}

// SetSnapshot sets the snapshot timestamp.
func (td *TxnDebugger) SetSnapshot(ts uint64) {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.SnapshotTS = ts
}

// RecordVisible records a row visible at snapshot.
func (td *TxnDebugger) RecordVisible() {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.VisibleRows++
	td.RowChecks++
}

// RecordHidden records a row hidden by MVCC.
func (td *TxnDebugger) RecordHidden() {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.HiddenByMVCC++
	td.RowChecks++
}

// RecordLockWait records lock wait time.
func (td *TxnDebugger) RecordLockWait(d time.Duration) {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.LockWaitTimeNS += int64(d)
}

// GetInfo returns TxnDebugInfo for EXPLAIN output.
func (td *TxnDebugger) GetInfo() *TxnDebugInfo {
	td.mu.Lock()
	defer td.mu.Unlock()
	return &TxnDebugInfo{
		SnapshotTS:      td.SnapshotTS,
		VisibleRows:     td.VisibleRows,
		HiddenByMVCC:    td.HiddenByMVCC,
		LockWaitTimeNS:  td.LockWaitTimeNS,
		IsolationLevel:  td.IsolationLevel,
	}
}

// Clear resets all counters.
func (td *TxnDebugger) Clear() {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.VisibleRows = 0
	td.HiddenByMVCC = 0
	td.LockWaitTimeNS = 0
	td.RowChecks = 0
}
