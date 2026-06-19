package bf

import (
	"os"
	"sync"
)

// PMemFile manages a persistent memory file for cold-page spill (REQ000302).
type PMemFile struct {
	file *os.File
	path string
	mu   sync.RWMutex
}

// CreatePMemFile opens or creates a PMem file at path.
func CreatePMemFile(path string) (*PMemFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &PMemFile{file: f, path: path}, nil
}

func (pm *PMemFile) Read(buf []byte, offset int64) (int, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.file.ReadAt(buf, offset)
}

func (pm *PMemFile) Write(buf []byte, offset int64) (int, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.file.WriteAt(buf, offset)
}

func (pm *PMemFile) Sync() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.file.Sync()
}

func (pm *PMemFile) Path() string { return pm.path }

func (pm *PMemFile) Close() error {
	if pm == nil {
		return nil
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.file.Close()
}
