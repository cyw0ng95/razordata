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

// SubCompactor partitions a key range into N parallel sub-jobs (REQ000319).
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

type CompactionJobResult struct {
	SourceLevel int
	Inputs      []SSTFileMeta
	Outputs     []SSTFileMeta
}

// RunSubCompaction splits inputs into parallel sub-range compaction jobs.
func (sc *SubCompactor) RunSubCompaction(ctx context.Context, sourceLevel int, inputs []SSTFileMeta) (*CompactionJobResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, ErrNoFilesToCompact
	}
	pivots := pivotKeys(inputs, sc.concurrency)
	if len(pivots) < 2 {
		job := &compactionJob{level: sourceLevel, inputs: inputs}
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
		subs = append(subs, sub{
			minKey: append([]byte(nil), lo...),
			maxKey: append([]byte(nil), hi...),
			job:    &compactionJob{level: sourceLevel, inputs: subInputs},
		})
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, sc.concurrency)
	errs := make([]error, len(subs))
	for i := range subs {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
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
	sort.Slice(all, func(i, j int) bool { return bytes.Compare(all[i], all[j]) < 0 })

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
	f, err := os.CreateTemp(dir, "subcompact-*.tmp")
	if err == nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	return filepath.Join(dir, "subcompact.tmp")
}
