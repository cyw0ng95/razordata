package ls

import (
	"bytes"
	"container/heap"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrCompactionInProgress = errors.New("compaction already in progress")
	ErrNoFilesToCompact     = errors.New("no files to compact")
)

type levelBudget struct {
	l0 int64
	l1 int64
	l2 int64
}

var defaultBudget = levelBudget{
	l0: 4 * 1024 * 1024,
	l1: 32 * 1024 * 1024,
	l2: 320 * 1024 * 1024,
}

func (lb levelBudget) budgetFor(level int) int64 {
	switch level {
	case 0:
		return lb.l0
	case 1:
		return lb.l1
	case 2:
		return lb.l2
	default:
		return lb.l2 * int64(level-1)
	}
}

type compactionJob struct {
	level   int
	inputs  []SSTFileMeta
	outputs []SSTFileMeta
	overlap []SSTFileMeta
}

func (cj *compactionJob) Run(manifest *manifest, dir string) error {
	if len(cj.inputs) == 0 {
		return ErrNoFilesToCompact
	}

	outputPath := filepath.Join(dir, "compaction.tmp")
	tmpFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer os.Remove(outputPath)
	defer tmpFile.Close()

	w := newSSTWriter()

	iters := make([]*sstIterator, 0, len(cj.inputs)+len(cj.overlap))
	for _, input := range cj.inputs {
		sstPath := filepath.Join(dir, fileName(&input))
		data, err := os.ReadFile(sstPath)
		if err != nil {
			closeIterators(iters)
			return err
		}
		reader, err := openSST(data)
		if err != nil {
			closeIterators(iters)
			return err
		}
		iters = append(iters, reader.Iterator())
	}

	for _, ov := range cj.overlap {
		sstPath := filepath.Join(dir, fileName(&ov))
		data, err := os.ReadFile(sstPath)
		if err != nil {
			closeIterators(iters)
			return err
		}
		reader, err := openSST(data)
		if err != nil {
			closeIterators(iters)
			return err
		}
		iters = append(iters, reader.Iterator())
	}

	h := &keyHeap{items: iters}
	heap.Init(h)

	for h.Len() > 0 {
		minItem := heap.Pop(h).(*sstIterator)
		w.Add(minItem.Key(), minItem.Value())
		if minItem.Next() {
			heap.Push(h, minItem)
		}
	}

	closeIterators(iters)

	sstData, err := w.Finish()
	if err != nil {
		return err
	}

	if _, err := tmpFile.Write(sstData); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	newFileID := nextFileID()
	newFileName := fileName(&SSTFileMeta{
		FileID:    newFileID,
		Level:     cj.level + 1,
		MinKey:    cj.inputs[0].MinKey,
		MaxKey:    cj.inputs[len(cj.inputs)-1].MaxKey,
		Size:      int64(len(sstData)),
		BloomBits: 10,
	})
	newPath := filepath.Join(dir, newFileName)
	if err := os.Rename(outputPath, newPath); err != nil {
		return err
	}

	newLevels := make([][]SSTFileMeta, len(manifest.Current().levels))
	copy(newLevels, manifest.Current().levels)

	for _, input := range cj.inputs {
		sstPath := filepath.Join(dir, fileName(&input))
		os.Remove(sstPath)
	}

	newLevels[cj.level] = removeFiles(newLevels[cj.level], cj.inputs)
	newLevels[cj.level+1] = append(newLevels[cj.level+1], SSTFileMeta{
		FileID:    newFileID,
		Level:     cj.level + 1,
		MinKey:    cj.inputs[0].MinKey,
		MaxKey:    cj.inputs[len(cj.inputs)-1].MaxKey,
		Size:      int64(len(sstData)),
		BloomBits: 10,
	})

	v := Version{
		num:     manifest.Current().num + 1,
		levels:  newLevels,
		created: time.Now(),
	}

	return manifest.Apply(v)
}

func fileName(meta *SSTFileMeta) string {
	return filepath.Join("sst", "L"+string(rune('0'+meta.Level))+"_"+string(meta.MinKey)+"_"+string(meta.MaxKey)+"_"+u64toa(meta.FileID)+".sst")
}

func u64toa(n uint64) string {
	var buf [20]byte
	i := 20
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

type keyHeap struct {
	items []*sstIterator
}

func (h *keyHeap) Len() int           { return len(h.items) }
func (h *keyHeap) Less(i, j int) bool { return bytes.Compare(h.items[i].Key(), h.items[j].Key()) < 0 }
func (h *keyHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *keyHeap) Push(x any)         { h.items = append(h.items, x.(*sstIterator)) }
func (h *keyHeap) Pop() any {
	item := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	return item
}

func closeIterators(iters []*sstIterator) {
	for _, it := range iters {
		it.Close()
	}
}

func removeFiles(files []SSTFileMeta, toRemove []SSTFileMeta) []SSTFileMeta {
	result := make([]SSTFileMeta, 0, len(files))
	for _, f := range files {
		found := false
		for _, r := range toRemove {
			if f.FileID == r.FileID {
				found = true
				break
			}
		}
		if !found {
			result = append(result, f)
		}
	}
	return result
}

type compactionManager struct {
	manifest        *manifest
	dir             string
	budget          levelBudget
	compactionMu    sync.Mutex
	compacting      atomic.Bool
	compactionQueue chan *compactionJob
	done            chan struct{}
	loopDone        chan struct{}
	wg              sync.WaitGroup
	stopOnce        sync.Once
}

func newCompactionManager(dir string, manifest *manifest) *compactionManager {
	cm := &compactionManager{
		manifest:        manifest,
		dir:             dir,
		budget:          defaultBudget,
		compactionQueue: make(chan *compactionJob, 10),
		done:            make(chan struct{}),
		loopDone:        make(chan struct{}),
	}
	cm.wg.Add(1)
	go cm.compactionLoop()
	return cm
}

func (cm *compactionManager) compactionLoop() {
	defer cm.wg.Done()
	defer close(cm.loopDone)
	for {
		select {
		case <-cm.done:
			return
		case job := <-cm.compactionQueue:
			if err := job.Run(cm.manifest, cm.dir); err != nil {
			}
			cm.compacting.Store(false)
		}
	}
}

// Stop signals the compaction goroutine to exit and waits for it,
// bounded by ctx. Idempotent: a second call with the same ctx
// returns nil immediately if the loop has already exited.
//
// Stop is distinct from Close: Stop is the graceful-shutdown entry
// point (Phase 4.1 of SYS.md:245-251) and respects a timeout; Close
// is the destructor and uses an infinite wait.
func (cm *compactionManager) Stop(ctx context.Context) error {
	cm.stopOnce.Do(func() {
		close(cm.done)
	})
	select {
	case <-cm.loopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (cm *compactionManager) MaybeCompact() {
	if cm.compacting.Load() {
		return
	}

	v := cm.manifest.Current()
	for level := 0; level < len(v.levels)-1; level++ {
		totalSize := int64(0)
		for _, f := range v.levels[level] {
			totalSize += f.Size
		}

		if totalSize > cm.budget.budgetFor(level) {
			cm.requestCompaction(level)
			return
		}
	}
}

func (cm *compactionManager) requestCompaction(level int) {
	cm.compactionMu.Lock()
	defer cm.compactionMu.Unlock()

	if cm.compacting.Load() {
		return
	}

	v := cm.manifest.Current()
	if level >= len(v.levels) {
		return
	}

	inputs := v.levels[level]
	if len(inputs) == 0 {
		return
	}

	var overlap []SSTFileMeta
	if level+1 < len(v.levels) {
		overlap = v.levels[level+1]
	}

	job := &compactionJob{
		level:   level,
		inputs:  inputs,
		outputs: nil,
		overlap: overlap,
	}

	cm.compacting.Store(true)
	select {
	case cm.compactionQueue <- job:
	default:
		cm.compacting.Store(false)
	}
}

func (cm *compactionManager) Close() error {
	return cm.Stop(context.Background())
}
