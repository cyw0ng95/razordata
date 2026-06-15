package slt

import (
	"crypto/md5"
)

// md5sum is a tiny indirection so diff.go does not need to import
// crypto/md5 directly. Returning the raw bytes lets callers format
// the digest however they want.
func md5sum(s string) []byte {
	h := md5.Sum([]byte(s))
	return h[:]
}
