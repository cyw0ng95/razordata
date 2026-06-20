package ls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var (
	ErrManifestNotFound = errors.New("ls: manifest not found")
	ErrInvalidManifest  = errors.New("ls: invalid manifest format")
)

// Version represents a point-in-time snapshot of the LSM manifest, holding
// the set of SST files organized by level.
type Version struct {
	num     int64
	levels  [][]SSTFileMeta
	created time.Time
}

// SSTFileMeta describes a single SST file: its level, key range, size, and
// bloom filter parameters.
type SSTFileMeta struct {
	FileID    uint64
	Level     int
	MinKey    []byte
	MaxKey    []byte
	Size      int64
	BloomBits int
}

type manifest struct {
	version atomic.Int64
	current atomic.Pointer[Version]
	dir     string
	changes chan Version
}

func newManifest(dir string) (*manifest, error) {
	m := &manifest{
		dir:     dir,
		changes: make(chan Version, 10),
	}
	m.version.Store(0)

	current := &Version{
		num:     0,
		levels:  make([][]SSTFileMeta, 0),
		created: time.Now(),
	}
	m.current.Store(current)

	manifestPath := filepath.Join(dir, "manifest")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}

	v, err := parseManifest(data)
	if err != nil {
		return nil, err
	}

	m.version.Store(v.num)
	m.current.Store(v)
	return m, nil
}

func (m *manifest) Current() *Version {
	return m.current.Load()
}

func (m *manifest) Apply(v Version) error {
	if v.num == 0 {
		v.num = m.version.Load() + 1
	}
	v.created = time.Now()

	data, err := encodeManifest(&v)
	if err != nil {
		return err
	}

	tmpPath := filepath.Join(m.dir, "manifest.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, filepath.Join(m.dir, "manifest")); err != nil {
		return err
	}

	m.version.Store(v.num)
	m.current.Store(&v)

	select {
	case m.changes <- v:
	default:
	}

	return nil
}

func (m *manifest) Checkpoint() (*ManifestCheckpoint, error) {
	v := m.Current()
	if v == nil {
		return nil, ErrInvalidManifest
	}

	return &ManifestCheckpoint{
		VersionNum: v.num,
		Files:      v.levels,
	}, nil
}

// ManifestCheckpoint is a serializable snapshot of the manifest version and
// its SST file list, used for WAL checkpointing.
type ManifestCheckpoint struct {
	VersionNum int64
	Files      [][]SSTFileMeta
}

func encodeManifest(v *Version) ([]byte, error) {
	var buf bytes.Buffer
	var b8 [8]byte

	binary.LittleEndian.PutUint64(b8[:], uint64(v.num))
	buf.Write(b8[:])
	binary.LittleEndian.PutUint64(b8[:], uint64(len(v.levels)))
	buf.Write(b8[:])

	for _, level := range v.levels {
		binary.LittleEndian.PutUint64(b8[:], uint64(len(level)))
		buf.Write(b8[:])
		for _, file := range level {
			buf.Write(encodeVarint(int64(len(file.MinKey))))
			buf.Write(file.MinKey)
			buf.Write(encodeVarint(int64(len(file.MaxKey))))
			buf.Write(file.MaxKey)
			binary.LittleEndian.PutUint64(b8[:], file.FileID)
			buf.Write(b8[:])
			binary.LittleEndian.PutUint64(b8[:], uint64(file.Size))
			buf.Write(b8[:])
			binary.LittleEndian.PutUint64(b8[:], uint64(file.Level))
			buf.Write(b8[:])
			binary.LittleEndian.PutUint64(b8[:], uint64(file.BloomBits))
			buf.Write(b8[:])
		}
	}

	checksum := crc32.Checksum(buf.Bytes(), crc32Koopman)
	var b4 [4]byte
	binary.LittleEndian.PutUint32(b4[:], checksum)
	buf.Write(b4[:])

	return buf.Bytes(), nil
}

func parseManifest(data []byte) (*Version, error) {
	if len(data) < 16 {
		return nil, ErrInvalidManifest
	}

	offset := 0

	v := &Version{}
	v.num = int64(binary.LittleEndian.Uint64(data[offset:]))
	offset += 8

	levelCount := int(binary.LittleEndian.Uint64(data[offset:]))
	offset += 8

	v.levels = make([][]SSTFileMeta, levelCount)

	for i := 0; i < levelCount; i++ {
		fileCount := int(binary.LittleEndian.Uint64(data[offset:]))
		offset += 8

		v.levels[i] = make([]SSTFileMeta, fileCount)
		for j := 0; j < fileCount; j++ {
			keyLen, n := decodeVarint(data[offset:])
			offset += n
			if offset+int(keyLen) > len(data) {
				return nil, ErrInvalidManifest
			}
			v.levels[i][j].MinKey = data[offset : offset+int(keyLen)]
			offset += int(keyLen)

			keyLen, n = decodeVarint(data[offset:])
			offset += n
			if offset+int(keyLen) > len(data) {
				return nil, ErrInvalidManifest
			}
			v.levels[i][j].MaxKey = data[offset : offset+int(keyLen)]
			offset += int(keyLen)

			if offset+8 > len(data) {
				return nil, ErrInvalidManifest
			}
			v.levels[i][j].FileID = binary.LittleEndian.Uint64(data[offset:])
			offset += 8

			if offset+8 > len(data) {
				return nil, ErrInvalidManifest
			}
			v.levels[i][j].Size = int64(binary.LittleEndian.Uint64(data[offset:]))
			offset += 8

			if offset+8 > len(data) {
				return nil, ErrInvalidManifest
			}
			v.levels[i][j].Level = int(int64(binary.LittleEndian.Uint64(data[offset:])))
			offset += 8

			if offset+8 > len(data) {
				return nil, ErrInvalidManifest
			}
			v.levels[i][j].BloomBits = int(int64(binary.LittleEndian.Uint64(data[offset:])))
			offset += 8
		}
	}

	storedChecksum := binary.LittleEndian.Uint32(data[len(data)-4:])
	computedChecksum := crc32.Checksum(data[:len(data)-4], crc32Koopman)
	if storedChecksum != computedChecksum {
		return nil, ErrInvalidManifest
	}

	return v, nil
}

func (m *manifest) Close() error {
	close(m.changes)
	return nil
}
