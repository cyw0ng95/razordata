//go:build linux

package df

import "golang.org/x/sys/unix"

// fadviseSequential advises the kernel that the file region will be
// accessed sequentially. The kernel may use this to increase read-ahead
// and improve I/O throughput for scan workloads (REQ000553).
// Returns nil on success or a syscall errno on failure. Errors are
// non-fatal (the operation is best-effort hint).
func fadviseSequential(fd int, offset int64, length int64) error {
	return unix.Fadvise(fd, offset, length, unix.FADV_SEQUENTIAL)
}

// fadviseWillNeed advises the kernel to begin read-ahead for the
// upcoming region. Used to prefetch blocks that are likely to be read
// soon (REQ000553).
func fadviseWillNeed(fd int, offset int64, length int64) error {
	return unix.Fadvise(fd, offset, length, unix.FADV_WILLNEED)
}
