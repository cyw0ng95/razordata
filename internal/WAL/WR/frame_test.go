package wr

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// TestWalFrameHeaderSize pins the SQLite journal layout constants.
// Bumping these without a coordinated format migration means reader
// and writer disagree on offsets and silent corruption becomes
// possible. REQ001297.
func TestWalFrameHeaderSize(t *testing.T) {
	if WalHdrSize != 24 {
		t.Fatalf("WalHdrSize = %d, want 24", WalHdrSize)
	}
	if WalFrameHdrSize != 24 {
		t.Fatalf("WalFrameHdrSize = %d, want 24", WalFrameHdrSize)
	}
}

// TestWalMagicBigEndian proves the WAL header magic is written as
// big-endian 0x574F4C47 (ASCII "WLOG" read left-to-right). This is
// the SQLite convention and any reader created later will rely on it.
// REQ001297.
func TestWalMagicBigEndian(t *testing.T) {
	buf := EncodeWalHdr(WalHdr{Magic: WalMagic})
	if !bytes.Equal(buf[0:4], []byte{'W', 'L', 'O', 'G'}) {
		t.Fatalf("magic = %v, want %q", buf[0:4], "WLOG")
	}
}

// TestWalHdrRoundTrip covers full header serialization coverage. It
// rejects truncated buffers, bad magic, bad version, and bad
// page-size, while confirming a well-formed header survives a
// round-trip. REQ001297.
func TestWalHdrRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*WalHdr)
		wantErr error
	}{
		{"baseline", func(*WalHdr) {}, nil},
		{"bad magic", func(h *WalHdr) { h.Magic = 0xDEADBEEF }, ErrWalBadMagic},
		{"zero version", func(h *WalHdr) { h.Version = 0 }, ErrWalBadVersion},
		{"future version", func(h *WalHdr) { h.Version = WalVersionV1 + 1 }, ErrWalBadVersion},
		{"odd page size", func(h *WalHdr) { h.PageSize = 4097 }, ErrWalBadPageSize},
		{"too small page size", func(h *WalHdr) { h.PageSize = 256 }, ErrWalBadPageSize},
		{"too large page size", func(h *WalHdr) { h.PageSize = 1 << 17 }, ErrWalBadPageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := WalHdr{
				Magic:         WalMagic,
				Version:       WalVersionV1,
				PageSize:      WalPageSize,
				CheckpointSeq: 7,
				Salt1:         0xC0FFEE01,
				Salt2:         0xBADC0DE2,
			}
			tc.mutate(&h)
			blob := EncodeWalHdr(h)
			got, err := DecodeWalHdr(blob)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != h {
					t.Fatalf("round-trip mismatch: got %+v want %+v", got, h)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error chain: got %v, want wraps %v", err, tc.wantErr)
			}
		})
	}
}

// TestWalHdrTruncatedInput covers DecodeWalHdr input validation: a
// buffer shorter than 24 bytes must return an error rather than
// reading off the end. REQ001297.
func TestWalHdrTruncatedInput(t *testing.T) {
	for n := 0; n < WalHdrSize; n++ {
		buf := make([]byte, n)
		if _, err := DecodeWalHdr(buf); err == nil {
			t.Fatalf("expected error for buffer len=%d, got nil", n)
		}
	}
}

// TestFrameHdrRoundTrip verifies a frame header survives
// serialization without mutating fields. Checksums are not yet
// computed here; set them to zero. REQ001297.
func TestFrameHdrRoundTrip(t *testing.T) {
	f := WalFrameHdr{
		PageNo:           17,
		DbSizeAfterCommit: 42,
		Salt1:            0xC0FFEE01,
		Salt2:            0xBADC0DE2,
	}
	blob := EncodeFrameHdr(f)
	if len(blob) != WalFrameHdrSize {
		t.Fatalf("encoded frame header = %d bytes, want %d", len(blob), WalFrameHdrSize)
	}
	got, err := DecodeFrameHdr(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PageNo != f.PageNo || got.DbSizeAfterCommit != f.DbSizeAfterCommit ||
		got.Salt1 != f.Salt1 || got.Salt2 != f.Salt2 {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, f)
	}
}

// TestFrameHeaderBigEndian verifies that PageNo is written
// big-endian — the first byte of a 4-byte big-endian uint32(17) is
// 0x00, not 0x11. REQ001297.
func TestFrameHeaderBigEndian(t *testing.T) {
	blob := EncodeFrameHdr(WalFrameHdr{PageNo: 17})
	if blob[0] != 0x00 || blob[1] != 0x00 || blob[2] != 0x00 || blob[3] != 0x11 {
		t.Fatalf("PageNo not big-endian: % x", blob[0:4])
	}
}

// TestAppendFrame computes checksums in-band, round-trips, and
// detects a bit flip in the page body. REQ001298.
func TestAppendFrame(t *testing.T) {
	page := bytes.Repeat([]byte{0xAB}, WalPageSize)
	dst := AppendFrame(nil, page, 1, 1, 0xC0FFEE01, 0xBADC0DE2)
	if len(dst) != WalFrameHdrSize+WalPageSize {
		t.Fatalf("appended frame len = %d, want %d", len(dst), WalFrameHdrSize+WalPageSize)
	}
	h, err := DecodeFrameHdr(dst[:WalFrameHdrSize])
	if err != nil {
		t.Fatalf("decode hdr: %v", err)
	}
	if h.PageNo != 1 || h.DbSizeAfterCommit != 1 {
		t.Fatalf("hdr fields wrong: %+v", h)
	}
	if err := VerifyFrame(h, dst[WalFrameHdrSize:]); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Flip a bit in the page body and ensure verification rejects.
	bad := append([]byte(nil), dst...)
	bad[WalFrameHdrSize+10] ^= 0x01
	h2, _ := DecodeFrameHdr(bad[:WalFrameHdrSize])
	if err := VerifyFrame(h2, bad[WalFrameHdrSize:]); !errors.Is(err, ErrFrameChecksumMismatch) {
		t.Fatalf("expected ErrFrameChecksumMismatch, got %v", err)
	}
}

// TestFrameChecksumDeterministic demonstrates that the same input
// produces the same checksums on repeated calls — a contract the
// tests rely on. REQ001298.
func TestFrameChecksumDeterministic(t *testing.T) {
	page := bytes.Repeat([]byte{0x5A}, WalPageSize)
	var hdr [WalFrameHdrSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], 1)   // PageNo
	binary.BigEndian.PutUint32(hdr[4:8], 1)   // DbSizeAfterCommit
	binary.BigEndian.PutUint32(hdr[8:12], 7)  // Salt1
	binary.BigEndian.PutUint32(hdr[12:16], 8) // Salt2
	c1a, c2a := FrameChecksum(hdr, page)
	c1b, c2b := FrameChecksum(hdr, page)
	if c1a != c1b || c2a != c2b {
		t.Fatalf("non-deterministic: (%08x,%08x) vs (%08x,%08x)", c1a, c2a, c1b, c2b)
	}
}

// TestWalFrameChecksum_Torn is the named test from REQ001298.
//
// A torn write is one where the kernel wrote part of a frame before
// a crash — the frame header is intact but the page body is missing
// trailing bytes, or vice-versa. The two-checksum scheme must reject
// either pattern. REQ001298.
//
// We simulate torn writes by appending a frame, then truncating its
// body and feeding the truncated slice to VerifyFrame.
func TestWalFrameChecksum_Torn(t *testing.T) {
	page := bytes.Repeat([]byte{0xCD}, WalPageSize)
	full := AppendFrame(nil, page, 7, 7, 0xC0FFEE01, 0xBADC0DE2)

	// 1) header intact, body truncated (last 1 KiB missing).
	hdr, _ := DecodeFrameHdr(full[:WalFrameHdrSize])
	tornBody := full[WalFrameHdrSize : len(full)-1024]
	if err := VerifyFrame(hdr, tornBody); !errors.Is(err, ErrFrameChecksumMismatch) {
		t.Fatalf("truncated body: expected ErrFrameChecksumMismatch, got %v", err)
	}

	// 2) header partially intact (checksum slots overwritten), body
	// intact. Construct: keep page, zero out the header's checksum
	// slots, and expect VerifyFrame to reject.
	full2 := AppendFrame(nil, page, 8, 8, 0xC0FFEE01, 0xBADC0DE2)
	hdrBytes := full2[:WalFrameHdrSize]
	binary.BigEndian.PutUint32(hdrBytes[16:20], 0xDEADBEEF) // Checksum1 overwritten
	hdrTorn, _ := DecodeFrameHdr(hdrBytes)
	if err := VerifyFrame(hdrTorn, full2[WalFrameHdrSize:]); !errors.Is(err, ErrFrameChecksumMismatch) {
		t.Fatalf("torn header: expected ErrFrameChecksumMismatch, got %v", err)
	}
}

// TestCommitMarker covers REQ001298: the commit marker is
// salt1||frameCount||c1||c2 (16 bytes BE) using init-then-fold.
// Round-trip succeeds; tampering is rejected.
func TestCommitMarker(t *testing.T) {
	const salt1 uint32 = 0xCAFEF00D
	const nframes uint32 = 13
	marker := EncodeCommitMarker(nil, salt1, nframes)
	if len(marker) != 16 {
		t.Fatalf("marker len = %d, want 16", len(marker))
	}
	if err := VerifyCommitMarker(marker); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Tamper with frameCount.
	tampered := append([]byte(nil), marker...)
	binary.BigEndian.PutUint32(tampered[4:8], nframes+1)
	if err := VerifyCommitMarker(tampered); !errors.Is(err, ErrCommitMarkerChecksumMismatch) {
		t.Fatalf("tampered: expected ErrCommitMarkerChecksumMismatch, got %v", err)
	}

	// Wrong length.
	if err := VerifyCommitMarker(marker[:7]); err == nil ||
		!strings.Contains(err.Error(), "commit marker length") {
		t.Fatalf("expected length error, got %v", err)
	}
}
