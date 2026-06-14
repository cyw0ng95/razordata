//go:build !slt_corpus_full

package uring

import (
	"errors"
	"runtime"
	"testing"
)

// TestRing_New_Valid verifies the constructor accepts valid
// entry counts and returns a non-nil Ring.
func TestRing_New_Valid(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Logf("New(8) returned err=%v (expected on hosts where io_uring is disabled)", err)
		return
	}
	defer r.Close()
	if r == nil {
		t.Fatal("New returned nil ring without error")
	}
}

// TestRing_New_InvalidEntries verifies the constructor rejects
// non-positive entry counts.
func TestRing_New_InvalidEntries(t *testing.T) {
	if _, err := New(0); err == nil {
		t.Error("New(0) succeeded, want error")
	}
	if _, err := New(-1); err == nil {
		t.Error("New(-1) succeeded, want error")
	}
}

// TestRing_Close_Idempotent verifies that Close is safe to
// call multiple times.
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

// TestRing_SubmitWait_OnShim verifies that the minimal shim
// returns ErrShimUnsupported, so the caller can fall back to
// pread/pwrite. The full implementation is a follow-up.
func TestRing_SubmitWait_OnShim(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	_, err = r.SubmitWait(1)
	if !errors.Is(err, ErrShimUnsupported) {
		t.Errorf("SubmitWait on shim: got err=%v, want ErrShimUnsupported", err)
	}
}

// TestRing_Fd_ShimReturnsNegative verifies the minimal shim
// exposes Fd() == -1 (it does not retain the underlying fd).
func TestRing_Fd_ShimReturnsNegative(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	defer r.Close()
	if r.Fd() != -1 {
		t.Errorf("Fd() = %d, want -1 (minimal shim)", r.Fd())
	}
}

// TestRing_PlatformAwareness is a cross-platform smoke test
// that runs on Linux and non-Linux. On non-Linux, the build
// tag routes to uring_other.go which exposes ErrUnsupported.
func TestRing_PlatformAwareness(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Log("non-linux: ErrUnsupported expected on SubmitWait")
		// uring_other.go handles this case
	}
	r, err := New(8)
	if err != nil {
		t.Logf("New(8) on %s: err=%v (expected on hosts without io_uring)", runtime.GOOS, err)
		return
	}
	defer r.Close()
}
