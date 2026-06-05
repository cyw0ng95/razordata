package LC

import (
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestHazardPointerSetPublish(t *testing.T) {
	h := newHazardPointerSet()

	var ptr unsafe.Pointer = unsafe.Pointer(new(int))
	h.Publish(ptr)

	for i := 0; i < MaxHazardPtrs; i++ {
		stored := h.ptrs[i].Load()
		if stored == nil || stored.(unsafe.Pointer) != ptr {
			t.Errorf("ptr[%d] should be %v", i, ptr)
		}
	}
}

func TestHazardPointerSetClear(t *testing.T) {
	h := newHazardPointerSet()

	var ptr unsafe.Pointer = unsafe.Pointer(new(int))
	h.Publish(ptr)
	h.Clear()

	for i := 0; i < MaxHazardPtrs; i++ {
		stored := h.ptrs[i].Load()
		if stored != nil && stored.(unsafe.Pointer) != nil {
			t.Errorf("ptr[%d] should be nil after Clear", i)
		}
	}
}

func TestHazardPointerSetScan(t *testing.T) {
	h := newHazardPointerSet()

	ptr1 := unsafe.Pointer(new(int))

	h.Publish(ptr1)

	scanned := h.Scan()
	if len(scanned) == 0 {
		t.Error("expected non-empty scan after publish")
	}

	h.Clear()
	scanned = h.Scan()
	if len(scanned) != 0 {
		t.Errorf("expected 0 pointers after clear, got %d", len(scanned))
	}
}

func TestHazardPointerSetConcurrency(t *testing.T) {
	h := newHazardPointerSet()

	var wg sync.WaitGroup
	iterations := 100

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				ptr := unsafe.Pointer(new(int))
				h.Publish(ptr)
				h.Scan()
				h.Clear()
			}
		}(i)
	}

	wg.Wait()
}

func TestEpochManagerRegisterThread(t *testing.T) {
	em := newEpochManager()
	em.Start()
	defer em.Stop()

	em.RegisterThread(1)

	time.Sleep(10 * time.Millisecond)

	found := false
	em.threads.Range(func(key, value any) bool {
		if key.(uint64) == 1 {
			found = true
		}
		return true
	})

	if !found {
		t.Error("thread 1 should be registered")
	}
}

func TestEpochManagerUnregisterThread(t *testing.T) {
	em := newEpochManager()

	em.RegisterThread(1)
	em.UnregisterThread(1)

	found := false
	em.threads.Range(func(key, value any) bool {
		if key.(uint64) == 1 {
			found = true
		}
		return true
	})

	if found {
		t.Error("thread 1 should be unregistered")
	}
}

func TestEpochManagerEnterEpoch(t *testing.T) {
	em := newEpochManager()
	em.Start()
	defer em.Stop()

	epoch := em.EnterEpoch()
	if epoch < 1 {
		t.Error("epoch should be >= 1")
	}
}

func TestEpochManagerExitEpoch(t *testing.T) {
	em := newEpochManager()
	em.Start()
	defer em.Stop()

	const testGoid uint64 = 12345
	em.RegisterThread(testGoid)
	em.ExitEpoch(testGoid)

	record, ok := em.threads.Load(testGoid)
	if !ok {
		t.Fatal("thread should be registered")
	}
	if record.(*threadRecord).enteredAt.Load() != 0 {
		t.Error("enteredAt should be 0 after ExitEpoch")
	}
}

func TestEpochManagerReclaim(t *testing.T) {
	em := newEpochManager()

	em.RegisterThread(1)

	batch := make([]unsafe.Pointer, 10)
	for i := range batch {
		batch[i] = unsafe.Pointer(new(int))
	}

	em.Reclaim(batch)
}

func TestEpochManagerStartStop(t *testing.T) {
	em := newEpochManager()

	em.Start()
	initialEpoch := em.epoch.Load()

	// Ticker interval is 100 ms; sleep just past one tick so at
	// least one increment is observable.
	time.Sleep(110 * time.Millisecond)

	em.Stop()

	if em.epoch.Load() <= initialEpoch {
		t.Error("epoch should have incremented after Start")
	}
}

func TestEpochManagerEpochIncrement(t *testing.T) {
	em := newEpochManager()
	em.Start()
	defer em.Stop()

	initialEpoch := em.epoch.Load()

	time.Sleep(110 * time.Millisecond)

	newEpoch := em.epoch.Load()
	if newEpoch <= initialEpoch {
		t.Error("epoch should have incremented after 100ms")
	}
}

func TestThreadRecord(t *testing.T) {
	record := &threadRecord{
		goroutineID: 123,
	}

	if record.goroutineID != 123 {
		t.Error("expected goroutineID 123")
	}

	record.enteredAt.Store(5)
	if record.enteredAt.Load() != 5 {
		t.Error("expected enteredAt 5")
	}
}

func BenchmarkHazardPublish(b *testing.B) {
	h := newHazardPointerSet()
	ptr := unsafe.Pointer(new(int))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Publish(ptr)
	}
}

func BenchmarkHazardClear(b *testing.B) {
	h := newHazardPointerSet()
	ptr := unsafe.Pointer(new(int))
	h.Publish(ptr)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Clear()
		h.Publish(ptr)
	}
}

func BenchmarkEpochEnter(b *testing.B) {
	em := newEpochManager()
	em.Start()
	defer em.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		em.EnterEpoch()
	}
}
