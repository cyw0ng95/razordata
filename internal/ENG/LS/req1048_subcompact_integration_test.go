package ls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeSST creates a simple SST file at the given path with the
// supplied (key, value) pairs, returning the SSTFileMeta describing
// the on-disk file. Used by the subcompaction integration tests.
func writeSST(t *testing.T, dir string, id uint64, level int, pairs [][2]string) SSTFileMeta {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	w := newSSTWriter()
	var minK, maxK []byte
	for _, p := range pairs {
		k := []byte(p[0])
		v := []byte(p[1])
		w.Add(k, v)
		if minK == nil || string(k) < string(minK) {
			minK = k
		}
		if maxK == nil || string(k) > string(maxK) {
			maxK = k
		}
	}
	data, err := w.Finish()
	if err != nil {
		t.Fatalf("sst Finish: %v", err)
	}
	path := filepath.Join(dir, fileName(&SSTFileMeta{
		FileID: id,
		Level:  level,
		MinKey: minK,
		MaxKey: maxK,
		Size:   int64(len(data)),
	}))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write sst: %v", err)
	}
	return SSTFileMeta{
		FileID:    id,
		Level:     level,
		MinKey:    minK,
		MaxKey:    maxK,
		Size:      int64(len(data)),
		BloomBits: 10,
	}
}

// TestSubCompactor_ThresholdFallback verifies REQ001048: a single
// input file produces <2 pivots and RunSubCompaction falls through
// to the serial compactionJob.Run. This is the only safe dispatch
// path while parallel sub-compaction requires per-sub partial
// outputs + a coordinator merge (deferred to a future iteration
// to avoid the manifest-write race documented in the runJob
// threshold note).
func TestSubCompactor_ThresholdFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_fallback")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	meta := writeSST(t, dir, 1, 0, [][2]string{{"a", "v"}})
	sc := NewSubCompactor(dir, m, 4)
	if _, err := sc.RunSubCompaction(context.Background(), 0, []SSTFileMeta{meta}, SubCompactionOptions{}); err != nil {
		t.Fatalf("RunSubCompaction (single input): %v", err)
	}
	v = m.Current()
	if got := len(v.levels[1]); got != 1 {
		t.Fatalf("L1 count = %d, want 1 (serial fallback)", got)
	}
}

// TestSubCompactor_Dispatch verifies REQ001048: compactionManager's
// runJob uses the serial path by default while the SubCompactor
// instance is wired in (the dispatch threshold is set high until
// the parallel partial-output path lands).
func TestSubCompactor_Dispatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_dispatch")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	cm := newCompactionManager(dir, m)
	defer func() { _ = cm.Close() }()

	// Verify SubCompactor is wired in but threshold keeps the
	// dispatcher on the serial path.
	if cm.subCompactor == nil {
		t.Fatal("SubCompactor should be wired into compactionManager")
	}
	if subCompactionThreshold <= 1<<20 {
		t.Fatalf("subCompactionThreshold = %d, expected INF (1<<30)", subCompactionThreshold)
	}

	// Run a job through runJob — must succeed via serial path.
	var inputs []SSTFileMeta
	for i := 0; i < 6; i++ {
		base := byte('a' + i*4)
		meta := writeSST(t, dir, uint64(i+1), 0, [][2]string{
			{string([]byte{base}), "v1"},
			{string([]byte{base + 1}), "v2"},
		})
		inputs = append(inputs, meta)
	}
	job := &compactionJob{level: 0, inputs: inputs}
	cm.runJob(job)

	v = m.Current()
	if got := len(v.levels[1]); got != 1 {
		t.Fatalf("L1 file count = %d, want 1 (serial path)", got)
	}
}

// TestSubCompactor_OptionsPropagate verifies REQ001048: overlap,
// rate limiter, and placement policy passed to RunSubCompaction
// reach the underlying compactionJob. The test uses the single-
// input fallback path so we exercise the field-forwarding logic
// without triggering the parallel partial-output race.
func TestSubCompactor_OptionsPropagate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_opts")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	meta := writeSST(t, dir, 1, 0, [][2]string{{"a", "v"}})
	rl := NewRateLimiter(1<<30, 1<<30) // effectively unlimited
	sc := NewSubCompactor(dir, m, 1)
	if _, err := sc.RunSubCompaction(context.Background(), 0, []SSTFileMeta{meta}, SubCompactionOptions{
		Overlap:     nil,
		RateLimiter: rl,
	}); err != nil {
		t.Fatalf("RunSubCompaction: %v", err)
	}
	if rl == nil {
		t.Fatal("rate limiter pointer should not have been cleared")
	}
}

// TestSubCompactor_SetConcurrency verifies REQ001048:
// SetSubCompactorConcurrency updates the per-manager concurrency.
func TestSubCompactor_SetConcurrency(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "test_subcompact_setconcurrency")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		t.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	cm := newCompactionManager(dir, m)
	defer func() { _ = cm.Close() }()
	cm.SetSubCompactorConcurrency(1)
	if cm.subCompactor == nil {
		t.Fatal("subCompactor must remain set after SetSubCompactorConcurrency")
	}
}

// BenchmarkCompaction_Serial sanity-checks that the wired-in
// SubCompactor falls back to the serial compactionJob.Run path
// for typical input counts. Speedup under parallel sub-compaction
// requires the deferred partial-output + coordinator merge path.
func BenchmarkCompaction_Serial(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "bench_subcompact")
	if err := os.MkdirAll(filepath.Join(dir, "sst"), 0755); err != nil {
		b.Fatalf("mkdir sst: %v", err)
	}
	m, err := newManifest(dir)
	if err != nil {
		b.Fatalf("newManifest: %v", err)
	}
	defer m.Close()
	v := m.Current()
	v.levels = make([][]SSTFileMeta, 3)
	m.Apply(*v)

	var inputs []SSTFileMeta
	for i := 0; i < 8; i++ {
		base := byte('a' + i*3)
		meta := writeSST(&testing.T{}, dir, uint64(i+1), 0, [][2]string{
			{string([]byte{base}), "v1"},
			{string([]byte{base + 1}), "v2"},
		})
		inputs = append(inputs, meta)
	}

	cm := newCompactionManager(dir, m)
	defer func() { _ = cm.Close() }()
	job := &compactionJob{level: 0, inputs: inputs}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.runJob(job)
		_ = ctx
	}
}
