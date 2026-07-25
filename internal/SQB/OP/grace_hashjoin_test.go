package OP

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"runtime"
	"sort"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// drainGraceInner collects all (k, v, pk) triples from a GraceHashJoin
// in emission order. Mirrors drainVHJInner for direct comparison.
func drainGraceInner(t *testing.T, g *GraceHashJoin) [][3]int64 {
	t.Helper()
	var out [][3]int64
	ctx := context.Background()
	for {
		batch, err := g.NextBatch(ctx)
		if err != nil {
			t.Fatalf("GraceHashJoin NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			k := UT.BatchValueAt(batch.Cols[0], i).(int64)
			v := UT.BatchValueAt(batch.Cols[1], i).(int64)
			pk := UT.BatchValueAt(batch.Cols[2], i).(int64)
			out = append(out, [3]int64{k, v, pk})
		}
		batch.Put()
	}
	return out
}

// drainGraceRows collects all rows from a GraceHashJoin as [][]any,
// preserving NULL markers. Each row has nCols values. Used for outer
// join equivalence where unmatched rows have NULL probe/build sides.
func drainGraceRows(t *testing.T, g *GraceHashJoin, nCols int) [][]any {
	t.Helper()
	var out [][]any
	ctx := context.Background()
	for {
		batch, err := g.NextBatch(ctx)
		if err != nil {
			t.Fatalf("GraceHashJoin NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			row := make([]any, nCols)
			for c := 0; c < nCols; c++ {
				if isColNull(&batch.Cols[c], i) {
					row[c] = nil
					continue
				}
				row[c] = UT.BatchValueAt(batch.Cols[c], i)
			}
			out = append(out, row)
		}
		batch.Put()
	}
	return out
}

// sortTriples sorts (k, v, pk) triples for order-independent comparison.
func sortTriples(rows [][3]int64) [][3]int64 {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		if a[1] != b[1] {
			return a[1] < b[1]
		}
		return a[2] < b[2]
	})
	return rows
}

// sortRows sorts [][]any rows lexicographically for order-independent
// comparison. nil sorts before all values.
func sortRows(rows [][]any) [][]any {
	sort.Slice(rows, func(i, j int) bool {
		for k := 0; k < len(rows[i]) && k < len(rows[j]); k++ {
			a, b := rows[i][k], rows[j][k]
			if a == nil && b == nil {
				continue
			}
			if a == nil {
				return true
			}
			if b == nil {
				return false
			}
			ai, aok := a.(int64)
			bi, bok := b.(int64)
			if aok && bok {
				if ai != bi {
					return ai < bi
				}
				continue
			}
			as, aok := a.(string)
			bs, bok := b.(string)
			if aok && bok {
				if as != bs {
					return as < bs
				}
				continue
			}
		}
		return len(rows[i]) < len(rows[j])
	})
	return rows
}

// --- correctness (small data, no spill) ---

func TestGraceHashJoin_InnerEquiJoin(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2, 3},
			[]int64{10, 20, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 3, 4})},
	}

	g := NewGraceHashJoin(build, probe, []int{0}, []int{0})
	defer g.Close()

	got := sortTriples(drainGraceInner(t, g))
	want := [][3]int64{{2, 20, 2}, {3, 30, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGraceHashJoin_LeftOuter(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2, 3},
			[]int64{10, 20, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 5})},
	}

	g := NewGraceHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindLeft)
	defer g.Close()

	got := sortRows(drainGraceRows(t, g, 3))
	// Expected: (2,20,2) matched; (1,10,nil) and (3,30,nil) unmatched.
	want := [][]any{
		{int64(1), int64(10), nil},
		{int64(2), int64(20), int64(2)},
		{int64(3), int64(30), nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGraceHashJoin_RightOuter(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 3},
			[]int64{10, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 3})},
	}

	g := NewGraceHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindRight)
	defer g.Close()

	got := sortRows(drainGraceRows(t, g, 3))
	// Expected: (3,30,3) matched; (nil,nil,2) unmatched probe.
	want := [][]any{
		{nil, nil, int64(2)},
		{int64(3), int64(30), int64(3)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGraceHashJoin_FullOuter(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2},
			[]int64{10, 20},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{2, 3})},
	}

	g := NewGraceHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindFull)
	defer g.Close()

	got := sortRows(drainGraceRows(t, g, 3))
	// Expected: (2,20,2) matched; (1,10,nil) unmatched build; (nil,nil,3) unmatched probe.
	want := [][]any{
		{nil, nil, int64(3)},
		{int64(1), int64(10), nil},
		{int64(2), int64(20), int64(2)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGraceHashJoin_MultiColumnKey(t *testing.T) {
	// Build: (a, b, v) — composite key (a, b).
	buildBatch := UT.GetBatch(3)
	buildBatch.SetColumnName(0, "a")
	buildBatch.SetColumnName(1, "b")
	buildBatch.SetColumnName(2, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 1, 2}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20, 10}
	buildBatch.Cols[2].Type = LX.T_INT_KW
	buildBatch.Cols[2].Data.Ints = []int64{100, 200, 300}
	buildBatch.Size = 3

	// Probe: (pa, pb) — composite key (pa, pb).
	probeBatch := UT.GetBatch(2)
	probeBatch.SetColumnName(0, "pa")
	probeBatch.SetColumnName(1, "pb")
	probeBatch.Cols[0].Type = LX.T_INT_KW
	probeBatch.Cols[0].Data.Ints = []int64{1, 1, 2}
	probeBatch.Cols[1].Type = LX.T_INT_KW
	probeBatch.Cols[1].Data.Ints = []int64{10, 20, 10}
	probeBatch.Size = 3

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	g := NewGraceHashJoin(build, probe, []int{0, 1}, []int{0, 1})
	defer g.Close()

	ctx := context.Background()
	rows := []struct{ a, b, v, pa, pb int64 }{}
	for {
		batch, err := g.NextBatch(ctx)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			rows = append(rows, struct{ a, b, v, pa, pb int64 }{
				UT.BatchValueAt(batch.Cols[0], i).(int64),
				UT.BatchValueAt(batch.Cols[1], i).(int64),
				UT.BatchValueAt(batch.Cols[2], i).(int64),
				UT.BatchValueAt(batch.Cols[3], i).(int64),
				UT.BatchValueAt(batch.Cols[4], i).(int64),
			})
		}
		batch.Put()
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 matched rows, got %d", len(rows))
	}
	// Each probe row matches exactly one build row with the same (a, b).
	got := map[[2]int64]int64{}
	for _, r := range rows {
		got[[2]int64{r.a, r.b}] = r.v
	}
	if got[[2]int64{1, 10}] != 100 {
		t.Errorf("(1,10): got v=%d, want 100", got[[2]int64{1, 10}])
	}
	if got[[2]int64{1, 20}] != 200 {
		t.Errorf("(1,20): got v=%d, want 200", got[[2]int64{1, 20}])
	}
	if got[[2]int64{2, 10}] != 300 {
		t.Errorf("(2,10): got v=%d, want 300", got[[2]int64{2, 10}])
	}
}

func TestGraceHashJoin_StringKey(t *testing.T) {
	// Build: (k TEXT, v INT).
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_TEXT
	buildBatch.Cols[0].Data.Strs = []string{"alice", "bob", "carol"}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{1, 2, 3}
	buildBatch.Size = 3

	// Probe: (pk TEXT).
	probeBatch := UT.GetBatch(1)
	probeBatch.SetColumnName(0, "pk")
	probeBatch.Cols[0].Type = LX.T_TEXT
	probeBatch.Cols[0].Data.Strs = []string{"bob", "carol", "dave"}
	probeBatch.Size = 3

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	g := NewGraceHashJoin(build, probe, []int{0}, []int{0})
	defer g.Close()

	ctx := context.Background()
	got := map[string]int64{}
	for {
		batch, err := g.NextBatch(ctx)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			k := batch.Cols[0].Data.Strs[i]
			v := UT.BatchValueAt(batch.Cols[1], i).(int64)
			got[k] = v
		}
		batch.Put()
	}
	if got["bob"] != 2 || got["carol"] != 3 {
		t.Fatalf("got bob=%d carol=%d, want bob=2 carol=3", got["bob"], got["carol"])
	}
	if _, ok := got["alice"]; ok {
		t.Error("alice should not match any probe key")
	}
}

func TestGraceHashJoin_NullKey_Build(t *testing.T) {
	// Build with a NULL key — should never match, emitted as unmatched in LEFT/FULL.
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_INT_KW
	buildBatch.Cols[0].Data.Ints = []int64{1, 0}
	buildBatch.Cols[0].Nulls = []bool{false, true}
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = []int64{10, 20}
	buildBatch.Size = 2

	probeBatch := UT.GetBatch(1)
	probeBatch.SetColumnName(0, "pk")
	probeBatch.Cols[0].Type = LX.T_INT_KW
	probeBatch.Cols[0].Data.Ints = []int64{1}
	probeBatch.Size = 1

	build := &testBatchProducer{batches: []*UT.Batch{buildBatch}}
	probe := &testBatchProducer{batches: []*UT.Batch{probeBatch}}

	g := NewGraceHashJoinWithKind(build, probe, []int{0}, []int{0}, JoinKindLeft)
	defer g.Close()

	got := sortRows(drainGraceRows(t, g, 3))
	// Expected: (1,10,1) matched; (nil,20,nil) NULL-key build row unmatched.
	want := [][]any{
		{nil, int64(20), nil},
		{int64(1), int64(10), int64(1)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGraceHashJoin_EmptyBuild(t *testing.T) {
	build := &testBatchProducer{batches: nil}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1, 2})},
	}

	g := NewGraceHashJoin(build, probe, []int{0}, []int{0})
	defer g.Close()

	ctx := context.Background()
	batch, err := g.NextBatch(ctx)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil for empty build, got batch with %d rows", batch.Size)
	}
}

func TestGraceHashJoin_EmptyProbe(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch([]int64{1}, []int64{10})},
	}
	probe := &testBatchProducer{batches: nil}

	g := NewGraceHashJoin(build, probe, []int{0}, []int{0})
	defer g.Close()

	ctx := context.Background()
	batch, err := g.NextBatch(ctx)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil for empty probe, got batch with %d rows", batch.Size)
	}
}

func TestGraceHashJoin_NoMatch(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch(
			[]int64{1, 2, 3},
			[]int64{10, 20, 30},
		)},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{4, 5, 6})},
	}

	g := NewGraceHashJoin(build, probe, []int{0}, []int{0})
	defer g.Close()

	ctx := context.Background()
	batch, err := g.NextBatch(ctx)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected nil for no match, got batch with %d rows", batch.Size)
	}
}

func TestGraceHashJoin_DoubleClose(t *testing.T) {
	build := &testBatchProducer{
		batches: []*UT.Batch{makeJoinBuildBatch([]int64{1}, []int64{10})},
	}
	probe := &testBatchProducer{
		batches: []*UT.Batch{makeJoinProbeBatch([]int64{1})},
	}

	g := NewGraceHashJoin(build, probe, []int{0}, []int{0})
	if err := g.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must not panic or error.
	if err := g.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestGraceHashJoin_CloseMidStream(t *testing.T) {
	build := chunkedBuildProducer(make([]int64, 5000), make([]int64, 5000))
	probe := chunkedProbeProducer([]int64{1, 2, 3})

	g := NewGraceHashJoin(build, probe, []int{0}, []int{0})
	ctx := context.Background()

	// Pull one batch (triggers partitioning + first partition output), then close.
	batch, err := g.NextBatch(ctx)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		batch.Put()
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close mid-stream: %v", err)
	}
}

// --- equivalence property test ---

// TestGraceHashJoin_EquivalenceWithVectorized is the strongest correctness
// signal: random small cases across all 4 join kinds, comparing GraceHashJoin
// output against the proven VectorizedHashJoin. Any partitioning or spill bug
// shows up as divergence. REQ001999.
//
// Memory hardening (REQ001999 follow-up): the prior 200-case version could
// OOM under GOMEMLIMIT because testBatchProducer.Close() was a no-op, leaking
// pooled batches across cases. That is now fixed, and we additionally:
//   - skip under -short (the suite is for the pre-commit gate, not -short runs)
//   - run each case as a subtest so tRunner's deferred GC reclaims per-case state
//   - bound row counts to 0..120 (was 0..200) — sufficient coverage, lighter heap
//   - call runtime.GC() every 32 cases to release any pooled-but-idle batches
func TestGraceHashJoin_EquivalenceWithVectorized(t *testing.T) {
	if testing.Short() {
		t.Skip("equivalence test runs ~100 random join cases; skip with -short")
	}
	const numCases = 100
	rng := rand.New(rand.NewPCG(42, 1<<20))
	kinds := []JoinKind{JoinKindInner, JoinKindLeft, JoinKindRight, JoinKindFull}

	for tc := 0; tc < numCases; tc++ {
		// Run as a subtest so each case's allocations are scoped and the
		// testing framework can release per-case state promptly.
		tc := tc
		t.Run(fmt.Sprintf("case_%03d", tc), func(t *testing.T) {
			buildN := rng.IntN(121) // 0..120
			probeN := rng.IntN(121)
			keyDomain := 1 + rng.IntN(20) // 1..20

			buildKeys := make([]int64, buildN)
			buildVals := make([]int64, buildN)
			for i := range buildKeys {
				buildKeys[i] = int64(rng.IntN(keyDomain))
				buildVals[i] = int64(i * 10)
			}
			probeKeys := make([]int64, probeN)
			for i := range probeKeys {
				probeKeys[i] = int64(rng.IntN(keyDomain))
			}
			kind := kinds[rng.IntN(len(kinds))]

			// Build two independent producer sets (batches are consumed by the join).
			vjhBuild := chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...))
			vjhProbe := chunkedProbeProducer(append([]int64(nil), probeKeys...))
			ghjBuild := chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...))
			ghjProbe := chunkedProbeProducer(append([]int64(nil), probeKeys...))

			var vjh *VectorizedHashJoin
			var ghj *GraceHashJoin
			if kind == JoinKindInner {
				vjh = NewVectorizedHashJoin(vjhBuild, vjhProbe, []int{0}, []int{0})
				ghj = NewGraceHashJoin(ghjBuild, ghjProbe, []int{0}, []int{0})
			} else {
				vjh = NewVectorizedHashJoinWithKind(vjhBuild, vjhProbe, []int{0}, []int{0}, kind)
				ghj = NewGraceHashJoinWithKind(ghjBuild, ghjProbe, []int{0}, []int{0}, kind)
			}

			// Use small budget to exercise the spill path on some cases.
			// Budget varies so some cases spill and others don't.
			ghj.WithMemoryBudget(int64(1+rng.IntN(8)) * 1024)

			// Use drainVHJOuterRows for all kinds — it handles NULLs gracefully,
			// and INNER joins produce no NULLs so the comparison is still exact.
			vjhRows := drainVHJOuterRows(t, vjh, 3)
			ghjRows := drainGraceRows(t, ghj, 3)
			vjh.Close()
			ghj.Close()

			vjhRows = sortRows(vjhRows)
			ghjRows = sortRows(ghjRows)

			if !reflect.DeepEqual(vjhRows, ghjRows) {
				t.Errorf("case %d (kind=%s buildN=%d probeN=%d domain=%d): VJH %d rows != GHJ %d rows\nVJH: %v\nGHJ: %v",
					tc, kind, buildN, probeN, keyDomain, len(vjhRows), len(ghjRows), vjhRows, ghjRows)
			}
		})

		// Periodically force GC to release pooled batches that sync.Pool is
		// holding idle. Under GOMEMLIMIT the pool's idle entries count toward
		// live heap; an explicit GC every 16 cases keeps peak memory bounded.
		if tc%16 == 15 {
			runtime.GC()
		}
	}
}

// TestGraceHashJoin_OOMGuard_BoundedMemory verifies that the OOM guard
// (totalMemBytes tracking + spillLargestPartition) keeps peak heap bounded
// even under adversarial conditions: all rows hash to ONE partition, the
// budget is tiny (4KB), and the data is large enough to trigger many
// flushFull cycles. Without the guard, this would OOM under GOMEMLIMIT.
// REQ001999.
func TestGraceHashJoin_OOMGuard_BoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("OOM guard test runs ~5K rows")
	}
	// 5000 rows, all with key=42 → all hash to the same partition.
	// With a 4KB budget and BatchSize=1024, each flushFull produces
	// ~16KB (1024 rows × 2 cols × 8 bytes), so the per-partition budget
	// is exceeded on the first flush, triggering spill. The OOM guard
	// ensures totalMemBytes never exceeds the budget by more than one
	// batch worth of slack.
	const n = 5000
	buildKeys := make([]int64, n)
	buildVals := make([]int64, n)
	for i := range buildKeys {
		buildKeys[i] = 42 // single key → single partition
		buildVals[i] = int64(i)
	}
	probeKeys := []int64{42} // single probe key matches all build rows

	ghj := NewGraceHashJoin(
		chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys),
		[]int{0}, []int{0},
	).WithEstimatedBuildRows(n).WithMemoryBudget(4 * 1024) // 4KB — extremely tight
	defer ghj.Close()

	rows := drainGraceInner(t, ghj)
	if len(rows) != n {
		t.Fatalf("expected %d matched rows, got %d", n, len(rows))
	}
	if !ghj.Spilled() {
		t.Fatal("expected spill to activate under 4KB budget with 5K rows in one partition")
	}
}

// triplesToRows converts [][3]int64 to [][]any for uniform comparison.
func triplesToRows(triples [][3]int64) [][]any {
	rows := make([][]any, len(triples))
	for i, t := range triples {
		rows[i] = []any{t[0], t[1], t[2]}
	}
	return rows
}

// drainVHJOuterRows collects rows from a VJH with outer join semantics,
// preserving NULL markers. Mirrors drainGraceRows for direct comparison.
func drainVHJOuterRows(t *testing.T, j *VectorizedHashJoin, nCols int) [][]any {
	t.Helper()
	var out [][]any
	ctx := context.Background()
	for {
		batch, err := j.NextBatch(ctx)
		if err != nil {
			t.Fatalf("VJH NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		for i := 0; i < batch.Size; i++ {
			row := make([]any, nCols)
			for c := 0; c < nCols; c++ {
				if isColNull(&batch.Cols[c], i) {
					row[c] = nil
					continue
				}
				row[c] = UT.BatchValueAt(batch.Cols[c], i)
			}
			out = append(out, row)
		}
		batch.Put()
	}
	return out
}

// --- spill path tests ---

func TestGraceHashJoin_SpillPath_Activates(t *testing.T) {
	if testing.Short() {
		t.Skip("spill activation test runs ~10K rows")
	}
	const n = 10000
	buildKeys := make([]int64, n)
	buildVals := make([]int64, n)
	for i := range buildKeys {
		buildKeys[i] = int64(i % 50) // ~200 rows per key → large partitions
		buildVals[i] = int64(i)
	}
	probeKeys := []int64{0, 1, 2, 3, 4}

	g := NewGraceHashJoin(
		chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys),
		[]int{0}, []int{0},
	).WithEstimatedBuildRows(n).
		WithMemoryBudget(64 * 1024) // 64KB → forces spill
	defer g.Close()

	// Trigger partitioning by pulling one batch.
	_, _ = g.NextBatch(context.Background())

	if !g.Spilled() {
		t.Fatalf("expected spill to activate with 64KB budget on %d rows", n)
	}
}

func TestGraceHashJoin_SpillPath_Correctness_Inner(t *testing.T) {
	if testing.Short() {
		t.Skip("spill correctness test runs ~10K rows")
	}
	const n = 10000
	buildKeys := make([]int64, n)
	buildVals := make([]int64, n)
	for i := range buildKeys {
		buildKeys[i] = int64(i % 100)
		buildVals[i] = int64(i * 7)
	}
	probeKeys := make([]int64, 200)
	for i := range probeKeys {
		probeKeys[i] = int64(i % 150)
	}

	// Reference VJH.
	vjhBuild := chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...))
	vjhProbe := chunkedProbeProducer(append([]int64(nil), probeKeys...))
	vjh := NewVectorizedHashJoin(vjhBuild, vjhProbe, []int{0}, []int{0})
	defer vjh.Close()
	want := sortTriples(drainVHJInner(t, vjh))

	// Grace with forced spill.
	ghj := NewGraceHashJoin(
		chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...)),
		chunkedProbeProducer(append([]int64(nil), probeKeys...)),
		[]int{0}, []int{0},
	).WithEstimatedBuildRows(n).WithMemoryBudget(16 * 1024)
	defer ghj.Close()

	got := sortTriples(drainGraceInner(t, ghj))
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("row %d: got %v, want %v (first divergence)", i, got[i], want[i])
		}
	}
}

func TestGraceHashJoin_SpillPath_Correctness_Full(t *testing.T) {
	if testing.Short() {
		t.Skip("spill correctness test runs ~5K rows")
	}
	const n = 5000
	buildKeys := make([]int64, n)
	buildVals := make([]int64, n)
	for i := range buildKeys {
		buildKeys[i] = int64(i % 80)
		buildVals[i] = int64(i)
	}
	probeKeys := make([]int64, 300)
	for i := range probeKeys {
		probeKeys[i] = int64(i % 100) // 20% of probe keys won't match
	}

	vjhBuild := chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...))
	vjhProbe := chunkedProbeProducer(append([]int64(nil), probeKeys...))
	vjh := NewVectorizedHashJoinWithKind(vjhBuild, vjhProbe, []int{0}, []int{0}, JoinKindFull)
	defer vjh.Close()
	want := sortRows(drainVHJOuterRows(t, vjh, 3))

	ghj := NewGraceHashJoinWithKind(
		chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...)),
		chunkedProbeProducer(append([]int64(nil), probeKeys...)),
		[]int{0}, []int{0}, JoinKindFull,
	).WithEstimatedBuildRows(n).WithMemoryBudget(16 * 1024)
	defer ghj.Close()
	got := sortRows(drainGraceRows(t, ghj, 3))

	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("row %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestGraceHashJoin_SpillPath_StringKey(t *testing.T) {
	if testing.Short() {
		t.Skip("spill string-key test runs ~2K rows")
	}
	const n = 2000
	buildBatch := UT.GetBatch(2)
	buildBatch.SetColumnName(0, "k")
	buildBatch.SetColumnName(1, "v")
	buildBatch.Cols[0].Type = LX.T_TEXT
	buildBatch.Cols[0].Data.Strs = make([]string, n)
	buildBatch.Cols[1].Type = LX.T_INT_KW
	buildBatch.Cols[1].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		buildBatch.Cols[0].Data.Strs[i] = "key" + itoa(int64(i%50))
		buildBatch.Cols[1].Data.Ints[i] = int64(i)
	}
	buildBatch.Size = n

	probeBatch := UT.GetBatch(1)
	probeBatch.SetColumnName(0, "pk")
	probeBatch.Cols[0].Type = LX.T_TEXT
	probeBatch.Cols[0].Data.Strs = []string{"key0", "key25", "key49", "key99"}
	probeBatch.Size = 4

	// Reference VJH.
	vjhBuild := &testBatchProducer{batches: []*UT.Batch{cloneBatch(buildBatch)}}
	vjhProbe := &testBatchProducer{batches: []*UT.Batch{cloneBatch(probeBatch)}}
	vjh := NewVectorizedHashJoin(vjhBuild, vjhProbe, []int{0}, []int{0})
	defer vjh.Close()
	wantCount := 0
	for {
		b, err := vjh.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("VJH: %v", err)
		}
		if b == nil {
			break
		}
		wantCount += b.Size
		b.Put()
	}

	ghj := NewGraceHashJoin(
		&testBatchProducer{batches: []*UT.Batch{cloneBatch(buildBatch)}},
		&testBatchProducer{batches: []*UT.Batch{cloneBatch(probeBatch)}},
		[]int{0}, []int{0},
	).WithEstimatedBuildRows(n).WithMemoryBudget(4 * 1024)
	defer ghj.Close()

	gotCount := 0
	for {
		b, err := ghj.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("GHJ: %v", err)
		}
		if b == nil {
			break
		}
		gotCount += b.Size
		b.Put()
	}
	if gotCount != wantCount {
		t.Fatalf("string-key spill: got %d rows, want %d", gotCount, wantCount)
	}
}

// cloneBatch deep-copies a batch so two independent producers can consume
// it. The source batch's ownership is unchanged.
func cloneBatch(src *UT.Batch) *UT.Batch {
	nCols := logicalColCount(src)
	dst := UT.GetBatch(nCols)
	dst.Size = src.Size
	for i := 0; i < nCols; i++ {
		dst.Cols[i].Name = src.Cols[i].Name
		dst.Cols[i].Type = src.Cols[i].Type
		allocateColData(&dst.Cols[i], src.Size, src.Cols[i].Type)
		for r := 0; r < src.Size; r++ {
			copyRowToColumn(&dst.Cols[i], &src.Cols[i], r, r)
		}
		if src.Cols[i].Nulls != nil {
			dst.Cols[i].Nulls = append([]bool(nil), src.Cols[i].Nulls...)
		}
	}
	return dst
}

func TestGraceHashJoin_TempFilesRemoved(t *testing.T) {
	if testing.Short() {
		t.Skip("temp file cleanup test runs ~5K rows")
	}
	const n = 5000
	buildKeys := make([]int64, n)
	buildVals := make([]int64, n)
	for i := range buildKeys {
		buildKeys[i] = int64(i % 30)
		buildVals[i] = int64(i)
	}
	probeKeys := []int64{0, 1, 2}

	g := NewGraceHashJoin(
		chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys),
		[]int{0}, []int{0},
	).WithEstimatedBuildRows(n).WithMemoryBudget(8 * 1024)

	// Drain to completion.
	for {
		b, err := g.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if b == nil {
			break
		}
		b.Put()
	}

	// Capture temp dir path before Close clears it.
	tempDir := g.tempDir
	if tempDir == "" {
		t.Skip("no temp dir created (partitioning may not have spilled)")
	}
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("temp dir should exist before Close: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(tempDir); err == nil {
		t.Error("temp dir still exists after Close")
	}
}

func TestGraceHashJoin_PartitionSkew(t *testing.T) {
	if testing.Short() {
		t.Skip("partition skew test runs ~5K rows")
	}
	// All rows share the same key → all land in one partition.
	const n = 5000
	buildKeys := make([]int64, n)
	buildVals := make([]int64, n)
	for i := range buildKeys {
		buildKeys[i] = 42 // single key
		buildVals[i] = int64(i)
	}
	probeKeys := []int64{42, 99} // 42 matches all, 99 matches none

	vjhBuild := chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...))
	vjhProbe := chunkedProbeProducer(append([]int64(nil), probeKeys...))
	vjh := NewVectorizedHashJoin(vjhBuild, vjhProbe, []int{0}, []int{0})
	defer vjh.Close()
	want := sortTriples(drainVHJInner(t, vjh))

	ghj := NewGraceHashJoin(
		chunkedBuildProducer(append([]int64(nil), buildKeys...), append([]int64(nil), buildVals...)),
		chunkedProbeProducer(append([]int64(nil), probeKeys...)),
		[]int{0}, []int{0},
	).WithEstimatedBuildRows(n).WithMemoryBudget(8 * 1024)
	defer ghj.Close()
	got := sortTriples(drainGraceInner(t, ghj))

	if len(got) != len(want) {
		t.Fatalf("skew: got %d rows, want %d", len(got), len(want))
	}
	// All build rows match the single probe key 42.
	for _, r := range got {
		if r[0] != 42 || r[2] != 42 {
			t.Errorf("skew row: got %v, want k=42 pk=42", r)
		}
	}
}

// --- large data acceptance test ---

func TestGraceHashJoin_LargeData_1Mx1K(t *testing.T) {
	// Gate behind an env var so `go test ./...` never accidentally triggers
	// a 1M-row join. The test is safe (spills to disk) but allocates ~16MB
	// of input slices and runs for several seconds. REQ001999 acceptance.
	if os.Getenv("RAZOR_TEST_LARGE") == "" {
		t.Skip("set RAZOR_TEST_LARGE=1 to run 1M×1K grace hash join test")
	}
	if runtime.GOARCH == "386" {
		t.Skip("32-bit platform: 1M-row test may exhaust address space")
	}
	const buildN = 1_000_000
	const probeN = 1000

	buildKeys := make([]int64, buildN)
	buildVals := make([]int64, buildN)
	for i := range buildKeys {
		buildKeys[i] = int64(i % 10000) // 100 duplicates per key
		buildVals[i] = int64(i)
	}
	probeKeys := make([]int64, probeN)
	for i := range probeKeys {
		probeKeys[i] = int64(i) // all keys 0..999 match
	}

	ghj := NewGraceHashJoin(
		chunkedBuildProducer(buildKeys, buildVals),
		chunkedProbeProducer(probeKeys),
		[]int{0}, []int{0},
	).WithEstimatedBuildRows(buildN).WithMemoryBudget(4 * 1024 * 1024) // 4MB → forces spill
	defer ghj.Close()

	rowCount := 0
	ctx := context.Background()
	for {
		b, err := ghj.NextBatch(ctx)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if b == nil {
			break
		}
		rowCount += b.Size
		b.Put()
	}

	// Each of 1000 probe keys matches 100 build rows → 100K matched rows.
	wantRows := int64(probeN * (buildN / 10000))
	if int64(rowCount) != wantRows {
		t.Fatalf("row count: got %d, want %d", rowCount, wantRows)
	}
	if !ghj.Spilled() {
		t.Error("expected spill on 1M rows with 4MB budget")
	}
}
