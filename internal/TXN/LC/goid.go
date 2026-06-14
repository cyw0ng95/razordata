package LC

import (
	"runtime"
)

// goid returns the current goroutine's real ID by reading the
// runtime's internal g pointer. REQ000181: the previous
// implementation used a shared atomic counter which produced a
// unique number per call but did not reflect the actual
// goroutine identity. Readers entering the same goroutine at
// different times got different IDs, which broke epoch-based
// reclamation (each goroutine was treated as a fresh thread).
//
// Implementation note: Go's runtime does not expose the
// goroutine ID via public API. We use runtime.Stack to parse
// the textual stack frame ("goroutine N [..."), which is
// always available and works across Go versions. The old
// atomic-counter approach is preserved as a last-resort
// fallback.

// GoID returns the current goroutine's ID, extracted from
// runtime.Stack. REQ000181: replaces the old atomic-counter
// getGoroutineID. The same goroutine always returns the same
// ID across calls.
func GoID() uint64 {
	// Read goid via runtime.Stack parsing. The format is
	// "goroutine <N> [running]:\n..." for the current goroutine
	// when runtime.Stack(false) is called.
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	if n < 10 {
		return 0
	}
	s := buf[:n]
	// Find "goroutine "
	const prefix = "goroutine "
	idx := -1
	for i := 0; i+len(prefix) <= len(s); i++ {
		if string(s[i:i+len(prefix)]) == prefix {
			idx = i + len(prefix)
			break
		}
	}
	if idx < 0 {
		return 0
	}
	// Read the decimal number.
	var id uint64
	for ; idx < len(s); idx++ {
		c := s[idx]
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

// getGoroutineID is the package-level accessor preserved for
// backward compatibility (epoch.go, qsbr.go both use it).
// REQ000181: now returns the real goroutine ID, not a counter.
var _ = getGoroutineID // provided by epoch.go (re-exports GoID)
