package lf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSegment(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	h, err := sm.CreateSegment(0)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if h.FD < 0 {
		t.Fatalf("FD should be non-negative")
	}
	h.Close()
}

func TestSegmentNaming(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	cases := []struct {
		n      uint64
		suffix string
	}{
		{0, "wal.000"},
		{1, "wal.001"},
		{99, "wal.099"},
		{100, "wal.100"},
		{999, "wal.999"},
	}

	for _, tc := range cases {
		h, err := sm.CreateSegment(tc.n)
		if err != nil {
			t.Fatalf("CreateSegment(%d): %v", tc.n, err)
		}
		h.Close()

		wantPath := filepath.Join(tmp, "wal", tc.suffix)
		if h.Path != wantPath {
			t.Fatalf("segment path for %d: got %s, want %s", tc.n, h.Path, wantPath)
		}
	}
}

func TestGetSegment(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	// Create segment 5.
	h1, err := sm.CreateSegment(5)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	h1.Close()

	// GetSegment should find it.
	h2, err := sm.GetSegment(5)
	if err != nil {
		t.Fatalf("GetSegment: %v", err)
	}
	h2.Close()
}

func TestGetSegmentNotFound(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	_, err = sm.GetSegment(99)
	if err != ErrSegmentNotFound {
		t.Fatalf("expected ErrSegmentNotFound, got %v", err)
	}
}

func TestTruncate(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	h, err := sm.CreateSegment(0)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	h.Close()

	// Write some data, then truncate to zero.
	path := filepath.Join(tmp, "wal", "wal.000")
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	f.Write([]byte("some data"))
	f.Close()

	if err := sm.Truncate(0, 0); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected size 0 after truncate, got %d", info.Size())
	}
}

func TestWalSubdirCreated(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	_, err = os.Stat(filepath.Join(tmp, "wal"))
	if err != nil {
		t.Fatalf("wal/ subdir should be created: %v", err)
	}
}

func TestRefcount(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	h1, err := sm.CreateSegment(7)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if h1.Refs.Load() != 1 {
		t.Fatalf("expected refs=1 after CreateSegment, got %d", h1.Refs.Load())
	}

	h2, err := sm.GetSegment(7)
	if err != nil {
		t.Fatalf("GetSegment: %v", err)
	}
	if h2.Refs.Load() != 2 {
		t.Fatalf("expected refs=2 after GetSegment, got %d", h2.Refs.Load())
	}

	h2.Close()
	if h1.Refs.Load() != 1 {
		t.Fatalf("expected refs=1 after h2.Close, got %d", h1.Refs.Load())
	}

	h1.Close()
	if h1.FD != -1 {
		t.Fatalf("FD should be -1 after final Close")
	}
}
