package mf

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"golang.org/x/sys/unix"
)

const (
	MagicValue     = uint32(0x5241524F) // "RAZO" in ASCII
	CurrentVersion = uint32(1)
)

var (
	ErrBadMagic        = errors.New("meta page has invalid magic bytes")
	ErrUpgradeRequired = errors.New("database version is newer than this software")
	ErrBadVersion      = errors.New("meta page version mismatch")
	ErrCorrupt         = errors.New("meta page checksum mismatch")
)

// MetaPage occupies block 0 of meta.razor.
// It holds the database format version, block size, catalog root pointer,
// and manifest checksum.
type MetaPage struct {
	Magic            uint32
	Version          uint32
	BlockSize        uint32
	CatalogRootPtr   uint64
	ManifestChecksum uint32
}

const metaPageSize = 4 + 4 + 4 + 8 + 4 // Magic + Version + BlockSize + CatalogRootPtr + ManifestChecksum = 24 bytes

// metaPageRaw is the on-disk layout of MetaPage (little-endian, no padding).
type metaPageRaw [metaPageSize]byte

func (p *MetaPage) MarshalBinary() ([]byte, error) {
	var raw metaPageRaw
	binary.LittleEndian.PutUint32(raw[0:4], p.Magic)
	binary.LittleEndian.PutUint32(raw[4:8], p.Version)
	binary.LittleEndian.PutUint32(raw[8:12], p.BlockSize)
	binary.LittleEndian.PutUint64(raw[12:20], p.CatalogRootPtr)
	binary.LittleEndian.PutUint32(raw[20:24], p.ManifestChecksum)
	return raw[:], nil
}

func (p *MetaPage) UnmarshalBinary(data []byte) error {
	if len(data) < metaPageSize {
		return errors.New("meta page data too short")
	}
	p.Magic = binary.LittleEndian.Uint32(data[0:4])
	p.Version = binary.LittleEndian.Uint32(data[4:8])
	p.BlockSize = binary.LittleEndian.Uint32(data[8:12])
	p.CatalogRootPtr = binary.LittleEndian.Uint64(data[12:20])
	p.ManifestChecksum = binary.LittleEndian.Uint32(data[20:24])
	return nil
}

// DefaultMetaPage returns a MetaPage with default values.
func DefaultMetaPage() *MetaPage {
	return &MetaPage{
		Magic:     MagicValue,
		Version:   CurrentVersion,
		BlockSize: 4096,
	}
}

// MetaReader reads and validates the meta page.
type MetaReader struct {
	path string
}

// NewMetaReader returns a MetaReader for the given meta.razor path.
func NewMetaReader(path string) *MetaReader {
	return &MetaReader{path: path}
}

// Read opens meta.razor, reads block 0, validates magic and version.
// Creates meta.razor with default values if it does not exist.
// Returns ErrBadMagic if magic bytes are invalid.
// Returns ErrUpgradeRequired if version is newer than CurrentVersion.
func (r *MetaReader) Read() (*MetaPage, error) {
	fd, err := unix.Open(r.path, unix.O_RDWR|unix.O_CREAT, 0600)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)

	var raw metaPageRaw
	n, err := unix.Pread(fd, raw[:], 0)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return nil, err
	}

	// Empty or short file: initialize with defaults.
	if n == 0 || len(raw[:n]) < metaPageSize {
		mp := DefaultMetaPage()
		if err := writeMeta(fd, mp); err != nil {
			return nil, err
		}
		return mp, nil
	}

	mp := &MetaPage{}
	if err := mp.UnmarshalBinary(raw[:n]); err != nil {
		return nil, err
	}

	if mp.Magic != MagicValue {
		return nil, ErrBadMagic
	}
	if mp.Version > CurrentVersion {
		return nil, ErrUpgradeRequired
	}

	return mp, nil
}

// MetaWriter writes the meta page to block 0 of meta.razor.
// Should only be called during CREATE DATABASE or CHECKPOINT.
type MetaWriter struct {
	path string
}

// NewMetaWriter returns a MetaWriter for the given meta.razor path.
func NewMetaWriter(path string) *MetaWriter {
	return &MetaWriter{path: path}
}

// Write writes the MetaPage to block 0 of meta.razor.
func (w *MetaWriter) Write(p *MetaPage) error {
	fd, err := unix.Open(w.path, unix.O_RDWR|unix.O_CREAT, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	return writeMeta(fd, p)
}

func writeMeta(fd int, p *MetaPage) error {
	// Compute checksum over everything except the checksum field itself (last 4 bytes).
	data, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	sum := crc32.ChecksumIEEE(data[:len(data)-4])
	var raw metaPageRaw
	binary.LittleEndian.PutUint32(raw[0:4], p.Magic)
	binary.LittleEndian.PutUint32(raw[4:8], p.Version)
	binary.LittleEndian.PutUint32(raw[8:12], p.BlockSize)
	binary.LittleEndian.PutUint64(raw[12:20], p.CatalogRootPtr)
	binary.LittleEndian.PutUint32(raw[20:24], sum)

	_, err = unix.Pwrite(fd, raw[:], 0)
	return err
}
