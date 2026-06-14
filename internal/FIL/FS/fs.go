package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"golang.org/x/sys/unix"
)

var (
	ErrPathTraversal = errors.New("path traversal escape attempt")
	ErrSymlink       = errors.New("symlink not allowed")
	ErrNotAbsolute   = errors.New("path must be absolute")
	ErrAlreadyExists = errors.New("file already exists")
	ErrDoesNotExist  = errors.New("file does not exist")
)

type pathValidator struct {
	root string
}

func newPathValidator(root string) (*pathValidator, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &pathValidator{root: abs}, nil
}

func (v *pathValidator) Validate(path string) error {
	if filepath.IsAbs(path) {
		return ErrNotAbsolute
	}
	if strings.Contains(path, "..") {
		return ErrPathTraversal
	}
	return nil
}

func (v *pathValidator) Resolve(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", ErrNotAbsolute
	}

	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		return "", ErrPathTraversal
	}

	abs := filepath.Join(v.root, clean)

	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", ErrSymlink
	}

	return abs, nil
}

// FileHandle is an open file with reference counting.
// mu guards FD against concurrent access and prevents double-close.
type FileHandle struct {
	Path string
	FD   int
	Refs int64
	mu   sync.Mutex
}

// Close closes the underlying FD. Safe to call multiple times.
func (h *FileHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.FD == -1 {
		return nil
	}

	if err := unix.Close(h.FD); err != nil {
		return err
	}
	h.FD = -1
	return nil
}

// FileManager manages files in the database directory.
type FileManager struct {
	root     string
	handles  sync.Map // map[string]*FileHandle, keyed by absolute path
	dirFDs   sync.Map // map[string]int, cached directory FDs for SyncDir
	validate *pathValidator
	log      lg.Logger
	locking  bool
}

// Option configures a FileManager.
type Option func(*FileManager)

// WithLocking enables advisory flock(LOCK_EX) on Open/Create to
// prevent concurrent multi-process access.
func WithLocking(v bool) Option {
	return func(fm *FileManager) { fm.locking = v }
}

// New creates a new FileManager rooted at root.
// Returns ErrDoesNotExist if root is not an existing directory.
func New(root string, log ...lg.Logger) (*FileManager, error) {
	return NewOptions(root, nil, log...)
}

// NewOptions creates a FileManager with additional configuration options.
func NewOptions(root string, opts []Option, log ...lg.Logger) (*FileManager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrDoesNotExist
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "open", Path: root, Err: errors.New("not a directory")}
	}
	pv, err := newPathValidator(abs)
	if err != nil {
		return nil, err
	}
	fm := &FileManager{root: abs, validate: pv, log: lg.FirstLogger(log)}
	for _, opt := range opts {
		opt(fm)
	}
	return fm, nil
}

// NewOrCreate creates a new FileManager, creating root and any parents if needed.
func NewOrCreate(root string, log ...lg.Logger) (*FileManager, error) {
	return NewOptionsOrCreate(root, nil, log...)
}

// NewOptionsOrCreate creates a FileManager with options, creating directory if needed.
func NewOptionsOrCreate(root string, opts []Option, log ...lg.Logger) (*FileManager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	pv, err := newPathValidator(abs)
	if err != nil {
		return nil, err
	}
	fm := &FileManager{root: abs, validate: pv, log: lg.FirstLogger(log)}
	for _, opt := range opts {
		opt(fm)
	}
	return fm, nil
}

// Open opens an existing file. Returns ErrDoesNotExist if absent.
// When locking is enabled, acquires an advisory flock(LOCK_EX).
func (fm *FileManager) Open(name string) (*FileHandle, error) {
	abs, err := fm.validate.Resolve(name)
	if err != nil {
		return nil, err
	}

	if h, ok := fm.handles.Load(abs); ok {
		fh := h.(*FileHandle)
		fh.mu.Lock()
		if fh.FD != -1 {
			if err := unix.Close(fh.FD); err != nil && fm.log != nil {
				fm.log.Warn("fs.open.close", "path", abs, "err", err)
			}
		}
		fd, err := unix.Open(abs, unix.O_RDWR, 0)
		fh.mu.Unlock()
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				return nil, ErrDoesNotExist
			}
			if fm.log != nil {
				fm.log.Error("fs.open", "path", abs, "err", err)
			}
			return nil, err
		}
		fh.mu.Lock()
		fh.FD = fd
		if fm.locking {
			if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
				fh.mu.Unlock()
				unix.Close(fd)
				return nil, err
			}
		}
		fh.mu.Unlock()
		return fh, nil
	}

	fd, err := unix.Open(abs, unix.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, ErrDoesNotExist
		}
		if fm.log != nil {
			fm.log.Error("fs.open", "path", abs, "err", err)
		}
		return nil, err
	}
	if fm.locking {
		if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
			unix.Close(fd)
			return nil, err
		}
	}

	h := &FileHandle{Path: abs, FD: fd}
	fm.handles.Store(abs, h)
	return h, nil
}

// Create creates a new file exclusively (O_CREAT|O_EXCL).
// Returns ErrAlreadyExists if the file already exists.
// When locking is enabled, acquires an advisory flock(LOCK_EX).
func (fm *FileManager) Create(name string) (*FileHandle, error) {
	abs, err := fm.validate.Resolve(name)
	if err != nil {
		return nil, err
	}

	fd, err := unix.Open(abs, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, ErrAlreadyExists
		}
		if fm.log != nil {
			fm.log.Error("fs.create", "path", abs, "err", err)
		}
		return nil, err
	}
	if fm.locking {
		if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
			unix.Close(fd)
			return nil, err
		}
	}

	h := &FileHandle{Path: abs, FD: fd}
	fm.handles.Store(abs, h)
	return h, nil
}

// Remove removes a file. Cached handle is evicted from the map.
func (fm *FileManager) Remove(name string) error {
	abs, err := fm.validate.Resolve(name)
	if err != nil {
		return err
	}

	if err := unix.Unlink(abs); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return ErrDoesNotExist
		}
		if fm.log != nil {
			fm.log.Error("fs.remove", "path", abs, "err", err)
		}
		return err
	}

	fm.handles.Delete(abs)
	return nil
}

// List returns file names matching the glob pattern relative to the database root.
func (fm *FileManager) List(pattern string) ([]string, error) {
	abs, err := fm.validate.Resolve(pattern)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(abs)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var result []string
	pat := filepath.Base(abs)
	for _, entry := range entries {
		matched, err := filepath.Match(pat, entry.Name())
		if err != nil {
			return nil, err
		}
		if matched {
			result = append(result, entry.Name())
		}
	}
	return result, nil
}

// MkdirAll creates directories with permission 0700.
func (fm *FileManager) MkdirAll(name string) error {
	abs, err := fm.validate.Resolve(name)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0700)
}

// SyncDir syncs the directory containing name to durable storage.
func (fm *FileManager) SyncDir(name string) error {
	abs, err := fm.validate.Resolve(name)
	if err != nil {
		return err
	}

	if dfd, ok := fm.dirFDs.Load(abs); ok {
		if err := unix.Fsync(dfd.(int)); err != nil {
			if fm.log != nil {
				fm.log.Error("fs.syncdir", "path", abs, "err", err)
			}
			return err
		}
		return nil
	}

	dirfd, err := unix.Open(abs, unix.O_RDONLY, 0)
	if err != nil {
		if fm.log != nil {
			fm.log.Error("fs.syncdir", "path", abs, "err", err)
		}
		return err
	}
	fm.dirFDs.Store(abs, dirfd)
	if err := unix.Fsync(dirfd); err != nil {
		if fm.log != nil {
			fm.log.Error("fs.syncdir", "path", abs, "err", err)
		}
		return err
	}
	return nil
}

// Close closes all cached file handles and directory FDs.
func (fm *FileManager) Close() error {
	var last error

	fm.dirFDs.Range(func(key, value any) bool {
		if err := unix.Close(value.(int)); err != nil && last == nil {
			last = err
		}
		return true
	})
	fm.dirFDs = sync.Map{}

	fm.handles.Range(func(key, value any) bool {
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
	fm.handles = sync.Map{}

	return last
}
