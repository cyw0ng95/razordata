package rp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/DF"
	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/MEM/BF"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
)

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