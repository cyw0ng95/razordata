package ls

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"

	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
)

// SubCompactor partitions a key range into N sub-jobs and runs them
// in parallel via a worker pool, then merges the per-sub-range SST
// outputs back into the level. REQ000319.
//
// Algorithm:
//  1. Pick pivot keys from the input SSTs (sample-and-bucket).
//  2. For each [pivot_i, pivot_i+1) range, build a sub-job that
//     merges only the records in that range from all inputs.
//  3. Run sub-jobs on a worker pool; each produces one output SST.
//  4. After all sub-jobs complete, atomically update the manifest
//     with the new SST list (replacing the inputs).
//
// Sub-compaction is enabled for L4+ (large ranges) where the
// merge work is dominated by I/O. Smaller levels are still
// compacted in a single job.
type SubCompactor struct {
	dir         string
	manifest    *manifest
	concurrency int
}

// NewSubCompactor creates a sub-compactor. concurrency is the
// maximum number of sub-jobs to run in parallel. Must be >= 1.
func NewSubCompactor(dir string, m *manifest, concurrency int) *SubCompactor {
	if concurrency < 1 {
		concurrency = 1
	}
	return &SubCompactor{dir: dir, manifest: m, concurrency: concurrency}
}

// CompactionJobResult holds the outputs of a successful compaction.
type CompactionJobResult struct {
	SourceLevel int
	Inputs      []SSTFileMeta
	Outputs     []SSTFileMeta
}

// RunSubCompaction splits the given input files into n sub-ranges
// and runs each sub-range as a parallel compaction job. Returns
// the union of all output files.
//
// The caller is responsible for applying the result to the manifest.
func (sc *SubCompactor) RunSubCompaction(ctx context.Context, sourceLevel int, inputs []SSTFileMeta) (*CompactionJobResult, error) {
	if len(inputs) == 0 {
		return nil, ErrNoFilesToCompact
	}
	// Sample pivots: pick N+1 boundary keys by sorting the
	// distinct min/max keys of the inputs and selecting every
	// n-th key. This gives a coarse partition that respects the
	// actual key distribution.
	pivots := pivotKeys(inputs, sc.concurrency)
	if len(pivots) < 2 {
		// Fall back to a single sub-job.
		job := &compactionJob{level: sourceLevel, inputs: inputs}
		if err := job.Run(sc.manifest, sc.dir); err != nil {
			return nil, err
		}
		return &CompactionJobResult{SourceLevel: sourceLevel, Inputs: inputs}, nil
	}

	// Partition inputs by pivot. Each input may span multiple
	// pivots, so it can appear in multiple sub-jobs.
	type sub struct {
		minKey []byte
		maxKey []byte
		job    *compactionJob
	}
	subs := make([]sub, 0, len(pivots)-1)
	for i := 0; i < len(pivots)-1; i++ {
		lo, hi := pivots[i], pivots[i+1]
		// Collect inputs whose [MinKey, MaxKey] intersect [lo, hi).
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
		subs = append(subs, sub{
			minKey: append([]byte(nil), lo...),
			maxKey: append([]byte(nil), hi...),
			job:    &compactionJob{level: sourceLevel, inputs: subInputs},
		})
	}

	// Run sub-jobs in parallel.
	var wg sync.WaitGroup
	sem := make(chan struct{}, sc.concurrency)
	errs := make([]error, len(subs))
	for i := range subs {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			// REQ000309 (iter-27): pin the subcompaction worker
			// to its OS thread so first-touch allocations land
			// on the local NUMA node. On non-NUMA hosts this is
			// a no-op (just a LockOSThread/UnlockOSThread pair).
			if nm.IsAvailable() {
				release := nm.PinWorker()
				defer release()
			}
			if err := subs[idx].job.Run(sc.manifest, sc.dir); err != nil {
				errs[idx] = err
			}
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}

	return &CompactionJobResult{
		SourceLevel: sourceLevel,
		Inputs:      inputs,
	}, nil
}

// pivotKeys returns n+1 boundary keys for partitioning. The first
// and last pivots are the global min and max keys respectively.
// n is the desired number of sub-ranges.
func pivotKeys(inputs []SSTFileMeta, n int) [][]byte {
	if len(inputs) == 0 || n < 1 {
		return nil
	}
	// Collect all distinct min/max keys and sort.
	all := make([][]byte, 0, 2*len(inputs))
	for _, in := range inputs {
		all = append(all, in.MinKey)
		all = append(all, in.MaxKey)
	}
	sort.Slice(all, func(i, j int) bool { return bytes.Compare(all[i], all[j]) < 0 })

	// Pick n+1 pivots uniformly.
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
	// Ensure strict ordering (no duplicates).
	deduped := pivots[:0]
	for i, p := range pivots {
		if i == 0 || bytes.Compare(p, deduped[len(deduped)-1]) > 0 {
			deduped = append(deduped, p)
		}
	}
	return deduped
}

// ensureUniqueTmp returns a unique temp filename for the sub-compactor.
func subTempPath(dir string) string {
	f, err := os.CreateTemp(dir, "subcompact-*.tmp")
	if err == nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	return filepath.Join(dir, "subcompact.tmp")
}


