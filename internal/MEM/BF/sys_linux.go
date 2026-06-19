//go:build linux

package bf

import (
	"syscall"
)

// madviseFn is a package-level var so tests can mock it.
var madviseFn = syscall.Madvise

// madviseDontNeed tells the kernel it can reclaim the pages backing
// buf immediately (MADV_DONTNEED). The syscall is advisory; the
// kernel may ignore it. No-op if buf is empty.
// The buf is assumed to be a multiple of the page size, aligned to
// the page size. Mismatches fall back to no-op.
func madviseDontNeed(buf []byte) {
	if len(buf) == 0 {
		return
	}
	_ = madviseFn(buf, syscall.MADV_DONTNEED)
}

// madviseHugePage hints the kernel to use transparent huge pages
// (MADV_HUGEPAGE) for the given buffer, reducing TLB misses on
// large, frequently-accessed regions. No-op on non-Linux or if buf
// is empty. REQ000302.
func madviseHugePage(buf []byte) {
	if len(buf) == 0 {
		return
	}
	_ = madviseFn(buf, syscall.MADV_HUGEPAGE)
}
