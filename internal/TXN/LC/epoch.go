package LC

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

const MaxThreads = 256

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
	inUse       atomic.Bool
}

type epochManager struct {
	epoch   atomic.Int64
	threads [MaxThreads]threadRecord
	active  atomic.Int64
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
	for i := range em.threads {
		rec := &em.threads[i]
		if !rec.inUse.Load() {
			if rec.inUse.CompareAndSwap(false, true) {
				rec.goroutineID = goroutineID
				rec.enteredAt.Store(em.epoch.Load())
				em.active.Add(1)
				return
			}
		}
	}
}

func (em *epochManager) UnregisterThread(goroutineID uint64) {
	for i := range em.threads {
		rec := &em.threads[i]
		if rec.inUse.Load() && rec.goroutineID == goroutineID {
			rec.goroutineID = 0
			rec.enteredAt.Store(0)
			rec.inUse.Store(false)
			em.active.Add(-1)
			return
		}
	}
}

func (em *epochManager) EnterEpoch() uint64 {
	goid := getGoroutineID()
	// Find existing record or register new one.
	found := false
	for i := range em.threads {
		rec := &em.threads[i]
		if rec.inUse.Load() && rec.goroutineID == goid {
			found = true
			epoch := em.epoch.Load()
			EC.BUG_ON(epoch < 0, "epoch.EnterEpoch: negative epoch %d", epoch)
			EC.BUG_ON(rec.enteredAt.Load() > epoch, "epoch.EnterEpoch: epoch violation — rec.enteredAt %d > currentEpoch %d", rec.enteredAt.Load(), epoch)
			rec.enteredAt.Store(epoch)
			return uint64(epoch)
		}
	}
	if !found {
		em.RegisterThread(goid)
	}
	epoch := em.epoch.Load()
	EC.BUG_ON(epoch < 0, "epoch.EnterEpoch: negative epoch %d", epoch)
	// For newly registered, set enteredAt after registration.
	for i := range em.threads {
		rec := &em.threads[i]
		if rec.inUse.Load() && rec.goroutineID == goid {
			rec.enteredAt.Store(epoch)
			break
		}
	}
	return uint64(epoch)
}

func (em *epochManager) ExitEpoch(goroutineID uint64) {
	for i := range em.threads {
		rec := &em.threads[i]
		if rec.inUse.Load() && rec.goroutineID == goroutineID {
			currentEpoch := em.epoch.Load()
			EC.WARN_ON(currentEpoch-rec.enteredAt.Load() > 1, "epoch.ExitEpoch: lagging thread goid=%d entered=%d current=%d", goroutineID, rec.enteredAt.Load(), currentEpoch)
			rec.enteredAt.Store(0)
			return
		}
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
	// A reader is stale if its enteredAt is 0 (has exited or never entered).
	// active.Load() == 0 is a fast-path shortcut: no registered threads
	// means no readers can be active.
	if em.active.Load() == 0 {
		return true
	}
	for i := range em.threads {
		rec := &em.threads[i]
		if rec.inUse.Load() && rec.enteredAt.Load() != 0 {
			return false
		}
	}
	return true
}

func (em *epochManager) allAged(threshold int64) bool {
	for i := range em.threads {
		rec := &em.threads[i]
		if rec.inUse.Load() {
			enteredAt := rec.enteredAt.Load()
			if enteredAt != 0 && enteredAt >= threshold {
				return false
			}
		}
	}
	return true
}

func runtime_Gosched() { runtime_GoschedFn() }

var runtime_GoschedFn = runtime.Gosched
