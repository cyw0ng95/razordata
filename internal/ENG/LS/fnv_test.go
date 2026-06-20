package ls

import (
	"math/rand/v2"
	"testing"
)

func TestFNV1a_EmptyInput(t *testing.T) {
	h := fnv1aHash(nil, fnv1aOffset32)
	if h != fnv1aOffset32 {
		t.Errorf("hash of empty input with offset basis seed = 0x%x, want 0x%x", h, fnv1aOffset32)
	}
}

func TestFNV1a_Hello(t *testing.T) {
	h := fnv1aHash([]byte("hello"), fnv1aOffset32)
	const want uint32 = 0x4f9f2cab
	if h != want {
		t.Errorf("hash of \"hello\" = 0x%x, want 0x%x", h, want)
	}
}

func TestFNV1a_DifferentSeeds(t *testing.T) {
	data := []byte("test data")
	h1 := fnv1aHash(data, 0)
	h2 := fnv1aHash(data, 1)
	if h1 == h2 {
		t.Error("hashes with seeds 0 and 1 should differ")
	}
}

func TestFNV1a_Deterministic(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog")
	h1 := fnv1aHash(data, 42)
	for i := 0; i < 100; i++ {
		h2 := fnv1aHash(data, 42)
		if h1 != h2 {
			t.Fatalf("non-deterministic hash on iteration %d: 0x%x vs 0x%x", i, h1, h2)
		}
	}
}

func TestFNV1a_RandomBytesDeterministic(t *testing.T) {
	rng := rand.New(rand.NewPCG(99, 100))
	for i := 0; i < 1000; i++ {
		n := rng.IntN(256)
		buf := make([]byte, n)
		for j := range buf {
			buf[j] = byte(rng.IntN(256))
		}
		seed := rng.Uint32()
		h1 := fnv1aHash(buf, seed)
		h2 := fnv1aHash(buf, seed)
		if h1 != h2 {
			t.Fatalf("non-deterministic hash for seed=0x%x, len=%d: 0x%x vs 0x%x", seed, n, h1, h2)
		}
	}
}

func TestFNV1a64_EmptyInput(t *testing.T) {
	h := fnv1aHash64(nil, fnv1aOffset32)
	if h != fnv1aOffset32 {
		t.Errorf("hash64 of empty input = 0x%x, want 0x%x", h, fnv1aOffset32)
	}
}

func TestFNV1a64_ShortKey(t *testing.T) {
	h := fnv1aHash64([]byte("hello"), fnv1aOffset32)
	// Short keys should still work correctly (tail loop only)
	hRef := fnv1aHash([]byte("hello"), fnv1aOffset32)
	if h != hRef {
		t.Errorf("hash64 of \"hello\" = 0x%x, want 0x%x (matches byte-at-a-time)", h, hRef)
	}
}

func TestFNV1a64_Exact8Bytes(t *testing.T) {
	data := []byte("12345678")
	h := fnv1aHash64(data, fnv1aOffset32)
	if h == 0 {
		t.Error("hash64 of 8-byte input should not be zero")
	}
}

func TestFNV1a64_LongKey(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog")
	h := fnv1aHash64(data, fnv1aOffset32)
	if h == 0 {
		t.Error("hash64 of long input should not be zero")
	}
}

func TestFNV1a64_MatchesByteAtATime(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 43))
	for i := 0; i < 1000; i++ {
		n := rng.IntN(256)
		buf := make([]byte, n)
		for j := range buf {
			buf[j] = byte(rng.IntN(256))
		}
		seed := rng.Uint32()
		h64 := fnv1aHash64(buf, seed)
		hRef := fnv1aHash(buf, seed)
		if h64 != hRef {
			t.Fatalf("hash64 mismatch for seed=0x%x, len=%d: 0x%x vs 0x%x", seed, n, h64, hRef)
		}
	}
}

func TestFNV1a64_DifferentSeeds(t *testing.T) {
	data := []byte("test data for 64-bit chunk processing")
	h1 := fnv1aHash64(data, 0)
	h2 := fnv1aHash64(data, 1)
	if h1 == h2 {
		t.Error("hash64 with seeds 0 and 1 should differ")
	}
}

func TestFNV1a64_Deterministic(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog and some extra bytes")
	h1 := fnv1aHash64(data, 42)
	for i := 0; i < 100; i++ {
		h2 := fnv1aHash64(data, 42)
		if h1 != h2 {
			t.Fatalf("non-deterministic hash64 on iteration %d: 0x%x vs 0x%x", i, h1, h2)
		}
	}
}

func TestNextPow2(t *testing.T) {
	tests := []struct {
		in   uint32
		want uint32
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 4},
		{5, 8},
		{7, 8},
		{8, 8},
		{9, 16},
		{15, 16},
		{16, 16},
		{17, 32},
		{100, 128},
		{255, 256},
		{256, 256},
		{1023, 1024},
		{1024, 1024},
	}
	for _, tt := range tests {
		got := nextPow2(tt.in)
		if got != tt.want {
			t.Errorf("nextPow2(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
