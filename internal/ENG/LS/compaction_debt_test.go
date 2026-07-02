package ls

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestCompactionDebt_PrioritizesLaggingLevel verifies REQ001171: a level at
// 200% budget is compacted before a level at 110% irrespective of ordinal
// order. L0 is at 110% of budget (urgency ~0.10), L1 is at 200% of budget
// (urgency ~1.50) — L1 should be selected despite L0 having lower ordinal.
func TestCompactionDebt_PrioritizesLaggingLevel(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_debt_priority")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fs := DefaultFS()
	manifest, err := newManifestWithFS(dir, fs)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer manifest.Close()

	// Create compactionManager with manual struct (no background loop)
	cm := &compactionManager{
		fs:              fs,
		manifest:        manifest,
		dir:             dir,
		compactionMu:    sync.Mutex{},
		compacting:      atomic.Bool{},
		compactionQueue: make(chan *compactionJob, 10),
		done:            make(chan struct{}),
		loopDone:        make(chan struct{}),
		wg:              sync.WaitGroup{},
		stopOnce:        sync.Once{},
		style:           atomic.Int32{},
		placementPolicy: nil,
		rateLimiter:     atomic.Pointer[RateLimiter]{},
		debts:           make(map[int]int64),
		budget:          defaultBudget,
	}

	// Set compaction style to Leveled (0)
	cm.style.Store(0)

	// Setup manifest with 3 levels
	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	if err := manifest.Apply(*v); err != nil {
		t.Fatalf("apply initial version: %v", err)
	}

	// L0: one file at 110% of 4 MB budget (~4.4 MB)
	// L1: two files at 200% of 32 MB budget (64 MB total, 32 MB each)
	v.levels[0] = []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 4*1024*1024 + 400*1024, BloomBits: 10},
	}
	v.levels[1] = []SSTFileMeta{
		{FileID: 2, Level: 1, MinKey: []byte("b"), MaxKey: []byte("y"), Size: 32 * 1024 * 1024, BloomBits: 10},
		{FileID: 3, Level: 1, MinKey: []byte("c"), MaxKey: []byte("z"), Size: 32 * 1024 * 1024, BloomBits: 10},
	}
	if err := manifest.Apply(*v); err != nil {
		t.Fatalf("apply version with files: %v", err)
	}

	// Verify initial state
	v = manifest.Current()
	if len(v.levels[0]) != 1 {
		t.Fatalf("expected 1 file at L0, got %d", len(v.levels[0]))
	}
	if len(v.levels[1]) != 2 {
		t.Fatalf("expected 2 files at L1, got %d", len(v.levels[1]))
	}

	// Recalculate debts before MaybeCompact
	cm.recalculateDebts()

	// Sanity: L1 urgency > L0 urgency
	l0urgency := float64(cm.debts[0]) / float64(cm.budget.l0) * (1.0 + 0.5*0)
	l1urgency := float64(cm.debts[1]) / float64(cm.budget.l1) * (1.0 + 0.5*1)
	if l1urgency <= l0urgency {
		t.Fatalf("setup error: L1 urgency (%.3f) should exceed L0 urgency (%.3f)", l1urgency, l0urgency)
	}

	// Call MaybeCompact — should select L1 (highest urgency) over L0
	cm.MaybeCompact()

	// Read the job from the queue
	job, ok := <-cm.compactionQueue
	if !ok {
		t.Fatal("no job was queued by MaybeCompact")
	}

	if job.level != 1 {
		t.Errorf("REQ001171: MaybeCompact selected level %d, want 1 (L1 has highest urgency %.2f vs L0 %.2f)",
			job.level, l1urgency, l0urgency)
	}
}

// TestCompactionDebt_RecalculateDebts computes debt = max(0, actualSize - budget)
// per level and stores it in cm.debts.
func TestCompactionDebt_RecalculateDebts(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_debt_recalc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fs := DefaultFS()
	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer manifest.Close()

	cm := &compactionManager{
		fs:              fs,
		manifest:        manifest,
		dir:             dir,
		compactionMu:    sync.Mutex{},
		compacting:      atomic.Bool{},
		compactionQueue: make(chan *compactionJob, 10),
		done:            make(chan struct{}),
		loopDone:        make(chan struct{}),
		wg:              sync.WaitGroup{},
		stopOnce:        sync.Once{},
		style:           atomic.Int32{},
		placementPolicy: nil,
		rateLimiter:     atomic.Pointer[RateLimiter]{},
		budget:          defaultBudget,
		debts:           make(map[int]int64),
	}

	// Setup: L0 has 10 MB of files (budget = 4 MB → debt = 6 MB)
	// L1 has 20 MB of files (budget = 32 MB → debt = 0)
	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	v.levels[0] = []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 10 * 1024 * 1024, BloomBits: 10},
	}
	v.levels[1] = []SSTFileMeta{
		{FileID: 2, Level: 1, MinKey: []byte("b"), MaxKey: []byte("y"), Size: 20 * 1024 * 1024, BloomBits: 10},
	}
	manifest.Apply(*v)

	cm.recalculateDebts()

	wantL0Debt := int64(10*1024*1024 - 4*1024*1024) // 6 MB
	wantL1Debt := int64(0)

	if got := cm.debts[0]; got != wantL0Debt {
		t.Errorf("debt[0] = %d, want %d", got, wantL0Debt)
	}
	if got := cm.debts[1]; got != wantL1Debt {
		// Debt is max(0, size - budget); 20 MB < 32 MB budget → 0
		t.Errorf("debt[1] = %d, want %d", got, wantL1Debt)
	}
	if got := cm.debts[2]; got != 0 {
		t.Errorf("debt[2] = %d, want 0 (no files)", got)
	}
}

// TestCompactionDebt_UrgencyScore checks that urgency is computed as
// (debt / budget) * (1.0 + 0.5 * level).
func TestCompactionDebt_UrgencyScore(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_debt_urgency")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fs := DefaultFS()
	manifest, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	defer manifest.Close()

	cm := &compactionManager{
		fs:              fs,
		manifest:        manifest,
		dir:             dir,
		compactionMu:    sync.Mutex{},
		compacting:      atomic.Bool{},
		compactionQueue: make(chan *compactionJob, 10),
		done:            make(chan struct{}),
		loopDone:        make(chan struct{}),
		wg:              sync.WaitGroup{},
		stopOnce:        sync.Once{},
		debts:           make(map[int]int64),
		budget:          defaultBudget,
		rateLimiter:     atomic.Pointer[RateLimiter]{},
		style:           atomic.Int32{},
		placementPolicy: nil,
	}

	// Setup: L0 debt = 4 MB (size 8 MB, budget 4 MB)
	// L1 debt = 32 MB (size 64 MB, budget 32 MB)
	v := manifest.Current()
	v.levels = make([][]SSTFileMeta, 3)
	v.levels[0] = []SSTFileMeta{
		{FileID: 1, Level: 0, MinKey: []byte("a"), MaxKey: []byte("z"), Size: 8 * 1024 * 1024, BloomBits: 10},
	}
	// L1: two files of 32 MB each = 64 MB total, budget 32 MB → debt = 32 MB
	v.levels[1] = []SSTFileMeta{
		{FileID: 2, Level: 1, MinKey: []byte("b"), MaxKey: []byte("y"), Size: 32 * 1024 * 1024, BloomBits: 10},
		{FileID: 3, Level: 1, MinKey: []byte("c"), MaxKey: []byte("z"), Size: 32 * 1024 * 1024, BloomBits: 10},
	}
	manifest.Apply(*v)

	cm.recalculateDebts()

	// Compute urgency manually
	l0Urgency := float64(cm.debts[0]) / float64(cm.budget.l0) * (1.0 + 0.5*0)
	l1Urgency := float64(cm.debts[1]) / float64(cm.budget.l1) * (1.0 + 0.5*1)

	// L0: (4MB / 4MB) * 1.0 = 1.0
	// L1: (32MB / 32MB) * 1.5 = 1.5
	// L1 > L0, so L1 should be selected
	if l1Urgency <= l0Urgency {
		t.Fatalf("expected L1 urgency (%.2f) > L0 urgency (%.2f)", l1Urgency, l0Urgency)
	}

	t.Logf("L0 urgency: %.2f, L1 urgency: %.2f", l0Urgency, l1Urgency)
}
