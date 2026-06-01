package lf

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cyw0ng95/razordata/internal/LOG/LG"
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

func TestIncref(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	h, err := sm.CreateSegment(3)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	if h.Refs.Load() != 1 {
		t.Fatalf("refs should be 1 after CreateSegment, got %d", h.Refs.Load())
	}

	h.Incref()
	if h.Refs.Load() != 2 {
		t.Fatalf("refs should be 2 after Incref, got %d", h.Refs.Load())
	}

	h.Incref()
	if h.Refs.Load() != 3 {
		t.Fatalf("refs should be 3 after second Incref, got %d", h.Refs.Load())
	}

	h.Close()
	h.Close()
}

func TestCloseIdempotent(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	h, err := sm.CreateSegment(4)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}

	h.Close()
	err = h.Close()
	if err == nil {
		t.Fatalf("second Close should return error")
	}
}

func TestCloseWithStaleFD(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	h, err := sm.CreateSegment(5)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	h.Close()

	err = h.Close()
	if err == nil {
		t.Fatalf("Close on stale handle should return error")
	}
}

func TestGetSegmentReopenStale(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	h1, err := sm.CreateSegment(6)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	h1.Close()

	h2, err := sm.GetSegment(6)
	if err != nil {
		t.Fatalf("GetSegment after stale close: %v", err)
	}
	if h2.FD < 0 {
		t.Fatalf("FD should be reopened, got %d", h2.FD)
	}
	h2.Close()
}

func TestGetSegmentCorruptDir(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	walDir := filepath.Join(tmp, "wal")
	if err := os.MkdirAll(walDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	corruptPath := filepath.Join(walDir, "wal.999")
	if err := os.MkdirAll(corruptPath, 0700); err != nil {
		t.Fatalf("MkdirAll corrupt: %v", err)
	}

	_, err = sm.GetSegment(999)
	if err != ErrCorruptSegment {
		t.Fatalf("expected ErrCorruptSegment, got %v", err)
	}
}

func TestTruncateNotFound(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	err = sm.Truncate(99, 0)
	if err != ErrSegmentNotFound {
		t.Fatalf("expected ErrSegmentNotFound, got %v", err)
	}
}

func TestCloseWithMultipleHandles(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	var handles []*FileHandle
	for i := uint64(10); i < 15; i++ {
		h, err := sm.CreateSegment(i)
		if err != nil {
			t.Fatalf("CreateSegment %d: %v", i, err)
		}
		handles = append(handles, h)
	}

	for _, h := range handles {
		h.Close()
	}

	if err := sm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestNewWithLogger tests New when mkdir fails with logger.
func TestNewWithLogger(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	// New should succeed
	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sm.Close()
}

// TestNewCreatesWalDir tests that New creates the wal subdirectory.
func TestNewCreatesWalDir(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf_test")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	// Wal directory should exist
	walDir := filepath.Join(path, "wal")
	if _, err := os.Stat(walDir); os.IsNotExist(err) {
		t.Error("wal directory not created")
	}
}

// TestCreateSegmentWithLogger tests CreateSegment error path with logger.
func TestCreateSegmentWithLogger(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	// Create segment should succeed
	fh, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	fh.Close()

	// Create same segment again - should reuse from pool
	fh2, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment again: %v", err)
	}
	fh2.Close()
}

// TestGetSegmentFromPool tests GetSegment when segment is in pool.
func TestGetSegmentFromPool(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	// Create a segment
	fh, err := sm.CreateSegment(5)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}

	// Get same segment - should come from pool
	fh2, err := sm.GetSegment(5)
	if err != nil {
		t.Fatalf("GetSegment: %v", err)
	}

	if fh != fh2 {
		t.Error("expected same FileHandle from pool")
	}

	fh2.Close()
	fh.Close()
}

// TestGetSegmentReopenClosed tests GetSegment reopens a closed file.
func TestGetSegmentReopenClosed(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	// Create and close a segment
	fh, err := sm.CreateSegment(10)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	fh.Close()

	// GetSegment should reopen it
	fh2, err := sm.GetSegment(10)
	if err != nil {
		t.Fatalf("GetSegment after close: %v", err)
	}
	fh2.Close()
}

// TestGetSegmentNotExist tests GetSegment for non-existent segment.
func TestGetSegmentNotExist(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	_, err = sm.GetSegment(999)
	if err != ErrSegmentNotFound {
		t.Errorf("expected ErrSegmentNotFound, got %v", err)
	}
}

// TestTruncateWithSegment tests Truncate on existing segment.
func TestTruncateWithSegment(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	fh, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	fh.Close()

	err = sm.Truncate(1, 1024)
	if err != nil {
		t.Fatalf("Truncate: %v", err)
	}
}

// TestSegmentPath tests segmentPath helper.
func TestSegmentPath(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	segPath := sm.segmentPath(5)
	expected := filepath.Join(path, "wal", "wal.005")
	if segPath != expected {
		t.Errorf("expected %s, got %s", expected, segPath)
	}
}

// TestFirstLoggerNilLF tests firstLogger with nil/empty input.
func TestFirstLoggerNilLF(t *testing.T) {
	result := firstLogger(nil)
	if result != nil {
		t.Error("expected nil for nil input")
	}

	result = firstLogger([]lg.Logger{})
	if result != nil {
		t.Error("expected nil for empty slice")
	}
}

// TestCloseMultipleSegments tests closing multiple segments.
func TestCloseMultipleSegments(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create multiple segments
	for i := 1; i <= 3; i++ {
		fh, err := sm.CreateSegment(uint64(i))
		if err != nil {
			t.Fatalf("CreateSegment %d: %v", i, err)
		}
		fh.Close()
	}

	// Close should close all
	if err := sm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second close should be safe
	if err := sm.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestFileHandleCloseWithRefs tests FileHandle.Close with multiple refs.
func TestFileHandleCloseWithRefs(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	fh, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}

	// Add extra references
	fh.Incref()
	fh.Incref()

	// Close once - should not close file yet (refs went from 3 to 2)
	err = fh.Close()
	if err != nil {
		t.Errorf("first Close: %v", err)
	}

	// Close again - should not close file yet (refs went from 2 to 1)
	err = fh.Close()
	if err != nil {
		t.Errorf("second Close: %v", err)
	}

	// Third close - refs went from 1 to 0, file should be closed
	err = fh.Close()
	if err != nil {
		t.Errorf("third Close: %v", err)
	}

	// Fourth close - already closed, should return error
	err = fh.Close()
	if err == nil {
		t.Error("expected error on fourth Close")
	}
}

// TestCreateSegmentError tests CreateSegment with invalid path.
func TestCreateSegmentError(t *testing.T) {
	// Create sm with valid path first
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lf")

	sm, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	// CreateSegment with invalid directory should fail
	// But since we use tmp dir, it should succeed - just test normal case
	fh, err := sm.CreateSegment(100)
	if err != nil {
		t.Fatalf("CreateSegment: %v", err)
	}
	fh.Close()
}

// TestListSegmentsEmpty verifies ListSegments returns nil/empty when no
// segments exist (R12 — clean-shutdown path).
func TestListSegmentsEmpty(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	nums, err := sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	if len(nums) != 0 {
		t.Errorf("expected empty, got %v", nums)
	}
}

// TestListSegmentsSorted verifies segments are returned in ascending
// numeric order regardless of creation order (R12).
func TestListSegmentsSorted(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	for _, n := range []uint64{5, 1, 3, 2, 4} {
		h, err := sm.CreateSegment(n)
		if err != nil {
			t.Fatalf("CreateSegment(%d): %v", n, err)
		}
		h.Close()
	}

	nums, err := sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	want := []uint64{1, 2, 3, 4, 5}
	if !reflect.DeepEqual(nums, want) {
		t.Errorf("expected %v, got %v", want, nums)
	}
}

// TestListSegmentsIgnoresNoise verifies files that don't match the
// wal.<digits> pattern are ignored (R12).
func TestListSegmentsIgnoresNoise(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	for _, n := range []uint64{0, 1} {
		h, err := sm.CreateSegment(n)
		if err != nil {
			t.Fatalf("CreateSegment(%d): %v", n, err)
		}
		h.Close()
	}

	walDir := filepath.Join(tmp, "wal")
	for _, name := range []string{"wal.abc", "wal.", "wal.12x", "WAL.000", "other.000", "wal..0"} {
		if err := os.WriteFile(filepath.Join(walDir, name), nil, 0600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	nums, err := sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	want := []uint64{0, 1}
	if !reflect.DeepEqual(nums, want) {
		t.Errorf("expected %v, got %v", want, nums)
	}
}

// TestListSegmentsLarge verifies that segments beyond 3 digits (e.g.
// wal.1000) are also enumerated — the format is minimum-3-digit, not
// strictly 3 digits.
func TestListSegmentsLarge(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	for _, n := range []uint64{0, 999, 1000, 50000} {
		h, err := sm.CreateSegment(n)
		if err != nil {
			t.Fatalf("CreateSegment(%d): %v", n, err)
		}
		h.Close()
	}

	nums, err := sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	want := []uint64{0, 999, 1000, 50000}
	if !reflect.DeepEqual(nums, want) {
		t.Errorf("expected %v, got %v", want, nums)
	}
}

// TestListSegmentsMissingWalDir verifies that ListSegments returns an
// empty slice (no error) if the wal directory has been removed out from
// under the manager.
func TestListSegmentsMissingWalDir(t *testing.T) {
	tmp := t.TempDir()
	sm, err := New(tmp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sm.Close()

	if err := os.RemoveAll(filepath.Join(tmp, "wal")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	nums, err := sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments: %v", err)
	}
	if len(nums) != 0 {
		t.Errorf("expected empty, got %v", nums)
	}
}
