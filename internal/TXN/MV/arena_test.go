package MV

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestArenaAlloc(t *testing.T) {
	a := newArena()

	mem1 := a.Alloc(100)
	if mem1 == nil {
		t.Fatal("expected non-nil allocation")
	}
	if len(mem1) != 100 {
		t.Fatalf("expected 100 bytes, got %d", len(mem1))
	}

	mem2 := a.Alloc(50)
	if mem2 == nil {
		t.Fatal("expected non-nil allocation")
	}
	if &mem1[0] == &mem2[0] {
		t.Fatal("allocations should not overlap")
	}

	offset := a.offset.Load()
	if offset != 150 {
		t.Fatalf("expected offset 150, got %d", offset)
	}
}

func TestArenaExhausted(t *testing.T) {
	a := newArena()

	mem := a.Alloc(arenaSize)
	if mem == nil {
		t.Fatal("first allocation at arenaSize should succeed")
	}

	mem2 := a.Alloc(1)
	if mem2 != nil {
		t.Fatal("allocation beyond arena size should return nil")
	}
}

func TestArenaCAS(t *testing.T) {
	a := newArena()

	done := make(chan bool)
	var lastOffset int64

	for i := 0; i < runtime.NumCPU()*2; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				ptr := a.Alloc(16)
				if ptr != nil {
					atomic.StoreInt64(&lastOffset, a.offset.Load())
				}
			}
			done <- true
		}()
	}

	for i := 0; i < runtime.NumCPU()*2; i++ {
		<-done
	}

	if lastOffset == 0 {
		t.Fatal("expected some allocations to succeed")
	}
}

func TestArenaRemaining(t *testing.T) {
	a := newArena()

	initial := a.remaining()
	if initial != arenaSize {
		t.Fatalf("expected initial remaining %d, got %d", arenaSize, initial)
	}

	a.Alloc(100)
	after := a.remaining()
	if after != arenaSize-100 {
		t.Fatalf("expected remaining %d, got %d", arenaSize-100, after)
	}
}

func TestArenaPool(t *testing.T) {
	a1 := getArena()
	a2 := getArena()

	if a1 == nil || a2 == nil {
		t.Fatal("getArena should return non-nil arena")
	}

	putArena(a1)
	putArena(a2)

	a3 := getArena()
	if a3 == nil {
		t.Fatal("should get arena from pool")
	}
}

func TestArenaPoolSlice(t *testing.T) {
	sliceLen := len(arenaPoolSlice)
	if sliceLen != runtime.GOMAXPROCS(0) {
		t.Fatalf("expected arenaPoolSlice size %d, got %d", runtime.GOMAXPROCS(0), sliceLen)
	}
}

func TestArenaConcurrency(t *testing.T) {
	a := newArena()

	var wg sync.WaitGroup
	iterations := 100
	goroutines := 10

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				mem := a.Alloc(64)
				if mem == nil {
					t.Errorf("allocation failed unexpectedly")
					break
				}
			}
		}()
	}

	wg.Wait()

	expectedOffset := int64(goroutines * iterations * 64)
	if a.offset.Load() != expectedOffset {
		t.Errorf("expected offset %d, got %d", expectedOffset, a.offset.Load())
	}
}
