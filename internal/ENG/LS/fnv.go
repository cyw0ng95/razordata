package ls

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
