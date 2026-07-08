package slt

import (
	"crypto/md5"
	"hash"
	"sync"
)

// md5Pool is a sync.Pool of hash.Hash (md5) reused across resultHash
// and hashValues to eliminate per-call allocation of the hasher.
var md5Pool = sync.Pool{
	New: func() any { return md5.New() },
}

// pooledMD5 returns the hex-encoded MD5 digest of s, using the
// package-level md5Pool to avoid allocating a new hasher per call.
func pooledMD5(s string) string {
	h := md5Pool.Get().(hash.Hash)
	defer func() {
		h.Reset()
		md5Pool.Put(h)
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