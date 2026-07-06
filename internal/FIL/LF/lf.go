package lf

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cyw0ng95/razordata/internal/LOG/EC"
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

// New creates a SegmentManager under root.
func New(root string, log ...lg.Logger) (*SegmentManager, error) {
	sm := &SegmentManager{root: root, log: lg.FirstLogger(log)}
	if err := os.MkdirAll(filepath.Join(root, "wal"), 0700); err != nil {
		if sm.log != nil {
			sm.log.Error("lf.new", "root", root, "err", err)
		}
		return nil, err
	}
	return sm, nil
}

func (sm *SegmentManager) segmentPath(n uint64) string {
	var b strings.Builder
	b.WriteString(sm.root)
	b.WriteString("/wal/wal.")
	s := strconv.FormatUint(n, 10)
	for i := len(s); i < 3; i++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
	return b.String()
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

// CreateSegmentDirect opens a WAL segment with O_DIRECT and pre-allocates
// disk space for the entire segment up to size. Returns EINVAL when O_DIRECT
// is not supported by the underlying filesystem.
func (sm *SegmentManager) CreateSegmentDirect(n uint64, size int64) (*FileHandle, error) {
	path := sm.segmentPath(n)

	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_DIRECT, 0600)
	if err != nil {
		if sm.log != nil {
			sm.log.Error("lf.create_segment_direct", "path", path, "err", err)
		}
		return nil, err
	}

	if err := unix.Fallocate(fd, 0, 0, size); err != nil {
		unix.Close(fd)
		return nil, err
	}

	h := &FileHandle{Path: path, FD: fd}
	h.Refs.Store(1)
	sm.pool.Store(n, h)
	return h, nil
}

func (sm *SegmentManager) Segment(n uint64) (*FileHandle, error) {
	if h, ok := sm.pool.Load(n); ok {
		fh := h.(*FileHandle)
		fh.mu.Lock()
		if fh.FD != -1 {
			fh.Refs.Add(1)
			fh.mu.Unlock()
			return fh, nil
		}
		// FD is -1 (closed). Reopen while holding the lock to
		// prevent TOCTOU race: another goroutine cannot close
		// the FD between our check and the open (REQ000598).
		fd, err := unix.Open(fh.Path, unix.O_RDWR, 0)
		if err != nil {
			fh.mu.Unlock()
			if sm.log != nil {
				sm.log.Error("lf.get_segment", "n", n, "path", fh.Path, "err", err)
			}
			return nil, err
		}
		fh.FD = fd
		fh.Refs.Add(1)
		fh.mu.Unlock()
		return fh, nil
	}

	path := sm.segmentPath(n)

	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, ErrSegmentNotFound
		}
		if errors.Is(err, unix.EISDIR) {
			return nil, ErrCorruptSegment
		}
		if sm.log != nil {
			sm.log.Error("lf.get_segment", "path", path, "err", err)
		}
		return nil, err
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		if sm.log != nil {
			sm.log.Error("lf.get_segment", "path", path, "err", err)
		}
		return nil, err
	}
	if stat.Mode&unix.S_IFDIR != 0 {
		unix.Close(fd)
		return nil, ErrCorruptSegment
	}

	h := &FileHandle{Path: path, FD: fd}
	h.Refs.Store(1)
	sm.pool.Store(n, h)
	return h, nil
}

func (sm *SegmentManager) Truncate(n uint64, newSize int64) error {
	h, err := sm.Segment(n)
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

// ListSegments enumerates WAL segment files in ascending order.
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

	slices.Sort(nums)
	return nums, nil
}

func (sm *SegmentManager) Close() error {
	var last error
	sm.pool.Range(func(key, value any) bool {
		h := value.(*FileHandle)
		h.mu.Lock()
		EC.WARN_ON(h.FD == -1, "lf.Close: double-close detected for segment %v", key)
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
