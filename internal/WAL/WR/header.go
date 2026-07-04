package wr

import (
	"fmt"

	"golang.org/x/sys/unix"
)

var pwriteHeader = func(fd int, buf []byte) (int, error) {
	return pwriteAt(fd, buf, 0)
}

func pwriteAt(fd int, buf []byte, off int64) (int, error) {
	return unix.Pwrite(fd, buf, off)
}

const (
	WALMagic            = "WLOG"
	WALVersionV1  uint8 = 0x01
	WALHeaderSize       = 12
)

const (
	FlagCompressionLZ4 uint8 = 0x01
	FlagDirectIO       uint8 = 0x02
)

const directBlockSize = 512

const maxSupportedVersion = WALVersionV1

// DirectDataOffset returns the file offset where WAL record data begins
// in O_DIRECT segments. The 12-byte header is padded to directBlockSize,
// so record data starts at directBlockSize rather than WALHeaderSize.
func DirectDataOffset() int64 {
	return directBlockSize
}

var headerEncode = func() []byte {
	b := make([]byte, WALHeaderSize)
	copy(b[0:4], WALMagic)
	b[4] = WALVersionV1
	return b
}()

var headerEncodeCompressed = func() []byte {
	b := make([]byte, WALHeaderSize)
	copy(b[0:4], WALMagic)
	b[4] = WALVersionV1
	b[5] = FlagCompressionLZ4
	return b
}()

func writeSegmentHeader(fd int) error {
	return writeSegmentHeaderWithFlags(fd, 0)
}

// writeSegmentHeaderWithFlags writes the 12-byte header with flags.
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

// writePaddedHeader writes the 12-byte WAL header padded to directBlockSize
// for O_DIRECT segments. Uses mmap to obtain a page-aligned buffer satisfying
// O_DIRECT alignment requirements.
func writePaddedHeader(fd int, flags uint8) error {
	buf, err := unix.Mmap(-1, 0, directBlockSize,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	if err != nil {
		return fmt.Errorf("wr: allocate aligned header buf: %w", err)
	}
	defer unix.Munmap(buf)
	if flags&FlagCompressionLZ4 != 0 {
		copy(buf, headerEncodeCompressed)
	} else {
		copy(buf, headerEncode)
	}
	buf[5] = flags
	n, err := unix.Pwrite(fd, buf, 0)
	if err != nil {
		return fmt.Errorf("wr: write padded header: %w", err)
	}
	if n != directBlockSize {
		return fmt.Errorf("wr: short padded header write: %d/%d", n, directBlockSize)
	}
	return nil
}

func readSegmentHeader(r interface {
	Read(p []byte) (int, error)
}) error {
	var buf [WALHeaderSize]byte
	if _, err := r.Read(buf[:]); err != nil {
		return fmt.Errorf("wr: read header: %w", err)
	}
	return ValidateSegmentHeader(buf[:])
}

// ValidateSegmentHeader parses a header buffer.
func ValidateSegmentHeader(buf []byte) error {
	if len(buf) < WALHeaderSize {
		return fmt.Errorf("%w: header shorter than %d bytes", ErrCorrupt, WALHeaderSize)
	}
	if string(buf[0:4]) != WALMagic {
		return fmt.Errorf("%w: bad segment magic", ErrCorrupt)
	}
	version := buf[4]
	if version == 0 {
		return fmt.Errorf("%w: legacy segment without header (pre-v0.10.0)", ErrCorrupt)
	}
	if version > maxSupportedVersion {
		return fmt.Errorf("%w: version %d > max supported %d",
			ErrCorrupt, version, maxSupportedVersion)
	}
	return nil
}
