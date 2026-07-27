package PX

import (
	"sync"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestPipelineCache_GetByText_Hit(t *testing.T) {
	cache := NewPipelineCache(16)
	spec := &PipelineSpec{SQLText: "SELECT 1", MemoKey: "sel:t1"}
	p := PS.NewParser("SELECT 1")
	stmt, _ := p.Parse()
	p.Close()

	cache.Put(spec, stmt)

	gotSpec, gotStmt := cache.GetByText("SELECT 1")
	if gotSpec == nil {
		t.Fatal("expected cache hit by text")
	}
	if gotStmt == nil {
		t.Fatal("expected cached stmt")
	}
}

func TestPipelineCache_GetByText_Miss(t *testing.T) {
	cache := NewPipelineCache(16)
	gotSpec, _ := cache.GetByText("SELECT 1")
	if gotSpec != nil {
		t.Fatal("expected cache miss")
	}
}

func TestPipelineCache_GetByMemo_Hit(t *testing.T) {
	cache := NewPipelineCache(16)
	spec := &PipelineSpec{SQLText: "SELECT 1", MemoKey: "sel:t1"}
	p := PS.NewParser("SELECT 1")
	stmt, _ := p.Parse()
	p.Close()

	cache.Put(spec, stmt)

	gotSpec := cache.GetByMemo("sel:t1")
	if gotSpec == nil {
		t.Fatal("expected cache hit by memo")
	}
}

func TestPipelineCache_GetByMemo_Miss(t *testing.T) {
	cache := NewPipelineCache(16)
	gotSpec := cache.GetByMemo("sel:t1")
	if gotSpec != nil {
		t.Fatal("expected cache miss")
	}
}

func TestPipelineCache_Eviction(t *testing.T) {
	cache := NewPipelineCache(3)

	for i := range 5 {
		sql := "SELECT " + string(rune('0'+i))
		spec := &PipelineSpec{SQLText: sql, MemoKey: "key" + string(rune('0'+i))}
		cache.Put(spec, nil)
	}

	// Cache should be at maxSize
	if cache.Len() != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", cache.Len())
	}

	// First two entries should have been evicted
	if spec, _ := cache.GetByText("SELECT 0"); spec != nil {
		t.Fatal("expected SELECT 0 to be evicted")
	}
	if spec, _ := cache.GetByText("SELECT 1"); spec != nil {
		t.Fatal("expected SELECT 1 to be evicted")
	}

	// Last three should still be present
	if spec, _ := cache.GetByText("SELECT 2"); spec == nil {
		t.Fatal("expected SELECT 2 to be present")
	}
	if spec, _ := cache.GetByText("SELECT 3"); spec == nil {
		t.Fatal("expected SELECT 3 to be present")
	}
	if spec, _ := cache.GetByText("SELECT 4"); spec == nil {
		t.Fatal("expected SELECT 4 to be present")
	}
}

func TestPipelineCache_Clear(t *testing.T) {
	cache := NewPipelineCache(16)
	spec := &PipelineSpec{SQLText: "SELECT 1", MemoKey: "sel:t1"}
	cache.Put(spec, nil)

	cache.Clear()

	if cache.Len() != 0 {
		t.Fatalf("expected 0 entries after clear, got %d", cache.Len())
	}
	if spec, _ := cache.GetByText("SELECT 1"); spec != nil {
		t.Fatal("expected miss after clear")
	}
}

func TestPipelineCache_Concurrent(t *testing.T) {
	cache := NewPipelineCache(64)
	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sql := "SELECT " + string(rune('0'+n))
			key := "key" + string(rune('0'+n))
			spec := &PipelineSpec{SQLText: sql, MemoKey: key}
			cache.Put(spec, nil)
			cache.GetByText(sql)
			cache.GetByMemo(key)
		}(i)
	}
	wg.Wait()
}

func TestPipelineCache_EvictionRemovesFromAllMaps(t *testing.T) {
	cache := NewPipelineCache(2)

	// Insert entry with both text and memo keys
	spec1 := &PipelineSpec{SQLText: "SELECT 1", MemoKey: "sel:t1"}
	cache.Put(spec1, nil)

	spec2 := &PipelineSpec{SQLText: "SELECT 2", MemoKey: "sel:t2"}
	cache.Put(spec2, nil)

	// Both should be present
	if cache.GetByMemo("sel:t1") == nil {
		t.Fatal("sel:t1 should be present")
	}
	if cache.GetByMemo("sel:t2") == nil {
		t.Fatal("sel:t2 should be present")
	}

	// Insert a third entry, triggering eviction of spec1
	spec3 := &PipelineSpec{SQLText: "SELECT 3", MemoKey: "sel:t3"}
	cache.Put(spec3, nil)

	// spec1 should be evicted from both text and memo maps
	if spec, _ := cache.GetByText("SELECT 1"); spec != nil {
		t.Fatal("SELECT 1 should be evicted from text map")
	}
	if cache.GetByMemo("sel:t1") != nil {
		t.Fatal("sel:t1 should be evicted from memo map")
	}
}

func TestNewPipelineCache_DefaultSize(t *testing.T) {
	cache := NewPipelineCache(0)
	if cache.maxSize != 256 {
		t.Fatalf("expected default maxSize 256, got %d", cache.maxSize)
	}
}
