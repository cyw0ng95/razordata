//go:build !linux

package ls

import "errors"

var errMmapUnsupported = errors.New("mmap is only supported on Linux")

// mmapFile is not available on non-Linux platforms.
func mmapFile(path string, size int64) []byte {
	return nil
}

// munmapFile is a no-op on non-Linux platforms.
func munmapFile(data []byte) error {
	return nil
}

// sysPageSize returns 0 to disable mmap on non-Linux platforms.
func sysPageSize() int {
	return 0
}
