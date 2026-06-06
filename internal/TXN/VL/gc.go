package VL

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var globalEpochManager = newEpochManager()

func init() {
	globalEpochManager.Start()
}

type versionGC struct {
	em       *epochManager
	reclaimQ chan []unsafe.Pointer
	stopCh   chan struct{}
}

type epochManager struct {
	epoch    atomic.Int64
	threads  sync.Map
	stopCh   chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

func newEpochManager() *epochManager {
	return &epochManager{
		stopCh: make(chan struct{}),
	}
}

func (em *epochManager) Start() {
	em.stopCh = make(chan struct{})
	em.wg.Add(1)
	go func() {
		defer em.wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				em.epoch.Add(1)
			case <-em.stopCh:
				return
			}
		}
	}()
}

// Stop signals the epoch manager's background goroutine to exit and
// waits for it, bounded by ctx. Idempotent.
//
// Stop is the graceful-shutdown entry point (Phase 4.2 of
// SYS.md:253-256). The wg.Wait runs in a side goroutine so that
// ctx.Done can preempt it; the side goroutine exits on its own once
// the wait completes.
func (em *epochManager) Stop(ctx context.Context) error {
	em.stopOnce.Do(func() {
		close(em.stopCh)
	})
	finished := make(chan struct{})
	go func() {
		em.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (em *epochManager) CurrentEpoch() int64 {
	return em.epoch.Load()
}

func (em *epochManager) AdvanceEpoch() {
	em.epoch.Add(1)
}

func CurrentEpoch() int64 {
	if globalGC.em == nil {
		return 0
	}
	return globalGC.em.CurrentEpoch()
}

func AdvanceEpoch() {
	if globalGC.em != nil {
		globalGC.em.AdvanceEpoch()
	}
}

var globalGC = &versionGC{
	reclaimQ: make(chan []unsafe.Pointer, 1024),
	stopCh:   make(chan struct{}),
}

func StartGC() {
	globalGC.em = newEpochManager()
	globalGC.em.Start()
}

func StopGC() {
	if globalGC.em != nil {
		_ = globalGC.em.Stop(context.Background())
	}
}

func ReclaimVersionNodes(batch []unsafe.Pointer) {
	if len(batch) == 0 {
		return
	}
	currentEpoch := globalGC.em.CurrentEpoch()
	oldestActiveEpoch := currentEpoch - 1

	globalGC.em.threads.Range(func(key, value any) bool {
		record := value.(*gcThreadRecord)
		enteredAt := record.enteredAt.Load()
		if enteredAt != 0 && enteredAt >= oldestActiveEpoch {
			return false
		}
		return true
	})

	for _, ptr := range batch {
		if ptr != nil {
		}
	}
}

type gcThreadRecord struct {
	goroutineID uint64
	enteredAt   atomic.Int64
}

var gcThreadRecords sync.Map

func RegisterGCThread(goroutineID uint64) {
	record := &gcThreadRecord{
		goroutineID: goroutineID,
		enteredAt:   atomic.Int64{},
	}
	record.enteredAt.Store(globalGC.em.CurrentEpoch())
	gcThreadRecords.Store(goroutineID, record)
}

func UnregisterGCThread(goroutineID uint64) {
	gcThreadRecords.Delete(goroutineID)
}
