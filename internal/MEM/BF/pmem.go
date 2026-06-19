package bf

import (
	"os"
	"sync"
)

// PMemFile manages a persistent memory (PMem) file for cold-page
// spill in a two-tier buffer pool. The file is served from a
// DAX-mounted filesystem or a regular disk; the abstraction is
// identical. REQ000302.
// All public methods are goroutine-safe.
type PMemFile struct {
	file *os.File
	path string
	mu   sync.RWMutex
}

// CreatePMemFile opens (or creates) a PMem file at path. The file is
// created with read-write permissions. Returns an error if the
// underlying fs operation fails.
func CreatePMemFile(path string) (*PMemFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &PMemFile{file: f, path: path}, nil
}

// Read reads len(buf) bytes from the file starting at offset into
// buf. Returns the number of bytes read.
func (pm *PMemFile) Read(buf []byte, offset int64) (int, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.file.ReadAt(buf, offset)
}

// Write writes buf to the file at offset. Returns the number of
// bytes written.
func (pm *PMemFile) Write(buf []byte, offset int64) (int, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.file.WriteAt(buf, offset)
}

// Sync flushes the file to PMem (fsync).
func (pm *PMemFile) Sync() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.file.Sync()
}

// Path returns the underlying file path.
func (pm *PMemFile) Path() string { return pm.path }

// Close closes the PMem file. Safe to call with a nil receiver.
func (pm *PMemFile) Close() error {
	if pm == nil {
		return nil
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.file.Close()
}
