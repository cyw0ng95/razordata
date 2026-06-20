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
