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
//
// The buf is assumed to be a multiple of the page size, aligned to
// the page size. Mismatches fall back to no-op.
func madviseDontNeed(buf []byte) {
	if len(buf) == 0 {
		return
	}
	// Best-effort: ignore errors. The kernel may return EINVAL on
	// non-page-aligned regions, which is acceptable.
	_ = madviseFn(buf, syscall.MADV_DONTNEED)
}
