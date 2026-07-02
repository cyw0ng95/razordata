package ls

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/hex"
	"errors"
	"io"
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
	fs              FS // REQ001172: virtual filesystem
	inputs          []SSTFileMeta
	outputs         []SSTFileMeta
	overlap         []SSTFileMeta
	rateLimiter     *RateLimiter    // REQ000318: optional bytes/sec throttle
	placementPolicy PlacementPolicy // REQ000300: tier-aware output placement
	// tmpPath is the per-job temporary output path. Empty means
	// the legacy default (<outputDir>/compaction.tmp). Sub-jobs
	// created by SubCompactor set a unique tmpPath so parallel
	// sub-runs don't collide on the shared temp filename.
	// REQ001048.
	tmpPath string
}

func (cj *compactionJob) Run(manifest *manifest, dir string) error {
	if len(cj.inputs) == 0 {
		return ErrNoFilesToCompact
	}

	outputDir := cj.placementPolicy.DeviceDir(cj.level+1, dir)
	if err := cj.fs.MkdirAll(filepath.Join(outputDir, "sst"), 0755); err != nil {
		return err
	}

	// REQ001048: when SubCompactor runs multiple sub-jobs in
	// parallel, the hard-coded "compaction.tmp" path collides.
	// Sub-jobs set cj.tmpPath to a unique per-job path; the
	// zero-value path preserves the legacy serial-compaction path.
	tmpPath := cj.tmpPath
	if tmpPath == "" {
		tmpPath = filepath.Join(outputDir, "compaction.tmp")
	}
	outputPath := tmpPath
	tmpFile, err := cj.fs.Create(outputPath)
	if err != nil {
		return err
	}
	defer cj.fs.Remove(outputPath)
	defer tmpFile.Close()

	w := acquireSSTWriter()
	defer releaseSSTWriter(w)

	iters := make([]*sstIterator, 0, len(cj.inputs)+len(cj.overlap))
	for _, input := range cj.inputs {
		sstPath := filepath.Join(dir, fileName(&input))
		reader, err := openSSTLazyWithFS(cj.fs, sstPath)
		if err != nil {
			closeIterators(iters)
			return err
		}
		iters = append(iters, reader.Iterator())
	}

	for _, ov := range cj.overlap {
		sstPath := filepath.Join(dir, fileName(&ov))
		reader, err := openSSTLazyWithFS(cj.fs, sstPath)
		if err != nil {
			closeIterators(iters)
			return err
		}
		iters = append(iters, reader.Iterator())
	}

	h := &keyHeap{items: iters}
	heap.Init(h)

	rl := cj.rateLimiter

	for h.Len() > 0 {
		minItem := heap.Pop(h).(*sstIterator)
		k := minItem.Key()
		v := minItem.Value()
		if rl != nil {
			rl.Wait(int64(len(k) + len(v)))
		}
		w.Add(k, v)
		if minItem.Next() {
			heap.Push(h, minItem)
		}
	}

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
		if err := cj.fs.Rename(outputPath, newPath); err != nil {
			return err
		}
	} else {
		// Cross-device: copy instead of rename, then create symlink.
		if err := copyFileWithFS(cj.fs, outputPath, newPath); err != nil {
			return err
		}
		if err := cj.fs.Remove(outputPath); err != nil {
			slog.Warn("compaction: remove temp output", "path", outputPath, "err", err)
		}
		enginePath := filepath.Join(dir, newFileName)
		if err := cj.fs.Remove(enginePath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove old engine path", "path", enginePath, "err", err)
		}
		if err := cj.fs.Symlink(newPath, enginePath); err != nil {
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
		_ = cj.fs.Remove(newPath)
		return err
	}

	for _, input := range cj.inputs {
		sstPath := filepath.Join(dir, fileName(&input))
		if err := cj.fs.Remove(sstPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove input SST", "path", sstPath, "err", err)
		}
	}

	for _, ov := range cj.overlap {
		sstPath := filepath.Join(dir, fileName(&ov))
		if err := cj.fs.Remove(sstPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove overlap SST", "path", sstPath, "err", err)
		}
	}

	return nil
}

// partialResult holds the output of a single sub-compaction phase.
// REQ001157: two-phase compaction — each sub-job writes a partial
// SST to its own tmpPath, then the coordinator merges all partial
// outputs and applies a single manifest update.
type partialResult struct {
	tmpPath string
	inputs  []SSTFileMeta
	overlap []SSTFileMeta
	minKey  []byte
	maxKey  []byte
	size    int64
}

// RunPartial writes the merged SST to cj.tmpPath without touching
// the manifest or removing input files. REQ001157. Returns the
// partialResult describing the output.
func (cj *compactionJob) RunPartial(dir string) (*partialResult, error) {
	if len(cj.inputs) == 0 {
		return nil, ErrNoFilesToCompact
	}

	outputDir := cj.placementPolicy.DeviceDir(cj.level+1, dir)
	if err := cj.fs.MkdirAll(filepath.Join(outputDir, "sst"), 0755); err != nil {
		return nil, err
	}

	tmpPath := cj.tmpPath
	if tmpPath == "" {
		tmpPath = filepath.Join(outputDir, "compaction.tmp")
	}

	tmpFile, err := cj.fs.Create(tmpPath)
	if err != nil {
		return nil, err
	}

	w := acquireSSTWriter()
	defer releaseSSTWriter(w)

	iters := make([]*sstIterator, 0, len(cj.inputs)+len(cj.overlap))
	for _, input := range cj.inputs {
		sstPath := filepath.Join(dir, fileName(&input))
		reader, err := openSSTLazyWithFS(cj.fs, sstPath)
		if err != nil {
			closeIterators(iters)
			tmpFile.Close()
			cj.fs.Remove(tmpPath)
			return nil, err
		}
		iters = append(iters, reader.Iterator())
	}

	for _, ov := range cj.overlap {
		sstPath := filepath.Join(dir, fileName(&ov))
		reader, err := openSSTLazyWithFS(cj.fs, sstPath)
		if err != nil {
			closeIterators(iters)
			tmpFile.Close()
			cj.fs.Remove(tmpPath)
			return nil, err
		}
		iters = append(iters, reader.Iterator())
	}

	h := &keyHeap{items: iters}
	heap.Init(h)

	rl := cj.rateLimiter

	for h.Len() > 0 {
		minItem := heap.Pop(h).(*sstIterator)
		k := minItem.Key()
		v := minItem.Value()
		if rl != nil {
			rl.Wait(int64(len(k) + len(v)))
		}
		w.Add(k, v)
		if minItem.Next() {
			heap.Push(h, minItem)
		}
	}

	closeIterators(iters)

	sstData, err := w.Finish()
	if err != nil {
		tmpFile.Close()
		cj.fs.Remove(tmpPath)
		return nil, err
	}

	if rl != nil {
		rl.Wait(int64(len(sstData)))
	}

	if _, err := tmpFile.Write(sstData); err != nil {
		tmpFile.Close()
		cj.fs.Remove(tmpPath)
		return nil, err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		cj.fs.Remove(tmpPath)
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		cj.fs.Remove(tmpPath)
		return nil, err
	}

	return &partialResult{
		tmpPath: tmpPath,
		inputs:  cj.inputs,
		overlap: cj.overlap,
		minKey:  cj.inputs[0].MinKey,
		maxKey:  cj.inputs[len(cj.inputs)-1].MaxKey,
		size:    int64(len(sstData)),
	}, nil
}

func copyFileWithFS(fs FS, src, dst string) error {
	sf, err := fs.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := fs.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()

	_, err = io.Copy(df, sf)
	return err
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
	fs              FS // REQ001172: virtual filesystem
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
	// REQ001048: SubCompactor wires parallel sub-compaction into
	// the compaction loop. nil for tests that bypass compaction.
	subCompactor *SubCompactor
	debts        map[int]int64
	// REQ001175: per-level rate limiter with debt-aware throttling.
	perLevelRL atomic.Pointer[PerLevelRateLimiter]
}

// subCompactionThreshold is the input-file count at which the
// compaction loop dispatches through SubCompactor. Below the
// threshold the serial path is faster (no goroutine overhead).
// REQ001048, REQ001157.
//
// REQ001157: SubCompactor now uses a two-phase approach —
// Phase 1 runs compactionJob.RunPartial in parallel (each sub-job
// writes a partial SST to its own tmpPath, no manifest touch);
// Phase 2 merges all partial outputs and applies a single manifest
// update in serial. This eliminates the manifest data race that
// existed when parallel sub-jobs each called compactionJob.Run.
// The threshold is safe at 4 for production use.
const subCompactionThreshold = 4

func newCompactionManager(fs FS, dir string, manifest *manifest) *compactionManager {
	cm := &compactionManager{
		fs:              fs,
		manifest:        manifest,
		dir:             dir,
		budget:          defaultBudget,
		compactionQueue: make(chan *compactionJob, 10),
		done:            make(chan struct{}),
		loopDone:        make(chan struct{}),
		// REQ001048: SubCompactor shares the manifest with the
		// compaction manager; concurrency defaults to 4 (matching
		// the subcompaction threshold) and can be tuned via
		// SetSubCompactorConcurrency.
		subCompactor: NewSubCompactor(fs, dir, manifest, 4),
		debts:        make(map[int]int64),
	}
	cm.wg.Add(1)
	go cm.compactionLoop()
	return cm
}

// SetSubCompactorConcurrency overrides the default parallelism for
// sub-compaction. REQ001048. Tests can pin it to 1 to force the
// serial fallback.
func (cm *compactionManager) SetSubCompactorConcurrency(n int) {
	cm.compactionMu.Lock()
	defer cm.compactionMu.Unlock()
	cm.subCompactor = NewSubCompactor(cm.fs, cm.dir, cm.manifest, n)
}

// recalculateDebts recomputes per-level compaction debt.
// debt = max(0, totalSize - budget) per level. REQ001171.
func (cm *compactionManager) recalculateDebts() {
	v := cm.manifest.Current()
	for level, files := range v.levels {
		var totalSize int64
		for _, f := range files {
			totalSize += f.Size
		}
		budget := cm.budget.budgetFor(level)
		debt := totalSize - budget
		if debt < 0 {
			debt = 0
		}
		cm.debts[level] = debt
	}
}

func (cm *compactionManager) compactionLoop() {
	defer cm.wg.Done()
	defer close(cm.loopDone)
	for {
		select {
		case <-cm.done:
			return
		case job := <-cm.compactionQueue:
			cm.runJob(job)
			cm.recalculateDebts()
			if cm.manualDone != nil {
				close(cm.manualDone)
				cm.manualDone = nil
			}
			cm.compacting.Store(false)
		}
	}
}

// runJob executes a compactionJob, dispatching through SubCompactor
// when input file count exceeds subCompactionThreshold. Below the
// threshold the serial path runs directly. REQ001048.
func (cm *compactionManager) runJob(job *compactionJob) {
	if cm.subCompactor != nil && len(job.inputs) > subCompactionThreshold {
		_, err := cm.subCompactor.RunSubCompaction(
			context.Background(),
			job.level,
			job.inputs,
			SubCompactionOptions{
				Overlap:         job.overlap,
				RateLimiter:     job.rateLimiter,
				PlacementPolicy: job.placementPolicy,
			},
		)
		if err != nil {
			slog.Error("subcompaction failed", "level", job.level, "inputs", len(job.inputs), "overlap", len(job.overlap), "err", err)
		}
		return
	}
	if err := job.Run(cm.manifest, cm.dir); err != nil {
		slog.Error("compaction failed", "level", job.level, "inputs", len(job.inputs), "overlap", len(job.overlap), "err", err)
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

	cm.recalculateDebts()

	bestLevel := -1
	bestUrgency := 0.0

	style := CompactionStyle(cm.style.Load())
	v := cm.manifest.Current()
	for level := range len(v.levels) - 1 {
		files := v.levels[level]
		if len(files) == 0 {
			continue
		}

		totalSize := int64(0)
		for _, f := range files {
			totalSize += f.Size
		}

		if !style.shouldCompact(level, len(files), totalSize) {
			continue
		}

		budget := cm.budget.budgetFor(level)
		if budget <= 0 {
			budget = 1
		}
		urgency := float64(cm.debts[level]) / float64(budget)
		urgency *= (1.0 + 0.5*float64(level))

		if urgency > bestUrgency {
			bestUrgency = urgency
			bestLevel = level
		}
	}

	if bestLevel >= 0 {
		cm.requestCompaction(bestLevel)
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
		fs:              cm.fs,
		inputs:          inputs,
		outputs:         nil,
		overlap:         overlap,
		rateLimiter:     cm.rateLimiter.Load(),
		placementPolicy: cm.placementPolicy,
	}

	// REQ001175: prefer per-level rate limiter when available.
	plrl := cm.perLevelRL.Load()
	if plrl != nil {
		plrl.mu.RLock()
		lr := plrl.levels[level]
		plrl.mu.RUnlock()
		if lr != nil {
			job.rateLimiter = lr
		}
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

// MergePartials merges all partial SST outputs into a single SST file
// and applies a single manifest update. REQ001157: this is the
// coordinator phase of two-phase compaction.
func (cm *compactionManager) MergePartials(partials []*partialResult, manifest *manifest, dir string) error {
	if len(partials) == 0 {
		return ErrNoFilesToCompact
	}

	// Collect all inputs and overlaps for manifest update
	allInputs := make([]SSTFileMeta, 0, len(partials))
	allOverlap := make([]SSTFileMeta, 0, len(partials))
	for _, p := range partials {
		allInputs = append(allInputs, p.inputs...)
		allOverlap = append(allOverlap, p.overlap...)
	}

	// Determine output directory from the first partial
	outputDir := partials[0].inputs[0].Level + 1
	outputDirPath := cm.placementPolicy.DeviceDir(outputDir, dir)
	if err := cm.fs.MkdirAll(filepath.Join(outputDirPath, "sst"), 0755); err != nil {
		return err
	}

	// Merge all partial SSTs into a single SST
	tmpFile, err := cm.fs.Create(filepath.Join(outputDirPath, "merge-*.tmp"))
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(outputDirPath, "merge-*.tmp")
	defer cm.fs.Remove(tmpPath)
	defer tmpFile.Close()

	w := acquireSSTWriter()
	defer releaseSSTWriter(w)

	iters := make([]*sstIterator, 0, len(partials))
	for _, p := range partials {
		reader, err := openSSTLazyWithFS(cm.fs, p.tmpPath)
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
		k := minItem.Key()
		v := minItem.Value()
		w.Add(k, v)
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

	// Determine min/max key from all partials
	globalMin := partials[0].minKey
	globalMax := partials[0].maxKey
	for _, p := range partials[1:] {
		if bytes.Compare(p.minKey, globalMin) < 0 {
			globalMin = p.minKey
		}
		if bytes.Compare(p.maxKey, globalMax) > 0 {
			globalMax = p.maxKey
		}
	}

	newFileID := nextFileID()
	newFileName := fileName(&SSTFileMeta{
		FileID:    newFileID,
		Level:     outputDir,
		MinKey:    globalMin,
		MaxKey:    globalMax,
		Size:      int64(len(sstData)),
		BloomBits: 10,
	})
	newPath := filepath.Join(outputDirPath, newFileName)
	if outputDirPath == dir {
		if err := cm.fs.Rename(tmpPath, newPath); err != nil {
			return err
		}
	} else {
		if err := copyFileWithFS(cm.fs, tmpPath, newPath); err != nil {
			return err
		}
		if err := cm.fs.Remove(tmpPath); err != nil {
			slog.Warn("compaction: remove temp output", "path", tmpPath, "err", err)
		}
		enginePath := filepath.Join(dir, newFileName)
		if err := cm.fs.Remove(enginePath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove old engine path", "path", enginePath, "err", err)
		}
		if err := cm.fs.Symlink(newPath, enginePath); err != nil {
			return err
		}
	}

	// Apply manifest update
	newLevels := make([][]SSTFileMeta, len(manifest.Current().levels))
	copy(newLevels, manifest.Current().levels)

	newLevels[partials[0].inputs[0].Level] = removeFiles(newLevels[partials[0].inputs[0].Level], allInputs)
	newLevels[outputDir] = removeFiles(newLevels[outputDir], allOverlap)
	newLevels[outputDir] = append(newLevels[outputDir], SSTFileMeta{
		FileID:    newFileID,
		Level:     outputDir,
		MinKey:    globalMin,
		MaxKey:    globalMax,
		Size:      int64(len(sstData)),
		BloomBits: 10,
	})

	v := Version{
		num:     manifest.Current().num + 1,
		levels:  newLevels,
		created: time.Now(),
	}

	if err := manifest.Apply(v); err != nil {
		_ = cm.fs.Remove(newPath)
		return err
	}

	// Remove input files
	for _, input := range allInputs {
		sstPath := filepath.Join(dir, fileName(&input))
		if err := cm.fs.Remove(sstPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove input SST", "path", sstPath, "err", err)
		}
	}

	for _, ov := range allOverlap {
		sstPath := filepath.Join(dir, fileName(&ov))
		if err := cm.fs.Remove(sstPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove overlap SST", "path", sstPath, "err", err)
		}
	}

	// Remove partial temp files
	for _, p := range partials {
		if err := cm.fs.Remove(p.tmpPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove partial tmp", "path", p.tmpPath, "err", err)
		}
	}

	return nil
}
