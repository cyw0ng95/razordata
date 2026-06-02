package VL

import (
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
	wg       sync.WaitGroup
}

type epochManager struct {
	epoch   atomic.Int64
	threads sync.Map
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func newEpochManager() *epochManager {
	return &epochManager{}
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

func (em *epochManager) Stop() {
	close(em.stopCh)
	em.wg.Wait()
}

func (em *epochManager) CurrentEpoch() int64 {
	return em.epoch.Load()
}

func (em *epochManager) AdvanceEpoch() {
	em.epoch.Add(1)
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
		globalGC.em.Stop()
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
	enteredAt    atomic.Int64
}

var gcThreadRecords sync.Map

func RegisterGCThread(goroutineID uint64) {
	record := &gcThreadRecord{
		goroutineID: goroutineID,
		enteredAt:    atomic.Int64{},
	}
	record.enteredAt.Store(globalGC.em.CurrentEpoch())
	gcThreadRecords.Store(goroutineID, record)
}

func UnregisterGCThread(goroutineID uint64) {
	gcThreadRecords.Delete(goroutineID)
}
