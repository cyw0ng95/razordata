// Package rp — SQLite-style WAL frame verification (REQ001297,
// REQ001298).
//
// This file is the replayer-side entry point for verifying frame
// blos produced by writers in package wr. It delegates to wr's
// exported DecodeFrameHdr / VerifyFrame / VerifyCommitMarker and
// adds:
//   - a parsed-frame value type with PageNo / DbSizeAfterCommit
//     accessors,
//   - a VerifyFrameBytes helper that takes the raw on-disk bytes of
//     a single frame (header + page body = WalFrameHdrSize + PageSize)
//     and reports per-tampering failure modes,
//   - a VerifyCommitMarkerBytes helper for the trailing 16-byte
//     marker that follows every commit.
//
// Concurrency and -shm protocol land in a future iteration. The
// helpers here are intentionally synchronous so the replayer can
// invoke them from a sequential crash-recovery scan.
package rp

import (
	"errors"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/WAL/WR"
)

// ErrFrameShortBuffer: the frame bytes are shorter than the canonical
// 24-byte header. REQ001297.
var ErrFrameShortBuffer = errors.New("rp: WAL frame bytes shorter than 24-byte header")

// ErrPageBodyWrongSize: the page body does not match the expected
// PageSize declared in the WAL header. REQ001297.
var ErrPageBodyWrongSize = errors.New("rp: WAL page body size mismatch")

// ParsedFrame pairs a parsed frame header with the page body that
// follows it on disk. REQ001297.
type ParsedFrame struct {
	Hdr   wr.WalFrameHdr
	Page  []byte // caller-owned slice; not retained by InspectFrame
}

// InspectFrame parses the 24-byte frame header and copies the page
// body out of the input bytes. The returned ParsedFrame.Page is a
// fresh slice (so callers may freely reuse the input buffer).
// REQ001297.
func InspectFrame(frame []byte) (ParsedFrame, error) {
	if len(frame) < wr.WalFrameHdrSize {
		return ParsedFrame{}, fmt.Errorf("%w: got %d", ErrFrameShortBuffer, len(frame))
	}
	hdr, err := wr.DecodeFrameHdr(frame[:wr.WalFrameHdrSize])
	if err != nil {
		return ParsedFrame{}, err
	}
	body := make([]byte, len(frame)-wr.WalFrameHdrSize)
	copy(body, frame[wr.WalFrameHdrSize:])
	return ParsedFrame{Hdr: hdr, Page: body}, nil
}

// VerifyFrameBytes performs integrity verification on the supplied
// on-disk frame blob: header parsing, salt-pair equality with the
// supplied `walSalt1`/`walSalt2`, checksum recomputation, and
// commit-marker storage layout. REQ001297, REQ001298.
//
// walSalt1 / walSalt2 must match the values carried in the WAL
// header for the commit this frame belongs to. A mismatch is
// reported as ErrFrameBadSalt (re-exported from wr).
func VerifyFrameBytes(frame []byte, walSalt1, walSalt2 uint32) error {
	if len(frame) < wr.WalFrameHdrSize {
		return fmt.Errorf("%w: got %d", ErrFrameShortBuffer, len(frame))
	}
	hdr, err := wr.DecodeFrameHdr(frame[:wr.WalFrameHdrSize])
	if err != nil {
		return err
	}
	if hdr.Salt1 != walSalt1 || hdr.Salt2 != walSalt2 {
		return fmt.Errorf("%w: have=(%08x,%08x) want=(%08x,%08x)",
			wr.ErrFrameBadSalt, hdr.Salt1, hdr.Salt2, walSalt1, walSalt2)
	}
	page := frame[wr.WalFrameHdrSize:]
	if len(page) != wr.WalPageSize {
		// Page bodies in razordata are exactly PageSize bytes. A
		// truncated frame body fails loud here so the replayer can
		// skip-or-fail per the existing torn-write policy.
		return fmt.Errorf("%w: got %d want %d", ErrPageBodyWrongSize, len(page), wr.WalPageSize)
	}
	return wr.VerifyFrame(hdr, page)
}

// VerifyCommitMarkerBytes wraps wr.VerifyCommitMarker so the
// replayer does not need to import encoding/binary just for length
// checks. REQ001298.
func VerifyCommitMarkerBytes(marker []byte) error {
	return wr.VerifyCommitMarker(marker)
}
