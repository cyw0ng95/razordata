//go:build linux

package sp

import (
	"syscall"

	df "github.com/cyw0ng95/razordata/internal/FIL/DF"
)

const hugePageSize = 2 * 1024 * 1024 // 2 MB

// mmapHugePages allocates a contiguous huge-page-backed memory region
// using MAP_HUGETLB. Returns the raw mmap'd bytes and the open fd
// used for the mmap (which must be closed on cleanup).
// Returns nil, -1 when hugetlbfs is not available or pre-reserved
// pages are insufficient.
func mmapHugePages(pageCount int) (data []byte, fd int, err error) {
	regionSize := pageCount * df.DefaultBlockSize
	if regionSize < hugePageSize {
		regionSize = hugePageSize
	}

	// Try /dev/hugepages/N for MAP_HUGETLB fd-based mmap.
	// Requires pre-reserved huge pages in /proc/sys/vm/nr_hugepages.
	for size := hugePageSize; size <= regionSize; size <<= 1 {
		fd, err = syscall.Open("/dev/hugepages/"+toSizeStr(size), syscall.O_RDWR|syscall.O_CREAT, 0600)
		if err == nil {
			break
		}
	}
	if fd < 0 {
		return nil, -1, syscall.ENODEV // hugetlbfs not available
	}

	// Open the fd for mmap. We'll mmap exactly regionSize.
	data, err = syscall.Mmap(fd, 0, regionSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE)
	if err != nil {
		syscall.Close(fd)
		return nil, -1, err
	}

	// Lock the memory to prevent swap-out (mlockall).
	if err := syscall.Mlockall(syscall.MCL_CURRENT); err != nil {
		syscall.Munmap(data)
		syscall.Close(fd)
		return nil, -1, err
	}

	return data[:regionSize], fd, nil
}

func toSizeStr(size int) string {
	switch size {
	case hugePageSize:
		return "2M"
	case hugePageSize * 2:
		return "4M"
	case hugePageSize * 4:
		return "8M"
	default:
		return ""
	}
}

// unlockHugePages undoes the MCL_CURRENT lock and releases the mmap'd region.
func unlockHugePages(data []byte, fd int) {
	_ = syscall.Munlockall()
	if len(data) > 0 {
		syscall.Munmap(data)
	}
	if fd > 0 {
		syscall.Close(fd)
	}
}

// hugePageEnabled detects whether hugetlbfs is available on this host.
func hugePageEnabled() bool {
	// Try to open /dev/hugepages/2M to probe availability.
	fd, err := syscall.Open("/dev/hugepages/2M", syscall.O_RDONLY, 0)
	if err == nil {
		syscall.Close(fd)
		return true
	}
	return false
}