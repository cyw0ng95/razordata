package ls

import (
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// FS abstracts filesystem operations for testability and alternate backends.
// Pebble's vfs.VFS pattern. REQ001172.
type FS interface {
	Open(name string) (File, error)
	Create(name string) (File, error)
	MkdirAll(path string, perm os.FileMode) error
	Remove(name string) error
	Rename(oldPath, newPath string) error
	Symlink(target, link string) error
	Stat(name string) (FileInfo, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	CreateTemp(dir, pattern string) (File, string, error)
}

// File abstracts os.File operations.
type File interface {
	io.ReaderAt
	io.WriterAt
	io.Reader
	io.Closer
	io.Writer // convenience: sequential write
	Sync() error
	Stat() (FileInfo, error)
	ReadAll() ([ ]byte, error)
}

// FileInfo abstracts os.FileInfo.
type FileInfo interface {
	Name() string
	Size() int64
	Mode() os.FileMode
	IsDir() bool
}

// osFS wraps standard library os calls.
type osFS struct{}

func (osFS) Open(name string) (File, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	return &osFile{File: f}, nil
}

func (osFS) Create(name string) (File, error) {
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	return &osFile{File: f}, nil
}

func (osFS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (osFS) Remove(name string) error {
	return os.Remove(name)
}

func (osFS) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (osFS) Symlink(target, link string) error {
	return os.Symlink(target, link)
}

func (osFS) Stat(name string) (FileInfo, error) {
	fi, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	return &osFileInfo{FileInfo: fi}, nil
}

func (osFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (osFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (osFS) CreateTemp(dir, pattern string) (File, string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, "", err
	}
	return &osFile{File: f}, f.Name(), nil
}

// osFile wraps *os.File.
type osFile struct{ *os.File }

func (f *osFile) ReadAll() ([]byte, error) {
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	data := make([]byte, stat.Size())
	_, err = f.ReadAt(data, 0)
	return data, err
}

func (f *osFile) Stat() (FileInfo, error) {
	fi, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return &osFileInfo{FileInfo: fi}, nil
}

type osFileInfo struct{ os.FileInfo }

func DefaultFS() FS { return osFS{} }

// inMemFS is an in-memory FS for testing.
type inMemFS struct {
	mu        sync.RWMutex
	files     map[string]*memFile
	injectErr atomic.Value // struct{ err error; rate float64 }
}

type memFile struct {
	data   []byte
	closed bool
	dir    bool
}

type memFileInfo struct {
	name string
	size int64
	mode os.FileMode
	dir  bool
}

func (fi *memFileInfo) Name() string      { return fi.name }
func (fi *memFileInfo) Size() int64       { return fi.size }
func (fi *memFileInfo) Mode() os.FileMode { return fi.mode }
func (fi *memFileInfo) IsDir() bool       { return fi.dir }

func newInMemFS() *inMemFS {
	return &inMemFS{files: make(map[string]*memFile)}
}

func (f *inMemFS) Open(name string) (File, error) {
	if err := f.checkInject(); err != nil {
		return nil, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	name = filepath.Clean(name)
	if name == "." {
		return nil, os.ErrNotExist
	}
	mf, ok := f.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &memFileReader{mf: mf}, nil
}

func (f *inMemFS) Create(name string) (File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name = filepath.Clean(name)
	f.files[name] = &memFile{}
	return &memFileWriter{fs: f, name: name}, nil
}

func (f *inMemFS) MkdirAll(path string, _ os.FileMode) error {
	path = filepath.Clean(path)
	if path == "." {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = &memFile{dir: true}
	return nil
}


func (f *inMemFS) Remove(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	name = filepath.Clean(name)
	if _, ok := f.files[name]; !ok {
		return os.ErrNotExist
	}
	delete(f.files, name)
	return nil
}

func (f *inMemFS) Rename(oldPath, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	oldPath = filepath.Clean(oldPath)
	newPath = filepath.Clean(newPath)
	mf, ok := f.files[oldPath]
	if !ok {
		return os.ErrNotExist
	}
	f.files[newPath] = mf
	delete(f.files, oldPath)
	return nil
}

func (f *inMemFS) Symlink(target, link string) error {
	return os.ErrInvalid // inMemFS doesn't support symlinks
}

func (f *inMemFS) Stat(name string) (FileInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	name = filepath.Clean(name)
	if name == "." {
		return nil, os.ErrNotExist
	}
	mf, ok := f.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	if mf.dir {
		return &memFileInfo{name: filepath.Base(name), mode: 0755, dir: true}, nil
	}
	return &memFileInfo{name: filepath.Base(name), size: int64(len(mf.data)), mode: 0644}, nil
}

func (f *inMemFS) ReadFile(name string) ([]byte, error) {
	ff, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer ff.Close()
	return ff.ReadAll()
}

func (f *inMemFS) WriteFile(name string, data []byte, _ os.FileMode) error {
	ff, err := f.Create(name)
	if err != nil {
		return err
	}
	_, err = ff.WriteAt(data, 0)
	return ff.Close()
}

func (f *inMemFS) CreateTemp(dir, pattern string) (File, string, error) {
	name := filepath.Join(dir, pattern)
	file, err := f.Create(name)
	if err != nil {
		return nil, "", err
	}
	return file, name, nil
}

// InjectFailure sets an error to return for a random fraction of operations.
func (f *inMemFS) InjectFailure(err error, rate float64) {
	f.injectErr.Store(struct {
		err  error
		rate float64
	}{err: err, rate: rate})
}

func (f *inMemFS) checkInject() error {
	v := f.injectErr.Load()
	if v == nil {
		return nil
	}
	cfg := v.(struct {
		err  error
		rate float64
	})
	if cfg.err == nil {
		return nil
	}
	// Deterministic for testing: if rate == 0, never fail; if rate >= 1, always fail
	if cfg.rate == 0 {
		return nil
	}
	if cfg.rate >= 1 {
		return cfg.err
	}
	if rand.Float64() < cfg.rate {
		return cfg.err
	}
	return nil
}

type memFileReader struct {
	mf     *memFile
	closed bool
}

func (r *memFileReader) ReadAt(p []byte, off int64) (int, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	if off >= int64(len(r.mf.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.mf.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *memFileReader) Read(p []byte) (int, error) {
	return r.ReadAt(p, 0)
}

func (r *memFileReader) Write(p []byte) (int, error) {
	return 0, os.ErrClosed
}

func (r *memFileReader) WriteAt(p []byte, off int64) (int, error) {
	return 0, os.ErrClosed
}

func (r *memFileReader) Sync() error             { return nil }
func (r *memFileReader) Stat() (FileInfo, error) { return nil, os.ErrClosed }
func (r *memFileReader) ReadAll() ([]byte, error) {
	if r.closed {
		return nil, os.ErrClosed
	}
	return append([]byte(nil), r.mf.data...), nil
}
func (r *memFileReader) Close() error {
	r.closed = true
	return nil
}

type memFileWriter struct {
	fs   *inMemFS
	name string
}

func (w *memFileWriter) WriteAt(p []byte, off int64) (int, error) {
	w.fs.mu.Lock()
	defer w.fs.mu.Unlock()
	mf := w.fs.files[w.name]
	end := off + int64(len(p))
	if end > int64(len(mf.data)) {
		mf.data = append(mf.data, make([]byte, end-int64(len(mf.data)))...)
	}
	copy(mf.data[off:], p)
	return len(p), nil
}

func (w *memFileWriter) Write(p []byte) (int, error) {
	w.fs.mu.Lock()
	defer w.fs.mu.Unlock()
	mf := w.fs.files[w.name]
	mf.data = append(mf.data, p...)
	return len(p), nil
}

func (w *memFileWriter) Read(p []byte) (int, error) {
	w.fs.mu.RLock()
	defer w.fs.mu.RUnlock()
	mf := w.fs.files[w.name]
	if off := int64(len(w.fs.files[w.name].data)); off >= int64(len(p)) {
		return 0, io.EOF
	}
	n := copy(p, mf.data)
	return n, nil
}

func (w *memFileWriter) ReadAt(p []byte, off int64) (int, error) {
	w.fs.mu.RLock()
	defer w.fs.mu.RUnlock()
	mf := w.fs.files[w.name]
	if off >= int64(len(mf.data)) {
		return 0, io.EOF
	}
	n := copy(p, mf.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (w *memFileWriter) Sync() error             { return nil }
func (w *memFileWriter) Stat() (FileInfo, error) { return nil, os.ErrClosed }
func (w *memFileWriter) ReadAll() ([]byte, error) {
	w.fs.mu.RLock()
	defer w.fs.mu.RUnlock()
	mf := w.fs.files[w.name]
	return append([]byte(nil), mf.data...), nil
}
func (w *memFileWriter) Close() error {
	return nil
}
