//go:build linux

package df

import (
	"syscall"
)

// mmapBlock maps the file at offset for size bytes. Returns the
// mapped slice and a munmap function. Caller must call munmap when
// done.
//
// On error, returns nil and a non-nil error.
//
// Implementation note: uses syscall.Mmap directly to avoid pulling
// in golang.org/x/sys/unix (which is already imported by the main
// df.go file).
func mmapBlock(fd int, offset int64, size int) ([]byte, error) {
	data, err := syscall.Mmap(
		fd,
		offset,
		size,
		syscall.PROT_READ,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// munmapBlock unmaps a previously mapped block.
func munmapBlock(data []byte) error {
	return syscall.Munmap(data)
}
