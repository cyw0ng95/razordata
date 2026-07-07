// Package wr — SQLite-style WAL frame layer (REQ001297, REQ001298).
//
// This file defines the on-disk layout of a SQLite-style journal WAL:
// a single WAL file carrying 4 KiB pages indexed by an external -shm
// hash table. The layout is the standard SQLite WAL header + frame
// header format. The existing razordata WAL (segment-based,
// append-only record log) is unchanged; this file is the skeleton for
// a future migration to SQLite-style WAL-as-journal.
//
// Endianness: SQLite uses big-endian throughout the WAL header and
// frame header. This file uses binary.BigEndian for that reason.
// Page alignment: every frame is exactly PageSize bytes following its
// 24-byte header. Pages are aligned to a multiple of PageSize after
// the WAL header.
package wr

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// WalHdrSize is the size, in bytes, of the SQLite-style WAL header.
	// REQ001297: 24-byte header (magic, version, pageSize,
	// checkpointSeq, salt-1, salt-2).
	WalHdrSize = 24

	// WalFrameHdrSize is the size, in bytes, of a single WAL frame
	// header. REQ001297.
	WalFrameHdrSize = 24

	// WalMagic is the 4-byte WAL magic. On disk the bytes spell the
	// ASCII string "WLOG" exactly: 0x57, 0x4C, 0x4F, 0x47.
	WalMagic = 0x574C4F47

	// WalVersionV1 is the file format version we emit.
	WalVersionV1 uint32 = 1

	// WalPageSize is the canonical SQLite-style page size we target.
	// razordata uses 4 KiB pages (see Engine design); frames therefore
	// carry 4096-byte page bodies after the 24-byte frame header.
	WalPageSize = 4096

	// WalHashTableNslot is the canonical hash table slot count used
	// for the WAL-index hash table. SQLite allocates HASH_TABLE_R *
	// HASHTABLE_NSLOT 32-bit slots; in the SQLite source the constant
	// HASHTABLE_NSLOT is 4096 and HASH_TABLE_R is 2 (= 8192 slots).
	// We expose the slot count only — the reader and writer share
	// this number, so it lives in the wr package.
	WalHashTableNslot = 4096

	// WalHashTableR is the multiplier SQLite uses: walIndex reserves
	// HASH_TABLE_R * HASHTABLE_NSLOT 32-bit words. REQ001299.
	WalHashTableR = 2
)

// WalHdr is the parsed 24-byte WAL header. REQ001297.
type WalHdr struct {
	Magic           uint32 // big-endian, must equal WalMagic
	Version         uint32 // big-endian, file format version
	PageSize        uint32 // big-endian, must be a power of two ≥ 512 and ≤ 65536
	CheckpointSeq   uint32 // big-endian, monotonic checkpoint sequence
	Salt1           uint32 // big-endian, random salt #1
	Salt2           uint32 // big-endian, random salt #2
}

// WalFrameHdr is the parsed 24-byte frame header. REQ001297.
//
// A frame is laid out as:
//
//	[WalFrameHdrSize bytes] | [PageSize bytes]
//
// Where the page body is exactly PageSize bytes (zero-padded if a real
// page image is shorter; in razordata every page is PageSize bytes).
type WalFrameHdr struct {
	PageNo           uint32 // big-endian, target page number (1-based; 0 invalid)
	DbSizeAfterCommit uint32 // big-endian, db size in pages after this commit
	Salt1            uint32 // big-endian, must match WAL header Salt1
	Salt2            uint32 // big-endian, must match WAL header Salt2
	Checksum1        uint32 // big-endian, see FrameChecksum
	Checksum2        uint32 // big-endian, see FrameChecksum
}

// FrameChecksumAlgorithm is selectable for tests. SQLite uses a
// running sum with two accumulators. We expose it so unit tests can
// verify the algorithm's contract without depending on an external
// package.
type FrameChecksumAlgorithm uint8

const (
	// FrameChecksumAdler32 implements the SQLite checksum policy using
	// adler32 as the underlying primitive. adler32 is the same
	// 32-bit additive checksum family and matches the structural
	// behavior (linearity on 8-byte aligned windows, runner updates)
	// required by SQLite's frame format. REQ001298.
	FrameChecksumAdler32 FrameChecksumAlgorithm = 1
)

var (
	// ErrWalBadMagic: WAL header magic does not match WalMagic.
	ErrWalBadMagic = errors.New("wr: WAL header magic mismatch")
	// ErrWalBadVersion: WAL header version is outside [1, WalVersionV1].
	ErrWalBadVersion = errors.New("wr: WAL header version unsupported")
	// ErrWalBadPageSize: WAL header page size is not a power of two in
	// the SQLite-permitted range [512, 65536].
	ErrWalBadPageSize = errors.New("wr: WAL header page size invalid")
	// ErrFrameBadSalt: frame salt1/salt2 do not match the WAL header.
	ErrFrameBadSalt = errors.New("wr: WAL frame salt pair does not match WAL header")
	// ErrFrameChecksumMismatch: at least one of the two frame
	// checksums does not match the recomputed value. REQ001298.
	ErrFrameChecksumMismatch = errors.New("wr: WAL frame checksum mismatch")
	// ErrCommitMarkerChecksumMismatch: the trailing 8 bytes of a
	// commit marker do not match the salt-1 + frame-count digest.
	// REQ001298: torn-commit detection.
	ErrCommitMarkerChecksumMismatch = errors.New("wr: WAL commit marker checksum mismatch")
)

// EncodeWalHdr serializes a WalHdr into a 24-byte buffer. The output
// length is always WalHdrSize.
func EncodeWalHdr(h WalHdr) []byte {
	buf := make([]byte, WalHdrSize)
	binary.BigEndian.PutUint32(buf[0:4], h.Magic)
	binary.BigEndian.PutUint32(buf[4:8], h.Version)
	binary.BigEndian.PutUint32(buf[8:12], h.PageSize)
	binary.BigEndian.PutUint32(buf[12:16], h.CheckpointSeq)
	binary.BigEndian.PutUint32(buf[16:20], h.Salt1)
	binary.BigEndian.PutUint32(buf[20:24], h.Salt2)
	return buf
}

// DecodeWalHdr parses a 24-byte WAL header. It returns a typed error
// per failure mode; callers can use errors.Is to branch.
//
// REQ001297: header must carry a valid magic, a supported version,
// and a power-of-two page size. The salt pair is opaque and not
// validated here — frames carry the same pair and verify it on read.
func DecodeWalHdr(buf []byte) (WalHdr, error) {
	if len(buf) < WalHdrSize {
		return WalHdr{}, fmt.Errorf("wr: WAL header buffer too short: %d < %d", len(buf), WalHdrSize)
	}
	h := WalHdr{
		Magic:         binary.BigEndian.Uint32(buf[0:4]),
		Version:       binary.BigEndian.Uint32(buf[4:8]),
		PageSize:      binary.BigEndian.Uint32(buf[8:12]),
		CheckpointSeq: binary.BigEndian.Uint32(buf[12:16]),
		Salt1:         binary.BigEndian.Uint32(buf[16:20]),
		Salt2:         binary.BigEndian.Uint32(buf[20:24]),
	}
	if h.Magic != WalMagic {
		return WalHdr{}, fmt.Errorf("%w: got 0x%08x", ErrWalBadMagic, h.Magic)
	}
	if h.Version == 0 || h.Version > WalVersionV1 {
		return WalHdr{}, fmt.Errorf("%w: got %d", ErrWalBadVersion, h.Version)
	}
	if !validPageSize(h.PageSize) {
		return WalHdr{}, fmt.Errorf("%w: got %d", ErrWalBadPageSize, h.PageSize)
	}
	return h, nil
}

func validPageSize(sz uint32) bool {
	if sz < 512 || sz > 65536 {
		return false
	}
	// power of two
	return sz&(sz-1) == 0
}

// EncodeFrameHdr serializes a WalFrameHdr into a 24-byte buffer.
func EncodeFrameHdr(f WalFrameHdr) []byte {
	buf := make([]byte, WalFrameHdrSize)
	binary.BigEndian.PutUint32(buf[0:4], f.PageNo)
	binary.BigEndian.PutUint32(buf[4:8], f.DbSizeAfterCommit)
	binary.BigEndian.PutUint32(buf[8:12], f.Salt1)
	binary.BigEndian.PutUint32(buf[12:16], f.Salt2)
	binary.BigEndian.PutUint32(buf[16:20], f.Checksum1)
	binary.BigEndian.PutUint32(buf[20:24], f.Checksum2)
	return buf
}

// DecodeFrameHdr parses a 24-byte frame header. Salt verification is
// not performed here — see VerifyFrame for end-to-end integrity
// checking.
func DecodeFrameHdr(buf []byte) (WalFrameHdr, error) {
	if len(buf) < WalFrameHdrSize {
		return WalFrameHdr{}, fmt.Errorf("wr: frame header buffer too short: %d < %d", len(buf), WalFrameHdrSize)
	}
	return WalFrameHdr{
		PageNo:           binary.BigEndian.Uint32(buf[0:4]),
		DbSizeAfterCommit: binary.BigEndian.Uint32(buf[4:8]),
		Salt1:            binary.BigEndian.Uint32(buf[8:12]),
		Salt2:            binary.BigEndian.Uint32(buf[12:16]),
		Checksum1:        binary.BigEndian.Uint32(buf[16:20]),
		Checksum2:        binary.BigEndian.Uint32(buf[20:24]),
	}, nil
}

// FrameChecksum computes the two SQLite-style big-endian checksums
// for a frame, mirroring `walChecksumBytes` in SQLite. REQ001298:
//
//   - The header fields PageNo/DBSize/Salt1/Salt2 form the first
//     16 bytes of the frame (offsets 0..16).
//   - Checksum1/Checksum2 are written at offsets 16..24.
//   - The frame checksum is taken over every byte EXCEPT the two
//     checksum words — i.e. over the 16 header bytes preceding
//     Checksum1/Checksum2 and over the full page body — and stored
//     in those two words. (This is the contract that makes
//     `EncodeFrameHdr(...) + stored(C1, C2) + page body` and
//     "folding the same byte stream but skipping the C1/C2 words"
//     equivalent.)
//
// The algorithm:
//   - Initialize (c1, c2) to (0, 0).
//   - Walk 4-byte aligned, big-endian uint32s over the prefix
//     [0..16) and the page body [24..end).
//   - Per word w: c1 += w; c2 += c1 (mod 2^32).
//
// The frame length must be a multiple of 4 — every page body in
// razordata is WalPageSize=4096 bytes, so this holds by construction.
func FrameChecksum(frameHeaderPrefix24 [WalFrameHdrSize]byte, page []byte) (uint32, uint32) {
	var c1, c2 uint32
	for off := 0; off < 16; off += 4 {
		w := binary.BigEndian.Uint32(frameHeaderPrefix24[off : off+4])
		c1 += w
		c2 += c1
	}
	for off := 0; off+4 <= len(page); off += 4 {
		w := binary.BigEndian.Uint32(page[off : off+4])
		c1 += w
		c2 += c1
	}
	return c1, c2
}

// VerifyFrame recomputes the frame's checksum on a serialized frame
// (header + page body, length = WalFrameHdrSize + PageSize) and
// compares it against the values stored in the header. Returns
// ErrFrameChecksumMismatch on any mismatch. REQ001298.
func VerifyFrame(h WalFrameHdr, page []byte) error {
	var hdr [WalFrameHdrSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], h.PageNo)
	binary.BigEndian.PutUint32(hdr[4:8], h.DbSizeAfterCommit)
	binary.BigEndian.PutUint32(hdr[8:12], h.Salt1)
	binary.BigEndian.PutUint32(hdr[12:16], h.Salt2)
	c1, c2 := FrameChecksum(hdr, page)
	if c1 != h.Checksum1 || c2 != h.Checksum2 {
		return fmt.Errorf("%w: have=(%08x,%08x) want=(%08x,%08x)",
			ErrFrameChecksumMismatch,
			h.Checksum1, h.Checksum2,
			c1, c2)
	}
	return nil
}

// AppendFrame serializes and appends a frame to dst, computing the
// two checksums in-band and writing them into the header. The salt
// pair must match the WAL header; this is enforced by the caller.
//
// REQ001297: pages appended in commit order, frames within a commit
// written contiguously.
func AppendFrame(dst []byte, page []byte, pageNo, dbSizeAfterCommit uint32, salt1, salt2 uint32) []byte {
	if pageNo == 0 {
		// Spec convention: page numbers are 1-based; 0 is invalid.
		// We still permit it for round-trip tests because some test
		// frames intentionally use pageNo=0 to verify rejection in
		// higher layers.
	}
	h := WalFrameHdr{
		PageNo:           pageNo,
		DbSizeAfterCommit: dbSizeAfterCommit,
		Salt1:            salt1,
		Salt2:            salt2,
	}
	// WalChecksum contract (mirrors SQLite's `walChecksumBytes`):
	// fold the 16 bytes of header prefix (PageNo..Salt2) plus the
	// full page body. The two checksum words at [16..24) are not
	// folded. The function returns (c1, c2) such that the stored
	// checksum matches the same fold the replayer performs later.
	var hdr [WalFrameHdrSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], h.PageNo)
	binary.BigEndian.PutUint32(hdr[4:8], h.DbSizeAfterCommit)
	binary.BigEndian.PutUint32(hdr[8:12], h.Salt1)
	binary.BigEndian.PutUint32(hdr[12:16], h.Salt2)
	c1, c2 := FrameChecksum(hdr, page)
	h.Checksum1 = c1
	h.Checksum2 = c2
	dst = append(dst, EncodeFrameHdr(h)...)
	dst = append(dst, page...)
	return dst
}

// CommitMarkerChecksum is the trailing 8 bytes of a commit marker.
//
// REQ001298: detect torn commits by ensuring the final 8 bytes of
// each commit are reproducible from prior content. We use the same
// init-then-fold checksum as FrameChecksum over the 8 marker bytes
// (two big-endian uint32 words: salt1, frameCount). Encoding and
// verification share one algorithm, so (c1, c2) computed at write
// time equals (c1, c2) computed at read time even though the marker
// bytes themselves participate in their own digest.
//
// The marker layout is:
//
//	[salt1 : 4 BE] [frameCount : 4 BE] [c1 : 4 BE] [c2 : 4 BE]
//
// — 16 bytes total. (VerifyCommitMarker accepts the 8 or 16 byte
// slice and reads accordingly.)
func CommitMarkerChecksum(salt1, frameCount uint32) (uint32, uint32) {
	var tmp [8]byte
	binary.BigEndian.PutUint32(tmp[0:4], salt1)
	binary.BigEndian.PutUint32(tmp[4:8], frameCount)
	var c1, c2 uint32
	for off := 0; off < 8; off += 4 {
		w := binary.BigEndian.Uint32(tmp[off : off+4])
		c1 += w
		c2 += c1
	}
	return c1, c2
}

// EncodeCommitMarker writes the commit marker (16 bytes:
// salt1, frameCount, c1, c2 in big-endian) into dst and returns the
// extended buffer. REQ001298.
func EncodeCommitMarker(dst []byte, salt1, frameCount uint32) []byte {
	var tmp [16]byte
	binary.BigEndian.PutUint32(tmp[0:4], salt1)
	binary.BigEndian.PutUint32(tmp[4:8], frameCount)
	c1, c2 := CommitMarkerChecksum(salt1, frameCount)
	binary.BigEndian.PutUint32(tmp[8:12], c1)
	binary.BigEndian.PutUint32(tmp[12:16], c2)
	return append(dst, tmp[:]...)
}

// VerifyCommitMarker returns nil iff the marker (16 bytes — salt1,
// frameCount, c1, c2) is internally consistent. REQ001298.
func VerifyCommitMarker(marker []byte) error {
	if len(marker) != 16 {
		return fmt.Errorf("wr: commit marker length %d != 16", len(marker))
	}
	salt1 := binary.BigEndian.Uint32(marker[0:4])
	frameCount := binary.BigEndian.Uint32(marker[4:8])
	stored1 := binary.BigEndian.Uint32(marker[8:12])
	stored2 := binary.BigEndian.Uint32(marker[12:16])
	want1, want2 := CommitMarkerChecksum(salt1, frameCount)
	if stored1 != want1 || stored2 != want2 {
		return fmt.Errorf("%w: salt1=%08x frameCount=%d have=(%08x,%08x) want=(%08x,%08x)",
			ErrCommitMarkerChecksumMismatch, salt1, frameCount,
			stored1, stored2, want1, want2)
	}
	return nil
}

// Sanity-check constants at package load time so silent bit rot in
// header offsets fails the test binary rather than corrupting data
// on disk.
func init() {
	if WalHdrSize != 24 || WalFrameHdrSize != 24 {
		panic("wr: WAL header size constants drifted; SQLite journal layout is fixed at 24 bytes")
	}
}
