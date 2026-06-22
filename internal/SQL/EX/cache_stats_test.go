package EX

import (
	"testing"
)

func TestCacheStats_Basic(t *testing.T) {
	cs := NewCacheStats(100)
	
	cs.RecordHit()
	cs.RecordHit()
	cs.RecordMiss()
	cs.RecordEviction()
	
	if cs.Hits != 2 {
		t.Errorf("Hits = %d, want 2", cs.Hits)
	}
	if cs.Misses != 1 {
		t.Errorf("Misses = %d, want 1", cs.Misses)
	}
	if cs.Evictions != 1 {
		t.Errorf("Evictions = %d, want 1", cs.Evictions)
	}
	
	rate := cs.GetHitRate()
	expectedRate := 66.66666666666667 // 2/3 * 100
	if rate < expectedRate-0.01 || rate > expectedRate+0.01 {
		t.Errorf("GetHitRate() = %f, want ~%f", rate, expectedRate)
	}
	
summary := cs.GetSummary()
	t.Logf("Summary: %s", summary)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestCacheStats_Empty(t *testing.T) {
	cs := NewCacheStats(100)
	
	rate := cs.GetHitRate()
	if rate != 0 {
		t.Errorf("GetHitRate() = %f, want 0 for empty cache", rate)
	}
}

func TestStmtCache_Basic(t *testing.T) {
	sc := NewStmtCache(3)
	
	entry := &StmtCacheEntry{
		SQL:      "SELECT 1",
		UseCount: 0,
	}
	
	// Put entry
	sc.Put("key1", entry)
	
	// Get entry
	got := sc.Get("key1")
	if got == nil {
		t.Fatal("expected entry to be found")
	}
	if got.UseCount != 1 {
		t.Errorf("UseCount = %d, want 1", got.UseCount)
	}
	
	// Get missing entry
	missing := sc.Get("key2")
	if missing != nil {
		t.Error("expected nil for missing entry")
	}
	
	// Check stats
	stats := sc.GetStats()
	if stats.Hits != 1 {
		t.Errorf("Hits = %d, want 1", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
}

func TestStmtCache_Eviction(t *testing.T) {
	sc := NewStmtCache(2)
	
	sc.Put("key1", &StmtCacheEntry{SQL: "SELECT 1"})
	sc.Put("key2", &StmtCacheEntry{SQL: "SELECT 2"})
	sc.Put("key3", &StmtCacheEntry{SQL: "SELECT 3"}) // Should evict key1
	
	if sc.Size() != 2 {
		t.Errorf("Size = %d, want 2 after eviction", sc.Size())
	}
	
stats := sc.GetStats()
	if stats.Evictions != 1 {
		t.Errorf("Evictions = %d, want 1", stats.Evictions)
	}
}

func TestStmtCache_Clear(t *testing.T) {
	sc := NewStmtCache(10)
	
	sc.Put("key1", &StmtCacheEntry{SQL: "SELECT 1"})
	sc.Put("key2", &StmtCacheEntry{SQL: "SELECT 2"})
	
	if sc.Size() != 2 {
		t.Errorf("Size = %d, want 2", sc.Size())
	}
	
	sc.Clear()
	
	if sc.Size() != 0 {
		t.Errorf("Size after Clear = %d, want 0", sc.Size())
	}
}

func TestStmtCache_Concurrency(t *testing.T) {
	sc := NewStmtCache(100)
	
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			sc.Put(string(rune(i)), &StmtCacheEntry{SQL: "SELECT"})
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			sc.Get(string(rune(i)))
		}
		done <- struct{}{}
	}()
	
	<-done
	<-done
	
	_ = sc.Size() // Just check it doesn't panic
}
