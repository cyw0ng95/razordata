package LC

import (
	"runtime"
	"unsafe"
)

// getg returns the current goroutine's g pointer (assembly implementation).
func getg() unsafe.Pointer

var goidOffset uintptr

func init() {
	goidOffset = findGoidOffset()
}

// findGoidOffset scans the g struct to locate the goid field by comparing
// against the value obtained from runtime.Stack. Runs once at init time.
func findGoidOffset() uintptr {
	expected := goidFromStack()
	gp := getg()
	// Scan up to 512 bytes into the g struct. goid is an int64 field
	// and has been within the first 256 bytes in all Go versions to date.
	for offset := uintptr(0); offset < 512; offset += 8 {
		val := *(*int64)(unsafe.Pointer(uintptr(gp) + offset))
		if uint64(val) == expected {
			return offset
		}
	}
	// Fallback: if we can't find it, return 0 and GoID() will fall
	// back to runtime.Stack.
	return ^uintptr(0) // sentinel: not found
}

// goidFromStack is the slow path: parses goroutine ID from runtime.Stack.
// Used only once at init time to calibrate goidOffset.
func goidFromStack() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	if n < 10 {
		return 0
	}
	s := buf[:n]
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

// GoID returns the current goroutine's ID (REQ000181, REQ002034).
// Uses assembly to read the runtime g pointer, then loads goid directly
// from the g struct at the offset discovered at init time.
// Zero allocations, nanosecond-scale latency.
func GoID() uint64 {
	if goidOffset == ^uintptr(0) {
		return goidFromStack()
	}
	gp := getg()
	return uint64(*(*int64)(unsafe.Pointer(uintptr(gp) + goidOffset)))
}

var _ = getGoroutineID
