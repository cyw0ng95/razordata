package bf

import (
	"testing"
)

// TestWTinyLFU_Estimate_Basic verifies estimate starts at 0 for
// unseen keys and increases with accesses. REQ000303.
func TestWTinyLFU_Estimate_Basic(t *testing.T) {
	w := newWTinyLFU()
	key := uint64(42)
	if got := w.estimate(key); got != 0 {
		t.Errorf("estimate before any access: %d, want 0", got)
	}
	w.increment(key)
	if got := w.estimate(key); got < 1 {
		t.Errorf("estimate after 1 increment: %d, want >=1", got)
	}
	for i := 0; i < 100; i++ {
		w.increment(key)
	}
	// Should saturate at 15 (4-bit cap).
	if got := w.estimate(key); got != 15 {
		t.Errorf("estimate after 101 increments: %d, want 15 (saturated)", got)
	}
}

// TestWTinyLFU_Admit_FavorsFrequent verifies the admission test
// prefers a hot key over a one-hit wonder. REQ000303.
func TestWTinyLFU_Admit_FavorsFrequent(t *testing.T) {
	w := newWTinyLFU()
	hotKey := uint64(1)
	coldKey := uint64(2)
	// Hot key: 10 increments.
	for i := 0; i < 10; i++ {
		w.increment(hotKey)
	}
	// New key: 1 increment. Candidate being evicted: hot (10 increments).
	// admit() also increments the new key, but 1 < 10 so it should reject.
	if w.admit(coldKey, hotKey) {
		// A one-hit wonder should NOT evict a 10-hit key.
		t.Errorf("admit should reject a cold new key over a hot candidate")
	}
	// Reverse: admit the hot key over the cold candidate.
	if !w.admit(hotKey, coldKey) {
		t.Errorf("admit should accept a hot new key over a cold candidate")
	}
}

// TestWTinyLFU_Stats verifies admitted/rejected counters.
func TestWTinyLFU_Stats(t *testing.T) {
	w := newWTinyLFU()
	hotKey := uint64(1)
	for i := 0; i < 10; i++ {
		w.increment(hotKey)
	}
	w.admit(uint64(2), hotKey) // reject
	w.admit(hotKey, uint64(2)) // accept
	stats := w.stats()
	if stats.Rejected != 1 {
		t.Errorf("rejected: %d, want 1", stats.Rejected)
	}
	if stats.Admitted != 1 {
		t.Errorf("admitted: %d, want 1", stats.Admitted)
	}
}

// TestWTinyLFU_RecordHit verifies recordHit returns true for hot
// keys (frequency > 1).
func TestWTinyLFU_RecordHit(t *testing.T) {
	w := newWTinyLFU()
	key := uint64(7)
	if w.recordHit(key) {
		t.Errorf("first hit should not be hot")
	}
	if !w.recordHit(key) {
		t.Errorf("second hit should be hot")
	}
}
