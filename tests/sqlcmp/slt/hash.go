package slt

import (
	"crypto/md5"
)

// pooledMD5 returns the hex-encoded MD5 digest of s. REQ002061:
// allocates md5.New() directly instead of using a sync.Pool, because
// hash.Hash is not safe for concurrent use — pooling caused data
// races when multiple goroutines ran SLT tests concurrently.
func pooledMD5(s string) string {
	h := md5.New()
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
