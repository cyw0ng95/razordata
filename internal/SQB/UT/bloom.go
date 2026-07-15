package UT

import (
	"encoding/binary"
	"math"

	"github.com/cespare/xxhash/v2"
)

// BloomFilter is a simple fixed-size bloom filter using packed uint64
// words and xxhash-derived hash functions for membership testing.
type BloomFilter struct {
	bits   []uint64
	size   int
	hashes int
}

// NewBloomFilter creates a bloom filter sized for expectedEntries with
// a target false-positive rate of fpRate.
func NewBloomFilter(expectedEntries int, fpRate float64) *BloomFilter {
	if expectedEntries <= 0 {
		expectedEntries = 16
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}

	n := float64(expectedEntries)
	p := float64(fpRate)

	bits := int(-n * math.Ln2 * math.Log(p) / (math.Ln2 * math.Ln2))
	if bits < 64 {
		bits = 64
	}
	words := (bits + 63) / 64
	hashes := int(float64(bits) / n * math.Ln2)
	if hashes < 1 {
		hashes = 1
	}
	if hashes > 30 {
		hashes = 30
	}

	return &BloomFilter{
		bits:   make([]uint64, words),
		size:   bits,
		hashes: hashes,
	}
}

// hashUint64 converts a uint64 to 8 bytes and computes xxhash.
func hashUint64(v uint64) uint64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	return xxhash.Sum64(buf[:])
}

// splitMix64 derives a secondary hash from a primary hash using
// the SplitMix64 algorithm.
func splitMix64(x uint64) uint64 {
	x = (x + 0x9e3779b97f4a7c15) & 0xffffffffffffffff
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x = x ^ (x >> 31)
	return x
}

// hashAt computes the i-th hash for a seed value using SplitMix64.
func hashAt(seed uint64, i int) uint64 {
	h := seed
	for j := 0; j < i; j++ {
		h = splitMix64(h)
	}
	return h
}

// bitIndex returns the (wordIndex, bitPos) for a given bit offset.
func (b *BloomFilter) bitIndex(offset int) (int, uint) {
	return offset >> 6, uint(offset & 63)
}

// Add inserts a uint64 key into the filter.
func (b *BloomFilter) Add(key uint64) {
	seed := hashUint64(key)
	for i := 0; i < b.hashes; i++ {
		h := hashAt(seed, i)
		offset := int(h % uint64(b.size))
		w, bit := b.bitIndex(offset)
		b.bits[w] |= 1 << bit
	}
}

// AddBytes hashes the byte slice and adds the result to the filter.
func (b *BloomFilter) AddBytes(key []byte) {
	seed := xxhash.Sum64(key)
	for i := 0; i < b.hashes; i++ {
		h := hashAt(seed, i)
		offset := int(h % uint64(b.size))
		w, bit := b.bitIndex(offset)
		b.bits[w] |= 1 << bit
	}
}

// Contains reports whether the key may be in the filter.
func (b *BloomFilter) Contains(key uint64) bool {
	seed := hashUint64(key)
	for i := 0; i < b.hashes; i++ {
		h := hashAt(seed, i)
		offset := int(h % uint64(b.size))
		w, bit := b.bitIndex(offset)
		if (b.bits[w] & (1 << bit)) == 0 {
			return false
		}
	}
	return true
}

// ContainsBytes reports whether the byte slice may be in the filter.
func (b *BloomFilter) ContainsBytes(key []byte) bool {
	seed := xxhash.Sum64(key)
	for i := 0; i < b.hashes; i++ {
		h := hashAt(seed, i)
		offset := int(h % uint64(b.size))
		w, bit := b.bitIndex(offset)
		if (b.bits[w] & (1 << bit)) == 0 {
			return false
		}
	}
	return true
}

// Merge combines b and other into a new BloomFilter whose bits are
// the bitwise OR of both inputs. The returned filter has size equal
// to the larger of the two and hash count equal to the larger (most
// discriminative) of the two.
func (b *BloomFilter) Merge(other *BloomFilter) *BloomFilter {
	larger := b
	smaller := other
	if other.size > b.size {
		larger = other
		smaller = b
	}

	result := &BloomFilter{
		bits:   make([]uint64, len(larger.bits)),
		size:   larger.size,
		hashes: larger.hashes,
	}
	copy(result.bits, larger.bits)
	for i := range smaller.bits {
		if i < len(result.bits) {
			result.bits[i] |= smaller.bits[i]
		}
	}
	return result
}
