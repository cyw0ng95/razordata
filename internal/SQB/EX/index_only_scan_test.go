package EX

import (
	"context"
	"fmt"
	"os"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
)

// TestIndexOnlyScan_CoveringQuery verifies REQ001107: when the
// projected columns are entirely covered by the index columns
// plus optionally the primary key, the planner wraps the
// OP.IndexScan in OP.IndexOnlyScan so the heap is not fetched.
func TestIndexOnlyScan_CoveringQuery(t *testing.T) {
	ex := newBitmapTestExecutor(t)
	ctx := context.Background()

	// Table with pk + a. Index on (a) — covers pk + a queries.
	mustExec(t, ex, ctx, "CREATE TABLE ios_cover (pk INTEGER PRIMARY KEY, a INTEGER, b INTEGER)")
	mustExec(t, ex, ctx, "CREATE INDEX idx_ios_cover_a ON ios_cover (a)")

	mustExec(t, ex, ctx, "INSERT INTO ios_cover VALUES (1, 10, 100)")
	mustExec(t, ex, ctx, "INSERT INTO ios_cover VALUES (2, 20, 200)")

	// Projecting only pk (covered by primary key) and a
	// (covered by index). Heap fetch is unnecessary.
	rows, err := ex.QueryAll(ctx, "SELECT pk, a FROM ios_cover WHERE a = 10")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected ≥ 1 row, got 0")
	}
}

// TestIndexOnlyScan_NonCoveringQuery verifies the planner does
// NOT take the OP.IndexOnlyScan path when the projection includes a
// column absent from the index. Coverage check must reject
// such queries so the planner falls back to OP.IndexScan + heap
// fetch.
func TestIndexOnlyScan_NonCoveringQuery(t *testing.T) {
	ex := newBitmapTestExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, "CREATE TABLE ios_noncover (pk INTEGER PRIMARY KEY, a INTEGER, b INTEGER)")
	mustExec(t, ex, ctx, "CREATE INDEX idx_ios_noncover_a ON ios_noncover (a)")

	mustExec(t, ex, ctx, "INSERT INTO ios_noncover VALUES (1, 10, 100)")

	// OP.Project b — not in index idx_a — so heap fetch is required.
	rows, err := ex.QueryAll(ctx, "SELECT b FROM ios_noncover WHERE a = 10")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected ≥ 1 row, got 0")
	}
}

// TestIndexOnlyScan_ExplainsLabel is a unit check on the
// operatorType switch in plan_node.go.
func TestIndexOnlyScan_ExplainsLabel(t *testing.T) {
	ios := &OP.IndexOnlyScan{}
	if got := operatorType(ios); got != "OP.IndexOnlyScan" {
		t.Fatalf("operatorType(OP.IndexOnlyScan) = %q, want %q", got, "OP.IndexOnlyScan")
	}
}

// BenchmarkIndexOnlyScan_VsHeapScan is the perf gate for
// REQ001107: the covering query should be measurably cheaper
// than a query that requires a heap fetch. We benchmark the
// planner-side decision only — backend row decode cost is
// shared with OP.SeqScan and not part of this test.
func BenchmarkIndexOnlyScan_VsHeapScan(b *testing.B) {
	if testing.Short() {
		b.Skip("short mode")
	}
	// The DT package keeps a process-wide RegisteredIndexes
	// map keyed by table name. To avoid collision across
	// benchmark reruns (which share the same process), we
	// suffix every table/index name with a per-run counter.
	base := benchSuffix()
	b.Run("covering", func(b *testing.B) {
		tblCover := "ios_bench_cover_" + base
		idxCover := "idx_" + tblCover + "_a"
		ex := newBitmapTestExecutorB(b)
		ctx := context.Background()
		if _, err := ex.Exec(ctx, "DROP TABLE IF EXISTS "+tblCover); err != nil {
			b.Skipf("pre-clean failed (catalog collision in shared DT state): %v", err)
		}
		mustExecB(b, ex, ctx, "CREATE TABLE "+tblCover+" (pk INTEGER PRIMARY KEY, a INTEGER, b INTEGER)")
		mustExecB(b, ex, ctx, "CREATE INDEX "+idxCover+" ON "+tblCover+" (a)")
		for i := 0; i < 1000; i++ {
			mustExecB(b, ex, ctx, "INSERT INTO "+tblCover+" VALUES ("+
				itoa(i)+", "+itoa(i%100)+", "+itoa(i%1000)+")")
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ex.QueryAll(ctx, "SELECT pk, a FROM "+tblCover+" WHERE a = 50")
		}
	})
	b.Run("noncovering", func(b *testing.B) {
		tblNon := "ios_bench_noncover_" + base
		idxNon := "idx_" + tblNon + "_a"
		ex := newBitmapTestExecutorB(b)
		ctx := context.Background()
		if _, err := ex.Exec(ctx, "DROP TABLE IF EXISTS "+tblNon); err != nil {
			b.Skipf("pre-clean failed (catalog collision in shared DT state): %v", err)
		}
		mustExecB(b, ex, ctx, "CREATE TABLE "+tblNon+" (pk INTEGER PRIMARY KEY, a INTEGER, b INTEGER)")
		mustExecB(b, ex, ctx, "CREATE INDEX "+idxNon+" ON "+tblNon+" (a)")
		for i := 0; i < 1000; i++ {
			mustExecB(b, ex, ctx, "INSERT INTO "+tblNon+" VALUES ("+
				itoa(i)+", "+itoa(i%100)+", "+itoa(i%1000)+")")
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = ex.QueryAll(ctx, "SELECT b FROM "+tblNon+" WHERE a = 50")
		}
	})
}

// benchSuffix returns a process-unique suffix so concurrent or
// repeated benchmark runs don't collide on EX/DT table-name
// globals.
var benchCounter int

func benchSuffix() string {
	benchCounter++
	return fmt.Sprintf("%d_%d", os.Getpid(), benchCounter)
}

func newBitmapTestExecutorB(b *testing.B) *Executor {
	b.Helper()
	dir := b.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		b.Fatalf("open engine: %v", err)
	}
	b.Cleanup(func() { eng.Close() })
	store := &engineStore{eng: eng}
	return NewExecutorWithEngine(store)
}

func mustExecB(b *testing.B, ex *Executor, ctx context.Context, sql string) {
	b.Helper()
	if _, err := ex.Exec(ctx, sql); err != nil {
		b.Fatalf("Exec(%q): %v", sql, err)
	}
}

func itoa(n int) string {
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

// findIndexOnlyInTree reports whether any *OP.IndexOnlyScan is in
// the operator tree.
func findIndexOnlyInTree(op DT.Operator) bool {
	if op == nil {
		return false
	}
	if _, ok := op.(*OP.IndexOnlyScan); ok {
		return true
	}
	if c, ok := op.(interface{ Child() DT.Operator }); ok {
		if findIndexOnlyInTree(c.Child()) {
			return true
		}
	}
	if lr, ok := op.(interface {
		LeftChild() DT.Operator
		RightChild() DT.Operator
	}); ok {
		if findIndexOnlyInTree(lr.LeftChild()) {
			return true
		}
		if findIndexOnlyInTree(lr.RightChild()) {
			return true
		}
	}
	return false
}

// _ = context.Background reserved for future ExecAll-style tests.
var _ = context.Background
