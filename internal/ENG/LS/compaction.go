package ls

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrCompactionInProgress = errors.New("ls: compaction already in progress")
	ErrNoFilesToCompact     = errors.New("ls: no files to compact")
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
	level           int
	inputs          []SSTFileMeta
	outputs         []SSTFileMeta
	overlap         []SSTFileMeta
	rateLimiter     *RateLimiter    // REQ000318: optional bytes/sec throttle
	placementPolicy PlacementPolicy // REQ000300: tier-aware output placement
}

func (cj *compactionJob) Run(manifest *manifest, dir string) error {
	if len(cj.inputs) == 0 {
		return ErrNoFilesToCompact
	}

	outputDir := cj.placementPolicy.DeviceDir(cj.level+1, dir)
	if err := os.MkdirAll(filepath.Join(outputDir, "sst"), 0755); err != nil {
		return err
	}

	outputPath := filepath.Join(outputDir, "compaction.tmp")
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

	rl := cj.rateLimiter

	var lastKey, lastVal []byte
	for h.Len() > 0 {
		minItem := heap.Pop(h).(*sstIterator)
		k := minItem.Key()
		v := minItem.Value()
		if rl != nil {
			rl.Wait(int64(len(k) + len(v)))
		}
		w.Add(k, v)
		lastKey = k
		lastVal = v
		if minItem.Next() {
			heap.Push(h, minItem)
		}
	}
	_ = lastKey
	_ = lastVal

	closeIterators(iters)

	sstData, err := w.Finish()
	if err != nil {
		return err
	}

	if rl != nil {
		rl.Wait(int64(len(sstData)))
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
	newPath := filepath.Join(outputDir, newFileName)
	if outputDir == dir {
		if err := os.Rename(outputPath, newPath); err != nil {
			return err
		}
	} else {
		// Cross-device: copy instead of rename, then create symlink.
		if err := copyFile(outputPath, newPath); err != nil {
			return err
		}
		os.Remove(outputPath)
		enginePath := filepath.Join(dir, newFileName)
		os.Remove(enginePath)
		if err := os.Symlink(newPath, enginePath); err != nil {
			return err
		}
	}

	newLevels := make([][]SSTFileMeta, len(manifest.Current().levels))
	copy(newLevels, manifest.Current().levels)

	newLevels[cj.level] = removeFiles(newLevels[cj.level], cj.inputs)
	newLevels[cj.level+1] = removeFiles(newLevels[cj.level+1], cj.overlap)
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

	if err := manifest.Apply(v); err != nil {
		_ = os.Remove(newPath)
		return err
	}

	for _, input := range cj.inputs {
		sstPath := filepath.Join(dir, fileName(&input))
		os.Remove(sstPath)
	}

	for _, ov := range cj.overlap {
		sstPath := filepath.Join(dir, fileName(&ov))
		os.Remove(sstPath)
	}

	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func fileName(meta *SSTFileMeta) string {
	return filepath.Join("sst",
		"L"+strconv.Itoa(int(meta.Level))+"_"+hex.EncodeToString(meta.MinKey)+"_"+hex.EncodeToString(meta.MaxKey)+"_"+u64toa(meta.FileID)+".sst")
}

// keyRangeOverlap reports whether [lo, hi] overlaps [flo, fhi] (REQ000601).
func keyRangeOverlap(lo, hi, flo, fhi []byte) bool {
	if len(hi) > 0 && len(flo) > 0 && bytes.Compare(hi, flo) < 0 {
		return false
	}
	if len(lo) > 0 && len(fhi) > 0 && bytes.Compare(lo, fhi) > 0 {
		return false
	}
	return true
}

func u64toa(n uint64) string {
	if n == 0 {
		return "0"
	}
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
	rateLimiter     atomic.Pointer[RateLimiter] // REQ000318: bytes/sec throttle
	style           atomic.Int32                // REQ000320: compaction strategy
	placementPolicy PlacementPolicy             // REQ000300: tier-aware output placement
	manualDone      chan struct{}               // REQ000634: ManualCompact completion signal
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
				slog.Error("compaction failed", "level", job.level, "inputs", len(job.inputs), "overlap", len(job.overlap), "err", err)
			}
			if cm.manualDone != nil {
				close(cm.manualDone)
				cm.manualDone = nil
			}
			cm.compacting.Store(false)
		}
	}
}

// Stop signals the compaction goroutine to exit and waits for it.
func (cm *compactionManager) Stop(ctx context.Context) error {
	cm.stopOnce.Do(func() {
		close(cm.done)
	})
	// If context is already cancelled, return immediately.
	if err := ctx.Err(); err != nil {
		return err
	}
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

	style := CompactionStyle(cm.style.Load())
	v := cm.manifest.Current()
	for level := 0; level < len(v.levels)-1; level++ {
		files := v.levels[level]
		totalSize := int64(0)
		for _, f := range files {
			totalSize += f.Size
		}

		if style.shouldCompact(level, len(files), totalSize) {
			cm.requestCompaction(level)
			return
		}
	}
}

// ManualCompact forces a compaction across all levels (REQ000257, REQ000634).
func (cm *compactionManager) ManualCompact() error {
	if cm.compacting.Load() {
		return ErrCompactionInProgress
	}

	v := cm.manifest.Current()

	for level := len(v.levels) - 2; level >= 0; level-- {
		files := v.levels[level]
		if len(files) == 0 {
			continue
		}

		done := make(chan struct{}, 1)
		if cm.requestCompaction(level) {
			cm.manualDone = done
		} else {
			close(done)
		}
		<-done
	}

	return nil
}

func (cm *compactionManager) requestCompaction(level int) bool {
	cm.compactionMu.Lock()
	defer cm.compactionMu.Unlock()

	if cm.compacting.Load() {
		return false
	}

	v := cm.manifest.Current()
	if level >= len(v.levels) {
		return false
	}

	inputs := v.levels[level]
	if len(inputs) == 0 {
		return false
	}

	var overlap []SSTFileMeta
	if level+1 < len(v.levels) {
		lo := inputs[0].MinKey
		hi := inputs[len(inputs)-1].MaxKey
		for _, f := range v.levels[level+1] {
			if keyRangeOverlap(lo, hi, f.MinKey, f.MaxKey) {
				overlap = append(overlap, f)
			}
		}
	}

	job := &compactionJob{
		level:           level,
		inputs:          inputs,
		outputs:         nil,
		overlap:         overlap,
		rateLimiter:     cm.rateLimiter.Load(),
		placementPolicy: cm.placementPolicy,
	}

	cm.compacting.Store(true)
	select {
	case cm.compactionQueue <- job:
		return true
	default:
		cm.compacting.Store(false)
		return false
	}
}

func (cm *compactionManager) Close() error {
	return cm.Stop(context.Background())
}
