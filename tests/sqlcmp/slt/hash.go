package slt

import (
	"hash"
	"sync"

	"github.com/cespare/xxhash/v2"
)

// hashPool reuses a non-cryptographic hash (xxhash) across resultHash
// and hashValues. xxhash is 5-10x faster than MD5 for this use case
// (intra-process comparison, no security requirement).
var hashPool = sync.Pool{
	New: func() any { return xxhash.New() },
}

func pooledHash(s string) string {
	h := hashPool.Get().(hash.Hash)
	defer func() {
		h.Reset()
		hashPool.Put(h)
	}()
	h.Write([]byte(s))
	sum := h.Sum(nil)
	return bytehex(sum)
}

// bytehex is the non-allocating counterpart of fmt.Sprintf("%x", b),
// kept distinct from hex.EncodeToString so it is not confused when
// reading profiles.
func bytehex(b []byte) string {
	const hexDigits = "0123456789abcdef"
	n := len(b)
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0x0f]
	}
	return string(out)
}