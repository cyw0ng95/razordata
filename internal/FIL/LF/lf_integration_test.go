package lf

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSegmentManager_CreateAndGet(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sm.Close()

	seg, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment failed: %v", err)
	}
	defer seg.Close()

	data := []byte("hello world")
	n, err := unix.Pwrite(seg.FD, data, 0)
	if err != nil {
		t.Fatalf("Pwrite failed: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected %d bytes written, got %d", len(data), n)
	}

	seg2, err := sm.GetSegment(1)
	if err != nil {
		t.Fatalf("GetSegment failed: %v", err)
	}
	defer seg2.Close()

	buf := make([]byte, len(data))
	n, err = unix.Pread(seg2.FD, buf, 0)
	if err != nil {
		t.Fatalf("Pread failed: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected %d bytes read, got %d", len(data), n)
	}
	if string(buf) != string(data) {
		t.Fatalf("data mismatch: expected %q, got %q", data, buf)
	}
}

func TestSegmentManager_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := sm.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := sm.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestSegmentManager_ListSegments(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sm.Close()

	for i := uint64(1); i <= 5; i++ {
		_, err := sm.CreateSegment(i)
		if err != nil {
			t.Fatalf("CreateSegment %d failed: %v", i, err)
		}
	}

	segments, err := sm.ListSegments()
	if err != nil {
		t.Fatalf("ListSegments failed: %v", err)
	}

	if len(segments) != 5 {
		t.Fatalf("expected 5 segments, got %d", len(segments))
	}
}

func TestSegmentManager_MultipleSegments(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sm.Close()

	seg1, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment 1 failed: %v", err)
	}
	defer seg1.Close()

	seg2, err := sm.CreateSegment(2)
	if err != nil {
		t.Fatalf("CreateSegment 2 failed: %v", err)
	}
	defer seg2.Close()

	data1 := []byte("segment 1 data")
	data2 := []byte("segment 2 data")

	_, err = unix.Pwrite(seg1.FD, data1, 0)
	if err != nil {
		t.Fatalf("Pwrite seg1 failed: %v", err)
	}

	_, err = unix.Pwrite(seg2.FD, data2, 0)
	if err != nil {
		t.Fatalf("Pwrite seg2 failed: %v", err)
	}

	seg1Read, err := sm.GetSegment(1)
	if err != nil {
		t.Fatalf("GetSegment 1 failed: %v", err)
	}
	defer seg1Read.Close()

	seg2Read, err := sm.GetSegment(2)
	if err != nil {
		t.Fatalf("GetSegment 2 failed: %v", err)
	}
	defer seg2Read.Close()

	buf1 := make([]byte, len(data1))
	buf2 := make([]byte, len(data2))

	unix.Pread(seg1Read.FD, buf1, 0)
	unix.Pread(seg2Read.FD, buf2, 0)

	if string(buf1) != string(data1) {
		t.Fatalf("seg1 data mismatch")
	}
	if string(buf2) != string(data2) {
		t.Fatalf("seg2 data mismatch")
	}
}

func TestSegmentManager_Truncate(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sm.Close()

	seg, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment failed: %v", err)
	}
	defer seg.Close()

	data := make([]byte, 1024)
	unix.Pwrite(seg.FD, data, 0)

	if err := sm.Truncate(1, 512); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}
}

func TestSegmentManager_GetNonExistent(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sm.Close()

	_, err = sm.GetSegment(999)
	if err == nil {
		t.Fatalf("expected error for non-existent segment, got nil")
	}
}

func TestSegmentManager_Path(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sm.Close()

	seg, err := sm.CreateSegment(42)
	if err != nil {
		t.Fatalf("CreateSegment failed: %v", err)
	}
	defer seg.Close()

	expectedPath := filepath.Join(dir, "wal", "wal.042")
	if seg.Path != expectedPath {
		t.Fatalf("expected path %q, got %q", expectedPath, seg.Path)
	}

	_, err = os.Stat(seg.Path)
	if err != nil {
		t.Fatalf("segment file does not exist at expected path: %v", err)
	}
}

func TestFileHandle_Incref(t *testing.T) {
	dir := t.TempDir()

	sm, err := New(dir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer sm.Close()

	seg, err := sm.CreateSegment(1)
	if err != nil {
		t.Fatalf("CreateSegment failed: %v", err)
	}
	defer seg.Close()

	initialRefs := seg.Refs.Load()
	seg.Incref()
	if seg.Refs.Load() != initialRefs+1 {
		t.Fatalf("expected refs %d, got %d", initialRefs+1, seg.Refs.Load())
	}
}
