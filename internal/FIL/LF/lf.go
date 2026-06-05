package lf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"golang.org/x/sys/unix"
)

var (
	ErrSegmentNotFound = errors.New("WAL segment not found")
	ErrCorruptSegment  = errors.New("WAL segment corrupted")
)

type SegmentManager struct {
	root string
	pool sync.Map // map[uint64]*FileHandle keyed by segment number
	log  lg.Logger
}

// New creates a SegmentManager under root (the database directory).
func New(root string, log ...lg.Logger) (*SegmentManager, error) {
	sm := &SegmentManager{root: root, log: firstLogger(log)}
	if err := os.MkdirAll(filepath.Join(root, "wal"), 0700); err != nil {
		if sm.log != nil {
			sm.log.Error("lf.new", "root", root, "err", err)
		}
		return nil, err
	}
	return sm, nil
}

func firstLogger(logs []lg.Logger) lg.Logger {
	if len(logs) > 0 {
		return logs[0]
	}
	return nil
}

func (sm *SegmentManager) segmentPath(n uint64) string {
	return filepath.Join(sm.root, "wal", fmt.Sprintf("wal.%03d", n))
}

func (sm *SegmentManager) CreateSegment(n uint64) (*FileHandle, error) {
	path := sm.segmentPath(n)

	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT, 0600)
	if err != nil {
		if sm.log != nil {
			sm.log.Error("lf.create_segment", "path", path, "err", err)
		}
		return nil, err
	}

	h := &FileHandle{Path: path, FD: fd}
	h.Refs.Store(1)
	sm.pool.Store(n, h)
	return h, nil
}

func (sm *SegmentManager) GetSegment(n uint64) (*FileHandle, error) {
	if h, ok := sm.pool.Load(n); ok {
		fh := h.(*FileHandle)
		fh.mu.Lock()
		if fh.FD != -1 {
			fh.Refs.Add(1)
			fh.mu.Unlock()
			return fh, nil
		}
		fd, err := unix.Open(fh.Path, unix.O_RDWR, 0)
		fh.mu.Unlock()
		if err != nil {
			if sm.log != nil {
				sm.log.Error("lf.get_segment", "n", n, "path", fh.Path, "err", err)
			}
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
		if sm.log != nil {
			sm.log.Error("lf.get_segment", "path", path, "err", err)
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, ErrCorruptSegment
	}

	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		if sm.log != nil {
			sm.log.Error("lf.get_segment", "path", path, "err", err)
		}
		return nil, err
	}

	h := &FileHandle{Path: path, FD: fd}
	h.Refs.Store(1)
	sm.pool.Store(n, h)
	return h, nil
}

func (sm *SegmentManager) Truncate(n uint64, newSize int64) error {
	h, err := sm.GetSegment(n)
	if err != nil {
		return err
	}
	defer h.Close()

	if err := unix.Ftruncate(h.FD, newSize); err != nil {
		if sm.log != nil {
			sm.log.Error("lf.truncate", "n", n, "path", h.Path, "err", err)
		}
		return err
	}
	return nil
}

// ListSegments enumerates WAL segment files in the root/wal directory and
// returns their numeric suffixes in ascending order. Returns an empty slice
// (no error) if the wal directory does not exist. Non-segment files (anything
// not matching wal.<digits>) are ignored.
func (sm *SegmentManager) ListSegments() ([]uint64, error) {
	walDir := filepath.Join(sm.root, "wal")
	entries, err := os.ReadDir(walDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		if sm.log != nil {
			sm.log.Error("lf.list_segments", "dir", walDir, "err", err)
		}
		return nil, err
	}

	var nums []uint64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "wal.") {
			continue
		}
		suffix := strings.TrimPrefix(name, "wal.")
		if suffix == "" {
			continue
		}
		allDigits := true
		for _, c := range suffix {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if !allDigits {
			continue
		}
		n, perr := strconv.ParseUint(suffix, 10, 64)
		if perr != nil {
			continue
		}
		nums = append(nums, n)
	}

	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	return nums, nil
}

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
