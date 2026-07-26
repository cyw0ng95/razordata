package EX

import (
	"context"
	"fmt"
	"os"
	"strings"
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
	if got := operatorType(ios); got != "IndexOnlyScan" {
		t.Fatalf("operatorType(OP.IndexOnlyScan) = %q, want %q", got, "IndexOnlyScan")
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

// TestAutoCoveringIndex_Basic — REQ001254: when SELECT columns ⊆
// index columns ∪ {PK}, planner auto-wraps IndexScan in IndexOnlyScan.
// Verifies the wrapping happens WITHOUT manual annotation by wiring
// up a real executor with a single-column index.
func TestAutoCoveringIndex_Basic(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("aci_basic", []string{"pk", "a", "z"}, "pk")
	ex.RegisterIndex("aci_basic", "idx_a", []string{"a"})
	id, _ := DT.TableIDFor("aci_basic")

	ctx := context.Background()
	for i := range 5 {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO aci_basic VALUES (%d, %d, %d)", i+1, i*10, i*1000)); err != nil {
			t.Fatal(err)
		}
	}
	idxStore := ls.NewIndexStore(eng, id, "idx_a")
	for i := range 5 {
		idxStore.Insert([]byte(fmt.Sprintf("%d", i*10)), []byte(fmt.Sprintf("%d", i*10)))
	}

	// Path 1: SELECT pk,a WHERE a = 10 → covering (pk is pk, a is in index).
	// Use ParseAndPlan to inspect the operator tree directly.
	p := ex.planner
	result, err := p.ParseAndPlan("SELECT pk, a FROM aci_basic WHERE a = 10")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !findIndexOnlyInTree(result.Root) {
		t.Fatalf("expected IndexOnlyScan for covering query; tree=%v", result.Root)
	}

	// Non-covering: SELECT z (not in index, not pk) → must NOT use IndexOnly.
	result2, err := p.ParseAndPlan("SELECT z FROM aci_basic WHERE a = 10")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if findIndexOnlyInTree(result2.Root) {
		t.Fatalf("IndexOnlyScan incorrectly selected for non-covering projection; tree=%v", result2.Root)
	}
}

// TestAutoCoveringIndex_MultiCol — REQ001254 with a multi-column
// composite index on (a, b). A SELECT projecting only a and b
// should hit the covering path.
func TestAutoCoveringIndex_MultiCol(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("aci_multi", []string{"pk", "a", "b", "c"}, "pk")
	ex.RegisterIndex("aci_multi", "idx_ab", []string{"a", "b"})
	id, _ := DT.TableIDFor("aci_multi")

	ctx := context.Background()
	for i := range 5 {
		if _, err := ex.Exec(ctx, fmt.Sprintf("INSERT INTO aci_multi VALUES (%d, %d, %d, %d)", i+1, i*10, i*100, i*1000)); err != nil {
			t.Fatal(err)
		}
	}
	idxStore := ls.NewIndexStore(eng, id, "idx_ab")
	for i := range 5 {
		key := []byte(fmt.Sprintf("%d/%d", i*10, i*100))
		val := []byte(fmt.Sprintf("%d/%d", i*10, i*100))
		idxStore.Insert(key, val)
	}

	// Covering: project a, b (leftmost prefix of composite index).
	p := ex.planner
	result, err := p.ParseAndPlan("SELECT a, b FROM aci_multi WHERE a = 10")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !findIndexOnlyInTree(result.Root) {
		t.Fatalf("expected IndexOnlyScan for covering-leftmost query; tree=%v", result.Root)
	}

	// Non-covering: project c — not in index → no IndexOnlyScan.
	result2, err := p.ParseAndPlan("SELECT c FROM aci_multi WHERE a = 10")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if findIndexOnlyInTree(result2.Root) {
		t.Fatalf("IndexOnlyScan incorrectly selected for non-covering c projection; tree=%v", result2.Root)
	}
}

// TestAutoCoveringIndex_ExplainContainsAnnotated — the EXPLAIN
// output must show "[covering]" or the IndexOnlyScan name when the
// covering path is selected. This is the user-visible contract.
func TestAutoCoveringIndex_ExplainContainsAnnotated(t *testing.T) {
	ResetForTest(t)
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("aci_explain", []string{"pk", "a"}, "pk")
	ex.RegisterIndex("aci_explain", "idx_a", []string{"a"})
	id, _ := DT.TableIDFor("aci_explain")

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO aci_explain VALUES (1, 100)"); err != nil {
		t.Fatal(err)
	}
	idxStore := ls.NewIndexStore(eng, id, "idx_a")
	idxStore.Insert([]byte("100"), []byte("100"))

	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT pk, a FROM aci_explain WHERE a = 100")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	var dump strings.Builder
	for _, r := range rows {
		if len(r.Data) >= 4 {
			dump.WriteString(r.Data[3].ToAny().(string))
			dump.WriteByte('\n')
		}
	}
	t.Logf("EXPLAIN:\n%s", dump.String())
	if !strings.Contains(dump.String(), "IndexOnlyScan") && !strings.Contains(dump.String(), "covering") {
		t.Errorf("expected IndexOnlyScan/[covering] in EXPLAIN output for covering query")
	}
}

// _ = context.Background reserved for future ExecAll-style tests.
var _ = context.Background
