package ls

import (
	"encoding/binary"
	"math/bits"
)

const (
	fnv1aPrime32  = 0x01000193
	fnv1aOffset32 = 0x811C9DC5
)

func fnv1aHash(data []byte, seed uint32) uint32 {
	hash := seed
	for _, b := range data {
		hash ^= uint32(b)
		hash *= fnv1aPrime32
	}
	return hash
}

// fnv1aHash64 reads data in 8-byte chunks for keys >8 bytes,
// processing each byte through the standard FNV-1a XOR-multiply.
func fnv1aHash64(data []byte, seed uint32) uint32 {
	hash := seed
	i := 0
	for i+8 <= len(data) {
		chunk := binary.LittleEndian.Uint64(data[i : i+8])
		hash ^= uint32(chunk & 0xff)
		hash *= fnv1aPrime32
		hash ^= uint32(chunk>>8) & 0xff
		hash *= fnv1aPrime32
		hash ^= uint32(chunk>>16) & 0xff
		hash *= fnv1aPrime32
		hash ^= uint32(chunk>>24) & 0xff
		hash *= fnv1aPrime32
		hash ^= uint32(chunk >> 32) & 0xff
		hash *= fnv1aPrime32
		hash ^= uint32(chunk>>40) & 0xff
		hash *= fnv1aPrime32
		hash ^= uint32(chunk>>48) & 0xff
		hash *= fnv1aPrime32
		hash ^= uint32(chunk>>56) & 0xff
		hash *= fnv1aPrime32
		i += 8
	}
	for ; i < len(data); i++ {
		hash ^= uint32(data[i])
		hash *= fnv1aPrime32
	}
	return hash
}

// nextPow2 rounds v up to the next power of 2.
func nextPow2(v uint32) uint32 {
	if v == 0 {
		return 1
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}

// popcount32 returns the number of set bits in v.
func popcount32(v uint32) int {
	return bits.OnesCount32(v)
}
