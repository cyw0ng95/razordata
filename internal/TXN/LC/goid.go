package LC

import (
	"runtime"
)

// GoID returns the current goroutine's ID (REQ000181).
func GoID() uint64 {
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

var _ = getGoroutineID
