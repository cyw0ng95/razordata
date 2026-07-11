package UT

// BloomFilter is a simple probabilistic set membership test for hash
// join pre-filtering. Uses a single uint64 hash and a bit array.
// Default false positive probability ~0.01 for reasonable fill ratios.
// REQ001506.
type BloomFilter struct {
	bits  []uint64
	mask  uint64
	shift uint
}

// NewBloomFilter creates a bloom filter sized for n expected elements.
// The bit array is sized to 8 × n bits (≈ 1% fpp).
func NewBloomFilter(n int) *BloomFilter {
	if n < 1 {
		n = 1
	}
	// Round up to power of 2 for fast modulo with & mask.
size := bloomNextPow2(n * 8)
	return &BloomFilter{
		bits:  make([]uint64, size/64),
		mask:  uint64(size - 1),
		shift: 6,
	}
}

// Add inserts a hash value into the bloom filter.
func (bf *BloomFilter) Add(h uint64) {
	h1 := h
	h2 := h>>32 | h<<32
	bf.bits[(h1&bf.mask)>>bf.shift] |= 1 << (h1 & 63)
	bf.bits[(h2&bf.mask)>>bf.shift] |= 1 << (h2 & 63)
}

// MaybeContains returns true if the hash might be in the set (false
// positives possible) or false if it is definitely not in the set.
func (bf *BloomFilter) MaybeContains(h uint64) bool {
	h1 := h
	h2 := h>>32 | h<<32
	if bf.bits[(h1&bf.mask)>>bf.shift]&(1<<(h1&63)) == 0 {
		return false
	}
	if bf.bits[(h2&bf.mask)>>bf.shift]&(1<<(h2&63)) == 0 {
		return false
	}
	return true
}

func bloomNextPow2(v int) int {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}