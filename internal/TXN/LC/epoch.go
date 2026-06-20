package LC

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

var goroutineID atomic.Uint64

func getGoroutineID() uint64 {
	if id := GoID(); id != 0 {
		return id
	}
	return goroutineID.Add(1)
}

type threadRecord struct {
	goroutineID uint64
	enteredAt   atomic.Int64
}

type epochManager struct {
	epoch   atomic.Int64
	threads sync.Map
	drainCh chan struct{}
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func newEpochManager() *epochManager {
	em := &epochManager{
		drainCh: make(chan struct{}),
		stopCh:  make(chan struct{}),
	}
	em.epoch.Store(1)
	return em
}

func (em *epochManager) RegisterThread(goroutineID uint64) {
	record := &threadRecord{
		goroutineID: goroutineID,
	}
	record.enteredAt.Store(em.epoch.Load())
	em.threads.Store(goroutineID, record)
}

func (em *epochManager) UnregisterThread(goroutineID uint64) {
	em.threads.Delete(goroutineID)
}

func (em *epochManager) EnterEpoch() uint64 {
	goid := getGoroutineID()
	em.RegisterThread(goid)
	epoch := em.epoch.Load()
	record, ok := em.threads.Load(goid)
	if ok {
		record.(*threadRecord).enteredAt.Store(epoch)
	}
	return uint64(epoch)
}

func (em *epochManager) ExitEpoch(goroutineID uint64) {
	if record, ok := em.threads.Load(goroutineID); ok {
		record.(*threadRecord).enteredAt.Store(0)
	}
}

func (em *epochManager) Reclaim(batch []unsafe.Pointer) {
	_ = em.epoch.Load()

	// Drain pending old-generation buffers from the arena.
	// This integrates with TXN/MV/arena.go's generation reclamation
	// (REQ000589).
	MV.ReclaimOldGenerations()

	// Nullify batch pointers to prevent use-after-free.
	for i := range batch {
		batch[i] = nil
	}
}

func (em *epochManager) Start() {
	em.wg.Add(1)
	go func() {
		defer em.wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				em.epoch.Add(1)
				select {
				case em.drainCh <- struct{}{}:
				default:
				}
				MV.ReclaimOldGenerations()
			case <-em.stopCh:
				return
			}
		}
	}()
}

func (em *epochManager) Stop() {
	close(em.stopCh)
	em.wg.Wait()
}

// WaitForDrain blocks until all active readers reach quiescent state.
func (em *epochManager) WaitForDrain(maxSpins int) bool {
	prev := em.epoch.Load()
	for range maxSpins {
		if em.allStale(prev) {
			return true
		}
		select {
		case <-em.drainCh:
		case <-em.stopCh:
			return em.allStale(prev)
		default:
			runtime_GoschedFn()
		}
	}
	return em.allStale(prev)
}

func (em *epochManager) allStale(threshold int64) bool {
	stale := true
	em.threads.Range(func(_, value any) bool {
		record := value.(*threadRecord)
		enteredAt := record.enteredAt.Load()
		if enteredAt != 0 && enteredAt >= threshold {
			stale = false
			return false
		}
		return true
	})
	return stale
}

func runtime_Gosched() { runtime_GoschedFn() }

var runtime_GoschedFn = runtime.Gosched
