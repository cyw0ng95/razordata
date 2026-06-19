package wr

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// pwriteHeader is a package-level indirection so tests can
// substitute a no-op. Production uses unix.Pwrite via the
// pwriteAt helper below; tests override this var to return
// success without touching the file descriptor.
var pwriteHeader = func(fd int, buf []byte) (int, error) {
	return pwriteAt(fd, buf, 0)
}

// pwriteAt does a positional write to fd. Wraps unix.Pwrite so
// the rest of the package does not import golang.org/x/sys/unix
// directly (kept here as a single import for clarity).
func pwriteAt(fd int, buf []byte, off int64) (int, error) {
	return unix.Pwrite(fd, buf, off)
}

// Segment file header (R13-1). Every WAL segment file starts
// with this 12-byte header. The header carries the magic and
// format version so the replayer can detect a v0.9.x segment
// (no header) and reject it, and so a future v0.11.x binary's
// segment can be rejected by an older replayer.
// Wire layout:
//
//	┌──────────────────────────────────────────────┐
//	│ magic     :4   = "WLOG" (0x57 0x4C 0x4F 0x47) │
//	│ version   :1   = 0x01                          │
//	│ flags     :1   = compression bit (REQ000034)   │
//	│ reserved  :6   = 0x00 (forward compat)         │
//	└──────────────────────────────────────────────┘
//
// The flags byte (offset 5) is a bitfield. Bit 0 (0x01) indicates
// that record bodies in this segment are lz4-compressed. A
// segment with the flag clear contains uncompressed records
// (the default for backward compatibility).
const (
	// WALMagic is the four-byte segment-file magic. v0.10.0+.
	WALMagic = "WLOG"
	// WALVersionV1 is the only version this binary writes.
	WALVersionV1 uint8 = 0x01
	// WALHeaderSize is the fixed header length. Writers must
	// advance writeOff by this amount after CreateSegment and
	// before the first record; readers must skip this many bytes
	// before decoding the record stream.
	WALHeaderSize = 12
)

// Segment header flags.
const (
	// FlagCompressionLZ4 indicates that record bodies in this
	// segment are lz4-compressed. The envelope format
	// (length:varint + body + crc32:4) is unchanged; only the
	// body bytes are lz4-compressed before the CRC is computed.
	// REQ000034.
	FlagCompressionLZ4 uint8 = 0x01
)

// maxSupportedVersion is the highest version this binary will
// accept on read. Bump this in lockstep with the version byte
// the writer emits.
const maxSupportedVersion = WALVersionV1

// headerEncode is the pre-built 12-byte header for an
// uncompressed segment. Computed once at package init so the
// writer does not re-serialize the constant bytes on every
// segment creation.
var headerEncode = func() []byte {
	b := make([]byte, WALHeaderSize)
	copy(b[0:4], WALMagic)
	b[4] = WALVersionV1
	// bytes 5..12 are zero (flags + reserved)
	return b
}()

// headerEncodeCompressed is the pre-built 12-byte header for a
// lz4-compressed segment. REQ000034.
var headerEncodeCompressed = func() []byte {
	b := make([]byte, WALHeaderSize)
	copy(b[0:4], WALMagic)
	b[4] = WALVersionV1
	b[5] = FlagCompressionLZ4
	// bytes 6..12 are zero (reserved)
	return b
}()

// writeSegmentHeader writes the 12-byte header to fd. Called by
// openSegmentLocked once per segment lifetime. Uses pwrite at
// offset 0 to avoid clobbering the buffered path. fd must be a
// fresh, empty file.
func writeSegmentHeader(fd int) error {
	return writeSegmentHeaderWithFlags(fd, 0)
}

// writeSegmentHeaderWithFlags writes the 12-byte header to fd
// with the specified flags byte. REQ000034: flags byte carries
// the compression bit.
func writeSegmentHeaderWithFlags(fd int, flags uint8) error {
	var buf []byte
	if flags&FlagCompressionLZ4 != 0 {
		buf = headerEncodeCompressed
	} else {
		buf = headerEncode
	}
	n, err := pwriteHeader(fd, buf)
	if err != nil {
		return fmt.Errorf("wr: write header: %w", err)
	}
	if n != WALHeaderSize {
		return fmt.Errorf("wr: short header write: %d/%d", n, WALHeaderSize)
	}
	return nil
}

// readSegmentHeader reads and validates the 12-byte header from
// r. Returns a typed error if the header is missing, the magic
// is wrong, or the version is newer than this binary supports.
// The replayer calls this exactly once per segment. The returned
// error is wrapped at the call site with segment context (which
// segment file, which byte offset).
func readSegmentHeader(r interface {
	Read(p []byte) (int, error)
}) error {
	var buf [WALHeaderSize]byte
	if _, err := r.Read(buf[:]); err != nil {
		return fmt.Errorf("wr: read header: %w", err)
	}
	return ValidateSegmentHeader(buf[:])
}

// ValidateSegmentHeader parses a header buffer. Exposed for
// tests that build segment fixtures in memory and for the
// replayer to validate a header in place. All failure paths
// return ErrCorrupt (or a chain that wraps it) so the replayer
// can surface corruption with a single errors.Is check.
func ValidateSegmentHeader(buf []byte) error {
	if len(buf) < WALHeaderSize {
		return fmt.Errorf("%w: header shorter than %d bytes", ErrCorrupt, WALHeaderSize)
	}
	if string(buf[0:4]) != WALMagic {
		return fmt.Errorf("%w: bad segment magic", ErrCorrupt)
	}
	version := buf[4]
	if version == 0 {
		// No header at all — the bytes were a record's length
		// varint that happened to land in the magic's range.
		// This is the v0.9.x segment signature. Treat as
		// corruption (R13-11: no silent compat).
		return fmt.Errorf("%w: legacy segment without header (pre-v0.10.0)", ErrCorrupt)
	}
	if version > maxSupportedVersion {
		return fmt.Errorf("%w: version %d > max supported %d",
			ErrCorrupt, version, maxSupportedVersion)
	}
	return nil
}
