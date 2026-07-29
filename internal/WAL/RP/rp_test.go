package rp

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/BF"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
	"github.com/cyw0ng95/razordata/internal/WAL/WR"
)

func TestAtomicBoolClear_RP(t *testing.T) {
	var a atomicBool

	if a.isSet() {
		t.Error("expected initially clear")
	}

	a.set()
	if !a.isSet() {
		t.Error("expected set after set()")
	}

	a.clear()
	if a.isSet() {
		t.Error("expected clear after clear()")
	}
}

func TestAtomicBoolClearMultiple_RP(t *testing.T) {
	var a atomicBool

	a.set()
	a.clear()
	a.clear()
	a.clear()

	if a.isSet() {
		t.Error("expected clear after multiple clears")
	}
}

func TestNew(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setup SegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setup BufferPool: %v", err)
	}
	defer bp.Close()

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.Close()
}

func TestNewRequiresDir(t *testing.T) {
	sm, err := setupSegmentManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	_, err = New("", sm, bp, Callbacks{}, nil)
	if err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestNewRequiresSegmentManager(t *testing.T) {
	bp, _ := setupBufferPool(t.TempDir())

	_, err := New(t.TempDir(), nil, bp, Callbacks{}, nil)
	if err == nil {
		t.Error("expected error for nil SegmentManager")
	}
}

func TestNewRequiresBufferPool(t *testing.T) {
	sm, _ := setupSegmentManager(t.TempDir())
	defer sm.Close()

	_, err := New(t.TempDir(), sm, nil, Callbacks{}, nil)
	if err == nil {
		t.Error("expected error for nil BufferPool")
	}
}

func TestReplayEmptyWAL(t *testing.T) {
	r, cleanup := newTestReplayer(t)
	defer cleanup()

	err := r.Replay()
	if err != nil {
		t.Errorf("Replay on empty WAL: %v", err)
	}
}

func TestLastCheckpointEmptyWAL(t *testing.T) {
	r, cleanup := newTestReplayer(t)
	defer cleanup()

	cp, err := r.LastCheckpoint()
	if err == nil {
		t.Error("expected error for empty WAL")
	}
	if cp != nil {
		t.Error("expected nil checkpoint")
	}
}

func TestReplayAfterClose(t *testing.T) {
	r, cleanup := newTestReplayer(t)
	r.Close()
	cleanup()

	err := r.Replay()
	if err == nil {
		t.Error("expected error after Close")
	}
}

func TestLastCheckpointAfterClose(t *testing.T) {
	r, cleanup := newTestReplayer(t)
	r.Close()
	cleanup()

	_, err := r.LastCheckpoint()
	if err == nil {
		t.Error("expected error after Close")
	}
}

func TestCloseIdempotent(t *testing.T) {
	r, cleanup := newTestReplayer(t)
	cleanup()

	err := r.Close()
	if err != nil {
		t.Errorf("first Close: %v", err)
	}

	err = r.Close()
	if err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestCallbacksZeroValue(t *testing.T) {
	r, cleanup := newTestReplayer(t)
	defer cleanup()

	err := r.Replay()
	if err != nil {
		t.Errorf("Replay with zero callbacks: %v", err)
	}
}

func TestCallbacksWithHooks(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setup SegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setup BufferPool: %v", err)
	}
	defer bp.Close()

	cb := Callbacks{
		OnData: func(blockID uint64, data []byte) error {
			return nil
		},
		OnCommit: func(txnID uint64, commitTS uint64) error {
			return nil
		},
		OnRollback: func(txnID uint64) error {
			return nil
		},
	}

	r, err := New(tmp, sm, bp, cb, nil)
	if err != nil {
		t.Fatalf("New with hooks: %v", err)
	}
	r.Close()
}

func TestReplayerInterface(t *testing.T) {
	var _ Replayer = (*replayer)(nil)
}

func setupSegmentManager(tmp string) (*lf.SegmentManager, error) {
	walDir := filepath.Join(tmp, "wal")
	os.MkdirAll(walDir, 0755)
	return lf.New(tmp)
}

func setupBufferPool(tmp string) (bf.BufferPool, error) {
	dfPath := filepath.Join(tmp, "buffer.pool")
	bd, err := df.Create(dfPath)
	if err != nil {
		return nil, err
	}
	bd.Close()

	bd, err = df.Open(dfPath)
	if err != nil {
		return nil, err
	}

	sp := sp.New()
	return bf.New(16, "", bd, sp)
}

func newTestReplayer(t *testing.T) (Replayer, func()) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setup SegmentManager: %v", err)
	}

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setup BufferPool: %v", err)
	}

	cleanup := func() {
		sm.Close()
		bp.Close()
	}

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return r, cleanup
}

func TestReplayWithData(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setupSegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setupBufferPool: %v", err)
	}
	defer bp.Close()

	w, err := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
	if err != nil {
		t.Fatalf("wr.New: %v", err)
	}

	batch := &wr.WriteBatch{
		TxnID: 1,
		Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: 1, Value: []byte("block1_data")},
			{Type: wr.RTData, BlockID: 2, Value: []byte("block2_data")},
			{Type: wr.RTCommit, TxnID: 1},
		},
	}
	_, err = w.Append(batch)
	if err != nil {
		t.Fatalf("wr.Append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("wr.Sync: %v", err)
	}
	w.Close()

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()

	if err := r.Replay(); err != nil {
		t.Errorf("Replay: %v", err)
	}
}

func TestReplayTriggersCallbacks(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setupSegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setupBufferPool: %v", err)
	}
	defer bp.Close()

	w, err := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
	if err != nil {
		t.Fatalf("wr.New: %v", err)
	}

	batch := &wr.WriteBatch{
		TxnID: 42,
		Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: 100, Value: []byte("callback_test_data")},
			{Type: wr.RTCommit, TxnID: 42},
		},
	}
	_, err = w.Append(batch)
	if err != nil {
		t.Fatalf("wr.Append: %v", err)
	}
	w.Sync()
	w.Close()

	var onDataCalled, onCommitCalled bool
	var onCommitTxnID uint64

	cb := Callbacks{
		OnData: func(blockID uint64, data []byte) error {
			onDataCalled = true
			if blockID != 100 {
				t.Errorf("OnData blockID: got %d, want 100", blockID)
			}
			if string(data) != "callback_test_data" {
				t.Errorf("OnData data: got %q, want %q", string(data), "callback_test_data")
			}
			return nil
		},
		OnCommit: func(txnID uint64, commitTS uint64) error {
			onCommitCalled = true
			onCommitTxnID = txnID
			return nil
		},
	}

	r, err := New(tmp, sm, bp, cb, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()

	if err := r.Replay(); err != nil {
		t.Errorf("Replay: %v", err)
	}

	if !onDataCalled {
		t.Error("OnData was not called")
	}
	if !onCommitCalled {
		t.Error("OnCommit was not called")
	}
	if onCommitTxnID != 42 {
		t.Errorf("OnCommit txnID: got %d, want 42", onCommitTxnID)
	}
}

func TestLastCheckpointFindsCheckpoint(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setupSegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setupBufferPool: %v", err)
	}
	defer bp.Close()

	w, err := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
	if err != nil {
		t.Fatalf("wr.New: %v", err)
	}

	for i := uint64(0); i < 3; i++ {
		batch := &wr.WriteBatch{
			TxnID: i + 1,
			Recs: []wr.LogRecord{
				{Type: wr.RTData, BlockID: i + 10, Value: []byte("block")},
				{Type: wr.RTCommit, TxnID: i + 1},
			},
		}
		_, err = w.Append(batch)
		if err != nil {
			t.Fatalf("wr.Append: %v", err)
		}
	}

	cp := &wr.Checkpoint{
		LSN:              100,
		CatalogRootPtr:   200,
		ManifestChecksum: 300,
		ActiveTXNs:       []uint64{1, 2, 3},
	}
	header, txns := wr.AppendCheckpointPayload(cp)
	cpBatch := &wr.WriteBatch{
		TxnID: 999,
		Recs: []wr.LogRecord{
			{Type: wr.RTCheckpoint, BlockID: uint64(len(cp.ActiveTXNs)), Key: header, Value: txns},
		},
	}
	_, err = w.Append(cpBatch)
	if err != nil {
		t.Fatalf("wr.Append checkpoint: %v", err)
	}
	w.Sync()
	w.Close()

	r, err := New(tmp, sm, bp, Callbacks{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()

	foundCp, err := r.LastCheckpoint()
	if err != nil {
		t.Fatalf("LastCheckpoint: %v", err)
	}
	if foundCp == nil {
		t.Fatal("expected checkpoint, got nil")
	}
	if foundCp.LSN != 100 {
		t.Errorf("checkpoint LSN: got %d, want 100", foundCp.LSN)
	}
	if foundCp.CatalogRootPtr != 200 {
		t.Errorf("checkpoint CatalogRootPtr: got %d, want 200", foundCp.CatalogRootPtr)
	}
}

func TestReplayWithCheckpointTruncation(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setupSegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setupBufferPool: %v", err)
	}
	defer bp.Close()

	w, err := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
	if err != nil {
		t.Fatalf("wr.New: %v", err)
	}

	_, err = w.Append(&wr.WriteBatch{
		TxnID: 1,
		Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: 1, Value: []byte("old_data")},
			{Type: wr.RTCommit, TxnID: 1},
		},
	})
	if err != nil {
		t.Fatalf("wr.Append: %v", err)
	}

	cp := &wr.Checkpoint{
		LSN:              50,
		CatalogRootPtr:   100,
		ManifestChecksum: 0,
		ActiveTXNs:       []uint64{},
	}
	header, txns := wr.AppendCheckpointPayload(cp)
	if _, err = w.Append(&wr.WriteBatch{
		TxnID: 2,
		Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: 2, Value: []byte("new_data")},
			{Type: wr.RTCommit, TxnID: 2},
		},
	}); err != nil {
		t.Fatalf("wr.Append data: %v", err)
	}
	if _, err = w.Append(&wr.WriteBatch{
		TxnID: 3,
		Recs: []wr.LogRecord{
			{Type: wr.RTCheckpoint, BlockID: 0, Key: header, Value: txns},
		},
	}); err != nil {
		t.Fatalf("wr.Append checkpoint: %v", err)
	}
	w.Sync()
	w.Close()

	var replayedBlocks []uint64
	cb := Callbacks{
		OnData: func(blockID uint64, data []byte) error {
			replayedBlocks = append(replayedBlocks, blockID)
			return nil
		},
	}

	r, err := New(tmp, sm, bp, cb, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()

	if err := r.Replay(); err != nil {
		t.Errorf("Replay: %v", err)
	}

	if len(replayedBlocks) == 0 {
		t.Error("expected some blocks to be replayed")
	}
}

// TestReplayLargeSegmentSpansChunks verifies that records spanning
// multiple read chunks (>64KB total) are all recovered during replay.
// REQ000572 — the inner-loop `offset += int64(consumed)` was double-
// counting the chunk-relative advance that `off += consumed` already
// tracks, so the next chunk read skipped records (the file position
// jumped past them). The outer `offset += int64(off)` after the loop
// was the correct advance.
func TestReplayLargeSegmentSpansChunks(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setupSegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setupBufferPool: %v", err)
	}
	defer bp.Close()

	w, err := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
	if err != nil {
		t.Fatalf("wr.New: %v", err)
	}

	const recordCount = 4000
	const payloadSize = 64
	batch := &wr.WriteBatch{
		TxnID: 1,
		Recs:  make([]wr.LogRecord, 0, recordCount*2+1),
	}
	for i := uint64(0); i < recordCount; i++ {
		val := make([]byte, payloadSize)
		for j := range val {
			val[j] = byte(i + uint64(j))
		}
		batch.Recs = append(batch.Recs, wr.LogRecord{
			Type:    wr.RTData,
			BlockID: i + 1,
			Value:   val,
		})
	}
	batch.Recs = append(batch.Recs, wr.LogRecord{Type: wr.RTCommit, TxnID: 1})

	if _, err = w.Append(batch); err != nil {
		t.Fatalf("wr.Append: %v", err)
	}
	if err := w.Sync(); err != nil {
		t.Fatalf("wr.Sync: %v", err)
	}
	w.Close()

	var seenDataBlocks []uint64
	var seenCommitTxnID uint64
	cb := Callbacks{
		OnData: func(blockID uint64, data []byte) error {
			seenDataBlocks = append(seenDataBlocks, blockID)
			return nil
		},
		OnCommit: func(txnID uint64, commitTS uint64) error {
			seenCommitTxnID = txnID
			return nil
		},
	}

	r, err := New(tmp, sm, bp, cb, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()

	if err := r.Replay(); err != nil {
		t.Errorf("Replay: %v", err)
	}

	if len(seenDataBlocks) != recordCount {
		t.Errorf("replayed %d RTData records, want %d", len(seenDataBlocks), recordCount)
	}
	for i, blk := range seenDataBlocks {
		if blk != uint64(i+1) {
			t.Errorf("record %d: blockID=%d, want %d", i, blk, i+1)
			break
		}
	}
	if seenCommitTxnID != 1 {
		t.Errorf("commit txnID=%d, want 1", seenCommitTxnID)
	}
}

func TestReplayRollbacksActiveTXNsFromCheckpoint(t *testing.T) {
	tmp := t.TempDir()

	sm, err := setupSegmentManager(tmp)
	if err != nil {
		t.Fatalf("setupSegmentManager: %v", err)
	}
	defer sm.Close()

	bp, err := setupBufferPool(tmp)
	if err != nil {
		t.Fatalf("setupBufferPool: %v", err)
	}
	defer bp.Close()

	w, err := wr.New(tmp, sm, sp.New(), lg.New(lg.Options{Output: io.Discard}), false)
	if err != nil {
		t.Fatalf("wr.New: %v", err)
	}

	// Write a checkpoint with active transactions 7 and 8.
	cp := &wr.Checkpoint{
		LSN:              50,
		CatalogRootPtr:   100,
		ManifestChecksum: 200,
		ActiveTXNs:       []uint64{7, 8},
	}
	header, txns := wr.AppendCheckpointPayload(cp)
	cpBatch := &wr.WriteBatch{
		TxnID: 99,
		Recs: []wr.LogRecord{
			{Type: wr.RTCheckpoint, BlockID: uint64(len(cp.ActiveTXNs)), Key: header, Value: txns},
		},
	}
	if _, err = w.Append(cpBatch); err != nil {
		t.Fatalf("wr.Append checkpoint: %v", err)
	}

	// Write a later committed transaction 9 (after checkpoint).
	_, err = w.Append(&wr.WriteBatch{
		TxnID: 9,
		Recs: []wr.LogRecord{
			{Type: wr.RTData, BlockID: 20, Value: []byte("txn9_data")},
			{Type: wr.RTCommit, TxnID: 9},
		},
	})
	if err != nil {
		t.Fatalf("wr.Append: %v", err)
	}
	w.Sync()
	w.Close()

	var rolledBack []uint64
	var committed []uint64
	cb := Callbacks{
		OnCommit: func(txnID uint64, commitTS uint64) error {
			committed = append(committed, txnID)
			return nil
		},
		OnRollback: func(txnID uint64) error {
			rolledBack = append(rolledBack, txnID)
			return nil
		},
	}

	r, err := New(tmp, sm, bp, cb, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()

	if err := r.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	wantCommitted := []uint64{9}
	if len(committed) != len(wantCommitted) {
		t.Errorf("committed txns: got %v, want %v", committed, wantCommitted)
	}
	for i, v := range wantCommitted {
		if i >= len(committed) || committed[i] != v {
			t.Errorf("committed[%d]: got %d, want %d", i, committed[i], v)
		}
	}

	wantRolledBack := map[uint64]struct{}{7: {}, 8: {}}
	if len(rolledBack) != len(wantRolledBack) {
		t.Errorf("rolled back txns: got %v, want keys %v", rolledBack, wantRolledBack)
	}
	for _, txnID := range rolledBack {
		if _, ok := wantRolledBack[txnID]; !ok {
			t.Errorf("unexpected rollback for txn %d", txnID)
		}
	}
}
