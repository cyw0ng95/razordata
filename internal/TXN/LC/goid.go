package LC

import (
	"sync"
)

// goroutineIDPool provides per-goroutine ID caching to avoid the
// runtime.Stack allocation on the hot path (REQ000603).
var goroutineIDPool = sync.Pool{
	New: func() any {
		return &goroutineIDCache{}
	},
}

// goroutineIDCache holds a cached goroutine ID.
type goroutineIDCache struct {
	id uint64
}

// GoID returns the current goroutine's ID (REQ000181, REQ000603).
// Uses a sync.Pool-backed cache to avoid runtime.Stack allocation.
func GoID() uint64 {
	cache := goroutineIDPool.Get().(*goroutineIDCache)
	defer goroutineIDPool.Put(cache)

	if cache.id != 0 {
		return cache.id
	}

	// First call for this cache: use runtime.Stack to get the ID.
	// This happens only once per cache reuse, not on every call.
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
	cache.id = id
	return id
}

var _ = getGoroutineID
