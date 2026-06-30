package ls

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"

	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
	"golang.org/x/sync/errgroup"
)

// SubCompactor partitions a key range into N parallel sub-jobs
// (REQ000319). Each sub-job is a compactionJob with the same
// overlap, rate limiter, and placement policy as the parent — so
// sub-compaction produces equivalent state to serial compaction but
// in parallel sub-ranges. REQ001048.
type SubCompactor struct {
	dir         string
	manifest    *manifest
	concurrency int
}

// NewSubCompactor creates a sub-compactor with the given concurrency.
func NewSubCompactor(dir string, m *manifest, concurrency int) *SubCompactor {
	if concurrency < 1 {
		concurrency = 1
	}
	return &SubCompactor{dir: dir, manifest: m, concurrency: concurrency}
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
// Each sub-job inherits the parent job's overlap, rate limiter, and
// placement policy so the merged output is equivalent to a single
// serial compactionJob over the full input range. REQ001048.
//
// If `inputs` is too small to benefit from partitioning (pivots < 2),
// it falls back to a serial compactionJob.Run for simplicity.
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
			level:           sourceLevel,
			inputs:          inputs,
			overlap:         opts.Overlap,
			rateLimiter:     opts.RateLimiter,
			placementPolicy: opts.PlacementPolicy,
		}
		// REQ001048: even in single-job fallback, set a unique
		// tmpPath so the legacy hard-coded name doesn't race with
		// future parallel sub-jobs sharing the same temp dir.
		if job.tmpPath == "" {
			tp, err := uniqueSubTempPath(sc.dir)
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
		// REQ001048: each sub-job sees only the overlap files whose
		// key range intersects this sub-range. Filtering prevents
		// double-counting overlap data across sub-jobs.
		var subOverlap []SSTFileMeta
		for _, ov := range opts.Overlap {
			if bytes.Compare(ov.MaxKey, lo) < 0 || bytes.Compare(ov.MinKey, hi) >= 0 {
				continue
			}
			subOverlap = append(subOverlap, ov)
		}
		// REQ001048: each sub-job gets a unique tmpPath so parallel
		// goroutines don't collide on the shared compaction.tmp
		// filename inside compactionJob.Run. We use os.CreateTemp
		// to guarantee uniqueness; the file is closed and removed
		// immediately — compactionJob.Run will recreate it.
		subTmp, err := uniqueSubTempPath(sc.dir)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sub{
			minKey: append([]byte(nil), lo...),
			maxKey: append([]byte(nil), hi...),
			job: &compactionJob{
				level:           sourceLevel,
				inputs:          subInputs,
				overlap:         subOverlap,
				rateLimiter:     opts.RateLimiter,
				placementPolicy: opts.PlacementPolicy,
				tmpPath:         subTmp,
			},
		})
	}

	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(sc.concurrency)
	for i := range subs {
		g.Go(func() error {
			if nm.IsAvailable() {
				release := nm.PinWorker()
				defer release()
			}
			if err := subs[i].job.Run(sc.manifest, sc.dir); err != nil {
				return err
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return &CompactionJobResult{
		SourceLevel: sourceLevel,
		Inputs:      inputs,
	}, nil
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
func uniqueSubTempPath(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "subcompact-*.tmp")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}
