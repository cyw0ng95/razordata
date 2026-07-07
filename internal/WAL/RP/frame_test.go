package rp

import (
	"bytes"
	"errors"
	"testing"

	"github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// TestInspectFrameParsesHeader ensures the replayer-side helper
// correctly parses a frame produced by wr.AppendFrame. REQ001297.
func TestInspectFrameParsesHeader(t *testing.T) {
	page := bytes.Repeat([]byte{0x42}, wr.WalPageSize)
	blob := wr.AppendFrame(nil, page, 17, 42, 0xC0FFEE01, 0xBADC0DE2)

	pf, err := InspectFrame(blob)
	if err != nil {
		t.Fatalf("InspectFrame: %v", err)
	}
	if pf.Hdr.PageNo != 17 {
		t.Errorf("PageNo = %d, want 17", pf.Hdr.PageNo)
	}
	if pf.Hdr.DbSizeAfterCommit != 42 {
		t.Errorf("DbSizeAfterCommit = %d, want 42", pf.Hdr.DbSizeAfterCommit)
	}
	if !bytes.Equal(pf.Page, page) {
		t.Error("page body not equal to input")
	}
}

// TestVerifyFrameBytesAcceptsValidFrame covers the happy path of
// VerifyFrameBytes: same salt pair, intact body, correct checksum.
// REQ001297, REQ001298.
func TestVerifyFrameBytesAcceptsValidFrame(t *testing.T) {
	const salt1, salt2 uint32 = 0xC0FFEE01, 0xBADC0DE2
	page := bytes.Repeat([]byte{0x99}, wr.WalPageSize)
	blob := wr.AppendFrame(nil, page, 1, 1, salt1, salt2)
	if err := VerifyFrameBytes(blob, salt1, salt2); err != nil {
		t.Fatalf("VerifyFrameBytes: %v", err)
	}
}

// TestVerifyFrameBytesRejectsSaltMismatch rejects a frame whose
// salt pair disagrees with the WAL header. REQ001297.
func TestVerifyFrameBytesRejectsSaltMismatch(t *testing.T) {
	page := bytes.Repeat([]byte{0x33}, wr.WalPageSize)
	blob := wr.AppendFrame(nil, page, 1, 1, 0xC0FFEE01, 0xBADC0DE2)
	// Submit with a different salt pair — should fail with
	// ErrFrameBadSalt propagated from wr.
	if err := VerifyFrameBytes(blob, 0xAAAA, 0xBBBB); err == nil ||
		!errors.Is(err, wr.ErrFrameBadSalt) {
		t.Fatalf("expected ErrFrameBadSalt, got %v", err)
	}
}

// TestVerifyFrameBytesRejectsBitFlip in the page body surfaces as
// ErrFrameChecksumMismatch — the contract REQ001298 specifies.
// REQ001298.
func TestVerifyFrameBytesRejectsBitFlip(t *testing.T) {
	const salt1, salt2 uint32 = 0xC0FFEE01, 0xBADC0DE2
	page := bytes.Repeat([]byte{0x77}, wr.WalPageSize)
	blob := wr.AppendFrame(nil, page, 1, 1, salt1, salt2)
	bad := append([]byte(nil), blob...)
	bad[wr.WalFrameHdrSize+128] ^= 0x01
	if err := VerifyFrameBytes(bad, salt1, salt2); err == nil ||
		!errors.Is(err, wr.ErrFrameChecksumMismatch) {
		t.Fatalf("expected ErrFrameChecksumMismatch, got %v", err)
	}
}

// TestVerifyFrameBytesRejectsTruncatedFrame rejects a frame blob
// whose size falls below WalFrameHdrSize, and a frame whose page
// body is shorter than PageSize. REQ001297.
func TestVerifyFrameBytesRejectsTruncatedFrame(t *testing.T) {
	const salt1, salt2 uint32 = 0xC0FFEE01, 0xBADC0DE2
	if err := VerifyFrameBytes(nil, salt1, salt2); err == nil ||
		!errors.Is(err, ErrFrameShortBuffer) {
		t.Fatalf("nil buffer: expected ErrFrameShortBuffer, got %v", err)
	}
	if err := VerifyFrameBytes(make([]byte, wr.WalFrameHdrSize-1), salt1, salt2); err == nil ||
		!errors.Is(err, ErrFrameShortBuffer) {
		t.Fatalf("short buffer: expected ErrFrameShortBuffer, got %v", err)
	}
	// Valid header but body shorter than PageSize.
	blob := wr.AppendFrame(nil, bytes.Repeat([]byte{0x55}, wr.WalPageSize/2), 1, 1, salt1, salt2)
	if err := VerifyFrameBytes(blob, salt1, salt2); err == nil ||
		!errors.Is(err, ErrPageBodyWrongSize) {
		t.Fatalf("short body: expected ErrPageBodyWrongSize, got %v", err)
	}
}

// TestVerifyCommitMarkerAcceptsEncoding covers the happy path of
// VerifyCommitMarkerBytes — encoding round-trip. REQ001298.
func TestVerifyCommitMarkerAcceptsEncoding(t *testing.T) {
	marker := wr.EncodeCommitMarker(nil, 0xCAFEF00D, 42)
	if err := VerifyCommitMarkerBytes(marker); err != nil {
		t.Fatalf("VerifyCommitMarkerBytes: %v", err)
	}
}
