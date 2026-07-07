//go:build linux

package wr

import (
	"os"
	"syscall"
)

// MmapShm creates or opens the -shm file at path, sets its size to
// WalShmSize(), and mmaps it with MAP_SHARED. Returns the mapped
// region and the file handle (caller must call MunmapShm to release).
func MmapShm(path string) ([]byte, *os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, nil, err
	}
	size := WalShmSize()
	if err := f.Truncate(int64(size)); err != nil {
		f.Close()
		return nil, nil, err
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return data, f, nil
}

// MunmapShm unmaps the shared memory region and closes the file.
func MunmapShm(data []byte, f *os.File) error {
	var err error
	if data != nil {
		err = syscall.Munmap(data)
	}
	if f != nil {
		if e := f.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// ShmPath returns the -shm file path for a given database directory.
func ShmPath(dbDir string) string {
	return dbDir + "-shm"
}

// WalShmOffset constants for the -shm file layout.
const (
	shmLockOffset = 0
	shmHdrOffset  = LockRegionSize
	shmHashOffset = LockRegionSize + WalIndexHdrSize
)