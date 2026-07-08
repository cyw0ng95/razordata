package LC

import (
	"sync"
	"testing"
	"time"
	"unsafe"
)

// TestHazardPointerSetPublishCurrent pins REQ000158 (R158-1):
// PublishCurrent writes to slot 0 only. Before the fix, a
// single 'Publish(ptr)' wrote to BOTH slots, defeating the
// double-slot design.
func TestHazardPointerSetPublishCurrent(t *testing.T) {
	h := newHazardPointerSet()

	var ptr unsafe.Pointer = unsafe.Pointer(new(int))
	h.PublishCurrent(ptr)

	if stored := h.ptrs[0].Load(); stored == nil || stored.(unsafe.Pointer) != ptr {
		t.Errorf("ptr[0] should be %v, got %v", ptr, stored)
	}
	// Slot 1 must remain untouched.
	if stored := h.ptrs[1].Load(); stored != nil {
		t.Errorf("ptr[1] should be nil after PublishCurrent, got %v", stored)
	}
}

// TestHazardPointerSetPublishNext pins REQ000158 (R158-2):
// PublishNext writes to slot 1 only. Together with
// PublishCurrent, this gives the reader a 'current' and a
// 'prefetch' pointer simultaneously.
func TestHazardPointerSetPublishNext(t *testing.T) {
	h := newHazardPointerSet()

	cur := unsafe.Pointer(new(int))
	nxt := unsafe.Pointer(new(int))

	h.PublishCurrent(cur)
	h.PublishNext(nxt)

	if stored := h.ptrs[0].Load(); stored == nil || stored.(unsafe.Pointer) != cur {
		t.Errorf("ptr[0] should be %v, got %v", cur, stored)
	}
	if stored := h.ptrs[1].Load(); stored == nil || stored.(unsafe.Pointer) != nxt {
		t.Errorf("ptr[1] should be %v, got %v", nxt, stored)
	}
}

// TestHazardPointerSetPublishSequentialOrder: when the same
// pointer is published to both slots, the second publish must
// not be clobbered by the first (the pre-fix bug had a tight
// loop that wrote to all slots in unspecified order).
func TestHazardPointerSetPublishSequentialOrder(t *testing.T) {
	h := newHazardPointerSet()

	cur := unsafe.Pointer(new(int))
	nxt := unsafe.Pointer(new(int))

	h.PublishCurrent(cur)
	h.PublishNext(nxt) // different pointer on purpose

	if stored := h.ptrs[0].Load(); stored == nil || stored.(unsafe.Pointer) != cur {
		t.Errorf("after PublishCurrent/PublishNext, ptr[0] should be %v, got %v", cur, stored)
	}
	if stored := h.ptrs[1].Load(); stored == nil || stored.(unsafe.Pointer) != nxt {
		t.Errorf("after PublishCurrent/PublishNext, ptr[1] should be %v, got %v", nxt, stored)
	}
}

func TestHazardPointerSetClear(t *testing.T) {
	h := newHazardPointerSet()

	cur := unsafe.Pointer(new(int))
	nxt := unsafe.Pointer(new(int))
	h.PublishCurrent(cur)
	h.PublishNext(nxt)
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

	h.PublishCurrent(ptr1)

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
				cur := unsafe.Pointer(new(int))
				nxt := unsafe.Pointer(new(int))
				h.PublishCurrent(cur)
				h.PublishNext(nxt)
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
	for i := range em.threads {
		if em.threads[i].inUse.Load() && em.threads[i].goroutineID == 1 {
			found = true
			break
		}
	}

	if !found {
		t.Error("thread 1 should be registered")
	}
}

func TestEpochManagerUnregisterThread(t *testing.T) {
	em := newEpochManager()

	em.RegisterThread(1)
	em.UnregisterThread(1)

	found := false
	for i := range em.threads {
		if em.threads[i].inUse.Load() && em.threads[i].goroutineID == 1 {
			found = true
			break
		}
	}

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

	record, ok := func() (*threadRecord, bool) {
	for i := range em.threads {
		if em.threads[i].inUse.Load() && em.threads[i].goroutineID == testGoid {
			return &em.threads[i], true
		}
	}
	return nil, false
}()
	if !ok {
		t.Fatal("thread should be registered")
	}
	if record.enteredAt.Load() != 0 {
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
	cur := unsafe.Pointer(new(int))
	nxt := unsafe.Pointer(new(int))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.PublishCurrent(cur)
		h.PublishNext(nxt)
	}
}

func BenchmarkHazardClear(b *testing.B) {
	h := newHazardPointerSet()
	cur := unsafe.Pointer(new(int))
	nxt := unsafe.Pointer(new(int))
	h.PublishCurrent(cur)
	h.PublishNext(nxt)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Clear()
		h.PublishCurrent(cur)
		h.PublishNext(nxt)
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
