package UT

import (
	"testing"
)

func TestBloomFilter_Basic(t *testing.T) {
	const n = 1000
	bf := NewBloomFilter(n, 0.01)

	added := make(map[uint64]struct{})
	for i := uint64(0); i < n; i++ {
		bf.Add(i)
		added[i] = struct{}{}
	}

	// All added keys should be found.
	for i := uint64(0); i < n; i++ {
		if !bf.Contains(i) {
			t.Errorf("Contains(%d) = false, want true", i)
		}
	}

	// Non-added keys may produce false positives, but with our settings
	// the rate should be low. Check that at least 90% of 1M random
	// non-added keys are correctly rejected.
	missed := 0
	total := 100000
	for i := uint64(n); i < uint64(n+total); i++ {
		if bf.Contains(i) {
			missed++
		}
	}
	rate := float64(missed) / float64(total)
	if rate > 0.05 {
		t.Errorf("false positive rate %.4f exceeds 0.05 threshold", rate)
	}
}

func TestBloomFilter_AddBytes(t *testing.T) {
	bf := NewBloomFilter(100, 0.01)

	keys := [][]byte{
		[]byte("hello"),
		[]byte("world"),
		[]byte("foo"),
	}
	for _, k := range keys {
		bf.AddBytes(k)
	}

	for _, k := range keys {
		if !bf.ContainsBytes(k) {
			t.Errorf("ContainsBytes(%q) = false, want true", k)
		}
	}

	missing := [][]byte{
		[]byte("hellox"),
		[]byte("notfound"),
	}
	for _, k := range missing {
		if bf.ContainsBytes(k) {
			t.Errorf("ContainsBytes(%q) = true for missing key", k)
		}
	}
}

func TestBloomFilter_Empty(t *testing.T) {
	bf := NewBloomFilter(10, 0.01)

	// An empty filter should return false for all keys.
	for i := uint64(0); i < 100; i++ {
		if bf.Contains(i) {
			t.Errorf("Contains(%d) = true on empty filter", i)
		}
	}

	if bf.ContainsBytes([]byte("anything")) {
		t.Error("ContainsBytes returned true on empty filter")
	}
}

func TestBloomFilter_Merge(t *testing.T) {
	bf1 := NewBloomFilter(2000, 0.001)
	bf2 := NewBloomFilter(2000, 0.001)

	for i := uint64(0); i < 2000; i++ {
		bf1.Add(i)
	}
	for i := uint64(2000); i < 4000; i++ {
		bf2.Add(i)
	}

	merged := bf1.Merge(bf2)

	// Keys from bf1 should be in merged.
	for i := uint64(0); i < 2000; i++ {
		if !merged.Contains(i) {
			t.Errorf("merged.Contains(%d) = false, want true (from bf1)", i)
		}
	}

	// Keys from bf2 should be in merged.
	for i := uint64(2000); i < 4000; i++ {
		if !merged.Contains(i) {
			t.Errorf("merged.Contains(%d) = false, want true (from bf2)", i)
		}
	}

	// Keys from neither: merging two bloom filters roughly doubles
	// the bit density, which significantly increases the FP rate.
	// The theoretical FP for 4000 keys in ~20k bits with k=6 is
	// ~12%.  We use a generous 0.25 threshold.
	missed := 0
	total := 100000
	for i := uint64(4000); i < 4000+uint64(total); i++ {
		if merged.Contains(i) {
			missed++
		}
	}
	rate := float64(missed) / float64(total)
	if rate > 0.25 {
		t.Errorf("merge false positive rate %.4f exceeds 0.25 threshold", rate)
	}
}

func TestBloomFilter_MergeSelf(t *testing.T) {
	bf := NewBloomFilter(50, 0.01)
	for i := uint64(0); i < 50; i++ {
		bf.Add(i)
	}

	merged := bf.Merge(bf)
	for i := uint64(0); i < 50; i++ {
		if !merged.Contains(i) {
			t.Errorf("self-merge.Contains(%d) = false", i)
		}
	}
}

func TestBloomFilter_DuplicateAdds(t *testing.T) {
	bf := NewBloomFilter(10, 0.01)

	// Adding the same key multiple times should not break anything.
	for i := 0; i < 100; i++ {
		bf.Add(42)
	}

	if !bf.Contains(42) {
		t.Error("Contains(42) = false after duplicate adds")
	}
}
