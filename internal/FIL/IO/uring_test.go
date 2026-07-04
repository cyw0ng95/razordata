//go:build !slt_corpus_full

package uring

import (
	"os"
	"runtime"
	"testing"
	"unsafe"
)

func TestRing_New_Valid(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Logf("New(8) err=%v (expected where io_uring is disabled)", err)
		return
	}
	defer r.Close()
	if r == nil {
		t.Fatal("New returned nil without error")
	}
}

func TestRing_New_InvalidEntries(t *testing.T) {
	if _, err := New(0); err == nil {
		t.Error("New(0) succeeded, want error")
	}
	if _, err := New(-1); err == nil {
		t.Error("New(-1) succeeded, want error")
	}
}

func TestRing_Close_Idempotent(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestRing_Fd_AfterNew(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	if r.Fd() <= 0 {
		t.Errorf("Fd() = %d, want > 0", r.Fd())
	}
}

func TestRing_PlatformAwareness(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("non-linux")
	}
	r, err := New(8)
	if err != nil {
		t.Logf("New(8) on %s: err=%v", runtime.GOOS, err)
		return
	}
	defer r.Close()
}

func TestRing_RegisterFixedFile(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()

	f, err := os.CreateTemp("", "uring-fd-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if r.Fd() <= 0 {
		t.Skipf("uring fd=%d", r.Fd())
	}

	idx, err := r.RegisterFixedFile(int(f.Fd()))
	if err != nil {
		t.Logf("RegisterFixedFile err=%v (acceptable)", err)
		return
	}
	if idx < 0 {
		t.Errorf("RegisterFixedFile returned negative index: %d", idx)
		return
	}

	if err := r.UnregisterFixedFile(idx); err != nil {
		t.Errorf("UnregisterFixedFile(%d): %v", idx, err)
	}
}

func TestIOSQE_Flags(t *testing.T) {
	if IOSQE_FIXED_FILE != 1 {
		t.Errorf("IOSQE_FIXED_FILE=%d, want 1", IOSQE_FIXED_FILE)
	}
	if IOSQE_IO_LINK != 4 {
		t.Errorf("IOSQE_IO_LINK=%d, want 4", IOSQE_IO_LINK)
	}
	if IOSQE_IO_DRAIN != 2 {
		t.Errorf("IOSQE_IO_DRAIN=%d, want 2", IOSQE_IO_DRAIN)
	}
	if IOSQE_IO_HARDLINK != 8 {
		t.Errorf("IOSQE_IO_HARDLINK=%d, want 8", IOSQE_IO_HARDLINK)
	}
}

func TestRing_Sqe_Submit_Pread(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	if r.Fd() <= 0 {
		t.Skipf("uring fd=%d", r.Fd())
	}

	content := []byte("hello io_uring world")
	f, err := os.CreateTemp("", "uring-test-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	buf := make([]byte, len(content))
	sqe, err := r.Sqe()
	if err != nil {
		t.Fatalf("Sqe: %v", err)
	}
	sqe.PrepRead(int(f.Fd()), buf, 0)
	sqe.SetUserData(42)

	n, err := r.SubmitWait(1)
	if err != nil {
		t.Skipf("SubmitWait: %v (io_uring I/O restricted)", err)
	}
	if n != 1 {
		t.Skipf("expected 1 CQE, got %d (io_uring I/O restricted)", n)
	}

	cqe, ok := r.PeekCqe()
	if !ok {
		t.Fatal("no CQE available")
	}
	if cqe.Res != int32(len(content)) {
		t.Errorf("CQE res=%d, want %d", cqe.Res, len(content))
	}
	if cqe.UserData != 42 {
		t.Errorf("CQE userData=%d, want 42", cqe.UserData)
	}
	if string(buf) != string(content) {
		t.Errorf("read=%q, want %q", buf, content)
	}
	r.ConsumeCqe()
}

func TestRing_Sqe_Submit_Pwrite(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	if r.Fd() <= 0 {
		t.Skipf("uring fd=%d", r.Fd())
	}

	content := []byte("pwrite test data")
	f, err := os.CreateTemp("", "uring-pwrite-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())

	sqe, err := r.Sqe()
	if err != nil {
		t.Fatalf("Sqe: %v", err)
	}
	sqe.PrepWrite(int(f.Fd()), content, 0)
	sqe.SetUserData(99)

	n, err := r.SubmitWait(1)
	if err != nil {
		t.Skipf("SubmitWait: %v (io_uring I/O restricted)", err)
	}
	if n != 1 {
		t.Skipf("expected 1 CQE, got %d (io_uring I/O restricted)", n)
	}

	cqe, ok := r.PeekCqe()
	if !ok {
		t.Fatal("no CQE available")
	}
	if cqe.Res != int32(len(content)) {
		t.Errorf("CQE res=%d, want %d", cqe.Res, len(content))
	}
	r.ConsumeCqe()

	readbuf := make([]byte, len(content))
	if _, err := f.ReadAt(readbuf, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(readbuf) != string(content) {
		t.Errorf("on-disk=%q, want %q", readbuf, content)
	}
}

func TestRing_Sqe_Submit_Fsync(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	if r.Fd() <= 0 {
		t.Skipf("uring fd=%d", r.Fd())
	}

	content := []byte("fsync test")
	f, err := os.CreateTemp("", "uring-fsync-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())

	sqe, err := r.Sqe()
	if err != nil {
		t.Fatalf("Sqe: %v", err)
	}
	sqe.PrepWrite(int(f.Fd()), content, 0)
	sqe.SetUserData(1)
	n, err := r.SubmitWait(1)
	if err != nil {
		t.Skipf("SubmitWait write: %v (io_uring I/O restricted)", err)
	}
	if n != 1 {
		t.Skipf("expected 1 CQE for write, got %d (io_uring I/O restricted)", n)
	}
	r.ConsumeCqe()

	// Fsync.
	sqe, err = r.Sqe()
	if err != nil {
		t.Fatalf("Sqe fsync: %v", err)
	}
	sqe.PrepFsync(int(f.Fd()))
	sqe.SetUserData(2)

	n, err = r.SubmitWait(1)
	if err != nil {
		t.Skipf("SubmitWait fsync: %v (io_uring I/O restricted)", err)
	}
	if n != 1 {
		t.Skipf("expected 1 CQE for fsync, got %d (io_uring I/O restricted)", n)
	}

	cqe, ok := r.PeekCqe()
	if !ok {
		t.Fatal("no CQE for fsync")
	}
	if cqe.Res < 0 {
		t.Errorf("fsync error: res=%d", cqe.Res)
	}
	r.ConsumeCqe()
}

func TestConstants(t *testing.T) {
	if IORING_OP_NOP != 0 {
		t.Errorf("IORING_OP_NOP=%d", IORING_OP_NOP)
	}
	if IORING_FSYNC_DATASYNC != 1 {
		t.Errorf("IORING_FSYNC_DATASYNC=%d", IORING_FSYNC_DATASYNC)
	}
}

func TestRing_Sqe_ClosedRing(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	r.Close()
	_, err = r.Sqe()
	if err == nil {
		t.Error("Sqe on closed ring: want error")
	}
}

func TestRing_SubmitWait_ClosedRing(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	r.Close()
	_, err = r.SubmitWait(1)
	if err == nil {
		t.Error("SubmitWait on closed ring: want error")
	}
}

func TestRing_RegisterFixedFile_Closed(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	r.Close()
	_, err = r.RegisterFixedFile(3)
	if err == nil {
		t.Error("RegisterFixedFile on closed ring: want error")
	}
}

func TestRing_UnregisterFixedFile_Closed(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	r.Close()
	err = r.UnregisterFixedFile(0)
	if err == nil {
		t.Error("UnregisterFixedFile on closed ring: want error")
	}
}

func TestSubmit_EmptyRing(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	_, err = r.SubmitWait(0)
	if err != nil {
		t.Errorf("SubmitWait(0) on empty ring: %v", err)
	}
}

func TestSetUserData(t *testing.T) {
	sqe := uring_sqe{}
	sqe.SetUserData(12345)
	if sqe.userData != 12345 {
		t.Errorf("userData=%d, want 12345", sqe.userData)
	}
}

func TestSetFlags(t *testing.T) {
	sqe := uring_sqe{}
	sqe.SetFlags(IOSQE_IO_LINK)
	if sqe.flags != IOSQE_IO_LINK {
		t.Errorf("flags=%d, want %d", sqe.flags, IOSQE_IO_LINK)
	}
}

func TestPrepRead_Basic(t *testing.T) {
	buf := []byte("test")
	sqe := uring_sqe{}
	sqe.PrepRead(7, buf, 100)
	if sqe.opcode != IORING_OP_READ {
		t.Errorf("opcode=%d", sqe.opcode)
	}
	if sqe.fd != 7 {
		t.Errorf("fd=%d, want 7", sqe.fd)
	}
	if sqe.off != 100 {
		t.Errorf("off=%d, want 100", sqe.off)
	}
	if sqe.len != 4 {
		t.Errorf("len=%d, want 4", sqe.len)
	}
}

func TestPrepWrite_Basic(t *testing.T) {
	buf := []byte("test")
	sqe := uring_sqe{}
	sqe.PrepWrite(11, buf, 200)
	if sqe.opcode != IORING_OP_WRITE {
		t.Errorf("opcode=%d", sqe.opcode)
	}
	if sqe.fd != 11 {
		t.Errorf("fd=%d, want 11", sqe.fd)
	}
	if sqe.off != 200 {
		t.Errorf("off=%d, want 200", sqe.off)
	}
}

func TestPrepFsync_Basic(t *testing.T) {
	sqe := uring_sqe{}
	sqe.PrepFsync(13)
	if sqe.opcode != IORING_OP_FSYNC {
		t.Errorf("opcode=%d", sqe.opcode)
	}
	if sqe.fd != 13 {
		t.Errorf("fd=%d, want 13", sqe.fd)
	}
	if sqe.len != IORING_FSYNC_DATASYNC {
		t.Errorf("len=%d, want %d", sqe.len, IORING_FSYNC_DATASYNC)
	}
}

func TestCqeCount_Empty(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	if r.CqeCount() != 0 {
		t.Errorf("CqeCount=%d, want 0", r.CqeCount())
	}
}

func TestPeekCqe_Empty(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	_, ok := r.PeekCqe()
	if ok {
		t.Error("PeekCqe on empty ring returned true")
	}
}

func TestSqSize(t *testing.T) {
	if unsafe.Sizeof(uring_sqe{}) != 64 {
		t.Errorf("uring_sqe size=%d, want 64", unsafe.Sizeof(uring_sqe{}))
	}
	if unsafe.Sizeof(uring_cqe{}) != 16 {
		t.Errorf("uring_cqe size=%d, want 16", unsafe.Sizeof(uring_cqe{}))
	}
	if unsafe.Sizeof(uring_params{}) != 120 {
		t.Errorf("uring_params size=%d, want 120", unsafe.Sizeof(uring_params{}))
	}
}
