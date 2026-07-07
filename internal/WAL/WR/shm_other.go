//go:build !linux

package wr

import (
	"errors"
	"os"
)

var errShmNotSupported = errors.New("wr: -shm not supported on this platform")

func MmapShm(path string) ([]byte, *os.File, error) {
	return nil, nil, errShmNotSupported
}

func MunmapShm(data []byte, f *os.File) error {
	if f != nil {
		return f.Close()
	}
	return nil
}

func ShmPath(dbDir string) string {
	return dbDir + "-shm"
}