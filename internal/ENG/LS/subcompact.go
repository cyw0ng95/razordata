package ls

import (
	"bytes"
	"container/heap"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
	"golang.org/x/sync/errgroup"
)

// SubCompactor partitions a key range into N parallel sub-jobs
// (REQ000319). Each sub-job is a compactionJob with the same
// overlap, rate limiter, and placement policy as the parent — so
// sub-compaction produces equivalent state to serial compaction but
// in parallel sub-ranges. REQ001048.
type SubCompactor struct {
	fs          FS
	dir         string
	manifest    *manifest
	concurrency int
}

// NewSubCompactor creates a sub-compactor with the given concurrency.
func NewSubCompactor(fs FS, dir string, m *manifest, concurrency int) *SubCompactor {
	if concurrency < 1 {
		concurrency = 1
	}
	return &SubCompactor{fs: fs, dir: dir, manifest: m, concurrency: concurrency}
}

// SubCompactionOptions carries the cross-cutting compaction settings
// that must be forwarded to every sub-job so sub-compaction matches
// the semantics of a single serial compactionJob. REQ001048.
type SubCompactionOptions struct {
	// Overlap files at level+1 that fall within the input range.
	// Each sub-job processes only the overlap files whose key range
	// intersects the sub-range (filtering happens in RunSubCompaction).
	Overlap []SSTFileMeta
	// RateLimiter optionally throttles bytes/sec. May be nil.
	RateLimiter *RateLimiter
	// PlacementPolicy controls tier-aware output placement.
	PlacementPolicy PlacementPolicy
}

type CompactionJobResult struct {
	SourceLevel int
	Inputs      []SSTFileMeta
	Outputs     []SSTFileMeta
}

// RunSubCompaction splits inputs into parallel sub-range compaction jobs.
// REQ001157: two-phase approach — each sub-job writes a partial SST to
// its own tmpPath (no manifest touch), then the coordinator merges all
// partial outputs and applies a single manifest update. This eliminates
// the manifest data race that occurred when parallel sub-jobs each
// called compactionJob.Run independently.
func (sc *SubCompactor) RunSubCompaction(ctx context.Context, sourceLevel int, inputs []SSTFileMeta, opts SubCompactionOptions) (*CompactionJobResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, ErrNoFilesToCompact
	}
	pivots := pivotKeys(inputs, sc.concurrency)
	if len(pivots) < 2 {
		job := &compactionJob{
			fs:              sc.fs,
			level:           sourceLevel,
			inputs:          inputs,
			overlap:         opts.Overlap,
			rateLimiter:     opts.RateLimiter,
			placementPolicy: opts.PlacementPolicy,
		}
		if job.tmpPath == "" {
			tp, err := uniqueSubTempPath(sc.fs, sc.dir)
			if err != nil {
				return nil, err
			}
			job.tmpPath = tp
		}
		if err := job.Run(sc.manifest, sc.dir); err != nil {
			return nil, err
		}
		return &CompactionJobResult{SourceLevel: sourceLevel, Inputs: inputs}, nil
	}

	type sub struct {
		minKey []byte
		maxKey []byte
		job    *compactionJob
	}
	subs := make([]sub, 0, len(pivots)-1)
	for i := 0; i < len(pivots)-1; i++ {
		lo, hi := pivots[i], pivots[i+1]
		var subInputs []SSTFileMeta
		for _, in := range inputs {
			if bytes.Compare(in.MaxKey, lo) < 0 || bytes.Compare(in.MinKey, hi) >= 0 {
				continue
			}
			subInputs = append(subInputs, in)
		}
		if len(subInputs) == 0 {
			continue
		}
		var subOverlap []SSTFileMeta
		for _, ov := range opts.Overlap {
			if bytes.Compare(ov.MaxKey, lo) < 0 || bytes.Compare(ov.MinKey, hi) >= 0 {
				continue
			}
			subOverlap = append(subOverlap, ov)
		}
		subTmp, err := uniqueSubTempPath(sc.fs, sc.dir)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sub{
			minKey: append([]byte(nil), lo...),
			maxKey: append([]byte(nil), hi...),
			job: &compactionJob{
				fs: sc.fs,
				// REQ001157: two-phase compaction — each sub-job writes a partial
				// SST to its own tmpPath, then the coordinator merges all partial
				// outputs and applies a single manifest update.
				level:           sourceLevel,
				inputs:          subInputs,
				overlap:         subOverlap,
				rateLimiter:     opts.RateLimiter,
				placementPolicy: opts.PlacementPolicy,
				tmpPath:         subTmp,
			},
		})
	}

	// REQ001157: Phase 1 — parallel partial SST writes (no manifest touch)
	partialResults := make([]*partialResult, len(subs))
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(sc.concurrency)
	for i := range subs {
		g.Go(func() error {
			if nm.IsAvailable() {
				release := nm.PinWorker()
				defer release()
			}
			pr, err := subs[i].job.RunPartial(sc.dir)
			if err != nil {
				return err
			}
			partialResults[i] = pr
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		// Clean up partial temp files on failure
		for _, pr := range partialResults {
			if pr != nil {
				_ = sc.fs.Remove(pr.tmpPath)
			}
		}
		return nil, err
	}

	// REQ001157: Phase 2 — coordinator merges all partial outputs
	// and applies a single manifest update (serial, no race)
	if err := sc.mergePartials(partialResults, sc.manifest, sc.dir); err != nil {
		return nil, err
	}

	return &CompactionJobResult{
		SourceLevel: sourceLevel,
		Inputs:      inputs,
	}, nil
}

// mergePartials merges all partial SST outputs into a single SST file
// and applies a single manifest update. REQ001157: coordinator phase.
func (sc *SubCompactor) mergePartials(partials []*partialResult, manifest *manifest, dir string) error {
	if len(partials) == 0 {
		return ErrNoFilesToCompact
	}

	allInputs := make([]SSTFileMeta, 0, len(partials))
	allOverlap := make([]SSTFileMeta, 0, len(partials))
	for _, p := range partials {
		allInputs = append(allInputs, p.inputs...)
		allOverlap = append(allOverlap, p.overlap...)
	}

	outputLevel := partials[0].inputs[0].Level + 1
	outputDir := sc.dir // sub-compaction outputs to same dir as inputs

	// Merge all partial SSTs into a single SST
	f, tmpPath, err := sc.fs.CreateTemp(outputDir, "merge-*.tmp")
	if err != nil {
		return err
	}

	defer sc.fs.Remove(tmpPath)
	defer f.Close()

	w := acquireSSTWriter()
	defer releaseSSTWriter(w)

	iters := make([]*sstIterator, 0, len(partials))
	for _, p := range partials {
		reader, err := openSSTLazy(p.tmpPath)
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

	if _, err := f.Write(sstData); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// Determine global min/max key
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
		Level:     outputLevel,
		MinKey:    globalMin,
		MaxKey:    globalMax,
		Size:      int64(len(sstData)),
		BloomBits: 10,
	})
	newPath := filepath.Join(outputDir, newFileName)
	if err := sc.fs.Rename(tmpPath, newPath); err != nil {
		return err
	}

	// Apply manifest update (single, serial — no race)
	newLevels := make([][]SSTFileMeta, len(manifest.Current().levels))
	copy(newLevels, manifest.Current().levels)

	newLevels[partials[0].inputs[0].Level] = removeFiles(newLevels[partials[0].inputs[0].Level], allInputs)
	newLevels[outputLevel] = removeFiles(newLevels[outputLevel], allOverlap)
	newLevels[outputLevel] = append(newLevels[outputLevel], SSTFileMeta{
		FileID:    newFileID,
		Level:     outputLevel,
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
		_ = sc.fs.Remove(newPath)
		return err
	}

	// Remove input files
	for _, input := range allInputs {
		sstPath := filepath.Join(dir, fileName(&input))
		if err := sc.fs.Remove(sstPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove input SST", "path", sstPath, "err", err)
		}
	}

	for _, ov := range allOverlap {
		sstPath := filepath.Join(dir, fileName(&ov))
		if err := sc.fs.Remove(sstPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove overlap SST", "path", sstPath, "err", err)
		}
	}

	// Remove partial temp files
	for _, p := range partials {
		if err := sc.fs.Remove(p.tmpPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("compaction: remove partial tmp", "path", p.tmpPath, "err", err)
		}
	}

	return nil
}

// pivotKeys returns n+1 boundary keys for partitioning.
func pivotKeys(inputs []SSTFileMeta, n int) [][]byte {
	if len(inputs) == 0 || n < 1 {
		return nil
	}
	all := make([][]byte, 0, 2*len(inputs))
	for _, in := range inputs {
		all = append(all, in.MinKey)
		all = append(all, in.MaxKey)
	}
	slices.SortFunc(all, func(a, b []byte) int { return bytes.Compare(a, b) })

	pivots := make([][]byte, 0, n+1)
	step := len(all) / (n + 1)
	if step < 1 {
		step = 1
	}
	pivots = append(pivots, all[0])
	for i := step; i < len(all); i += step {
		pivots = append(pivots, all[i])
		if len(pivots) >= n+1 {
			break
		}
	}
	if len(pivots) == 0 || !bytes.Equal(pivots[len(pivots)-1], all[len(all)-1]) {
		pivots = append(pivots, all[len(all)-1])
	}
	deduped := pivots[:0]
	for i, p := range pivots {
		if i == 0 || bytes.Compare(p, deduped[len(deduped)-1]) > 0 {
			deduped = append(deduped, p)
		}
	}
	return deduped
}

func subTempPath(dir string) string {
	// Legacy entrypoint — kept for any out-of-tree caller. Returns
	// a fixed filename; not safe for parallel sub-compaction.
	return filepath.Join(dir, "subcompact.tmp")
}

// uniqueSubTempPath returns a fresh, unused temp path under dir
// suitable for use as a sub-job's compaction.tmp. The file is
// created (to claim the name) and immediately removed — callers
// must recreate it themselves. REQ001048.
func uniqueSubTempPath(fs FS, dir string) (string, error) {
	if err := fs.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, path, err := fs.CreateTemp(dir, "subcompact-*.tmp")
	if err != nil {
		return "", err
	}
	// path is already returned from CreateTemp
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := fs.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}
