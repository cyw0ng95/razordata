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
)

const maxSupportedVersion = WALVersionV1

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
