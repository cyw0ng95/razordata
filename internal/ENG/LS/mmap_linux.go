//go:build linux

package ls

import (
	"syscall"
	"unsafe"
)

// mmapFile memory-maps the entire file for zero-copy reads.
// Returns the mapped slice or nil on error.
func mmapFile(path string, size int64) []byte {
	if size <= 0 {
		return nil
	}
	f, err := syscall.Open(path, syscall.O_RDONLY, 0)
	if err != nil {
		return nil
	}
	defer syscall.Close(f)

	// MAP_SHARED for consistency across processes; data blocks are
	// read-only so no write-back needed.
	data, err := syscall.Mmap(f, 0, int(size),
		syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil
	}
	// Advise random access pattern for sequential SST reads.
	syscall.Madvise(data, syscall.MADV_RANDOM)

	return data
}

// munmapFile unmaps the memory region. Safe to call on nil.
func munmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return syscall.Munmap(data)
}

// alignPageSize returns the system page size, used for alignment checks.
func alignPageSize() int {
	return int(unsafe.Sizeof(uint64(0)) * 8) // fallback; real page size below
}

// sysPageSize returns the system page size via syscall.
func sysPageSize() int {
	return int(syscall.Getpagesize())
}
