package lf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// WAL segment files live under the "wal/" subdirectory.
// Naming: wal.000, wal.001, ... (3-digit zero-padded numeric suffix).
// WAL segments always use buffered I/O (no O_DIRECT).

var (
	ErrSegmentNotFound = errors.New("WAL segment not found")
	ErrCorruptSegment  = errors.New("WAL segment corrupted")
)

// SegmentManager manages WAL segment files and their handle pool.
type SegmentManager struct {
	root string
	pool sync.Map // map[uint64]*FileHandle keyed by segment number
	mu   sync.Mutex
}

// New creates a SegmentManager under root (the database directory).
func New(root string) (*SegmentManager, error) {
	sm := &SegmentManager{root: root}
	if err := os.MkdirAll(filepath.Join(root, "wal"), 0700); err != nil {
		return nil, err
	}
	return sm, nil
}

// segmentPath returns the path for WAL segment n under the wal/ subdirectory.
func (sm *SegmentManager) segmentPath(n uint64) string {
	return filepath.Join(sm.root, "wal", fmt.Sprintf("wal.%03d", n))
}

// CreateSegment opens or creates WAL segment n for append-only access.
// Returns a FileHandle with refcount = 1.
func (sm *SegmentManager) CreateSegment(n uint64) (*FileHandle, error) {
	path := sm.segmentPath(n)

	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT, 0600)
	if err != nil {
		return nil, err
	}

	h := &FileHandle{
		Path: path,
		FD:   fd,
	}
	h.Refs.Store(1)
	sm.pool.Store(n, h)
	return h, nil
}

// GetSegment returns an existing segment handle, or ErrSegmentNotFound.
// If the cached handle's FD is already closed (e.g. by a prior FileManager.Close()),
// it reopens the file and updates the handle.
func (sm *SegmentManager) GetSegment(n uint64) (*FileHandle, error) {
	if h, ok := sm.pool.Load(n); ok {
		fh := h.(*FileHandle)
		fh.mu.Lock()
		if fh.FD != -1 {
			fh.Refs.Add(1)
			fh.mu.Unlock()
			return fh, nil
		}
		// FD was already closed; reopen it.
		fd, err := unix.Open(fh.Path, unix.O_RDWR, 0)
		fh.mu.Unlock()
		if err != nil {
			return nil, err
		}
		fh.mu.Lock()
		fh.FD = fd
		fh.Refs.Add(1)
		fh.mu.Unlock()
		return fh, nil
	}

	path := sm.segmentPath(n)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSegmentNotFound
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, ErrCorruptSegment
	}

	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	h := &FileHandle{Path: path, FD: fd}
	h.Refs.Store(1)
	sm.pool.Store(n, h)
	return h, nil
}

// Truncate shrinks segment n to size newSize via ftruncate.
// Used after WAL replay to discard fully-replayed segments.
func (sm *SegmentManager) Truncate(n uint64, newSize int64) error {
	h, err := sm.GetSegment(n)
	if err != nil {
		return err
	}
	defer h.Close()

	if err := unix.Ftruncate(h.FD, newSize); err != nil {
		return err
	}
	return nil
}

// Close closes all active segment handles.
func (sm *SegmentManager) Close() error {
	var last error
	sm.pool.Range(func(key, value any) bool {
		h := value.(*FileHandle)
		h.mu.Lock()
		if h.FD != -1 {
			if err := unix.Close(h.FD); err != nil {
				last = err
			}
			h.FD = -1
		}
		h.mu.Unlock()
		return true
	})
	sm.pool = sync.Map{}
	return last
}

// FileHandle mirrors fs.FileHandle for WAL segment handles.
// It is a copy to avoid import cycles.
type FileHandle struct {
	Path string
	FD   int
	Refs atomic.Int64
	mu   sync.Mutex
}

func (h *FileHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.FD == -1 {
		return errors.New("file already closed")
	}
	if h.Refs.Add(-1) > 0 {
		return nil
	}
	if err := unix.Close(h.FD); err != nil {
		return err
	}
	h.FD = -1
	return nil
}

func (h *FileHandle) Incref() { h.Refs.Add(1) }
