package ls

// FNV-1a constants (REQ000174).
// Standard FNV-1a 32-bit parameters for BloomFilter double hashing.
const (
	fnv1aPrime32  = 0x01000193
	fnv1aOffset32 = 0x811C9DC5
)

// fnv1aHash computes FNV-1a hash with specified seed.
// FNV-1a: hash = offset; for each byte: hash = hash XOR byte; hash = hash * prime
func fnv1aHash(data []byte, seed uint32) uint32 {
	hash := seed
	for _, b := range data {
		hash ^= uint32(b)
		hash *= fnv1aPrime32
	}
	return hash
}
