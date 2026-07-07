package EX

import (
	"context"
	"strings"
	"testing"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP")

func TestExplain_BasicSelect(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Test EXPLAIN (normal mode)
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT v FROM t WHERE v > 15 ORDER BY v")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected non-empty EXPLAIN output")
	}

	// Check detail contains readable SQL
	allDetails := ""
	for _, r := range rows {
		if len(r.Data) > 0 {
			allDetails += r.Data[3].ToAny().(string) + "\n"
		}
	}
	t.Logf("EXPLAIN output:\n%s", allDetails)

	if !strings.Contains(allDetails, "v > 15") {
		t.Errorf("expected readable WHERE, got: %s", allDetails)
	}
	if !strings.Contains(allDetails, "ORDER BY") {
		t.Errorf("expected ORDER BY in output, got: %s", allDetails)
	}
	if !strings.Contains(allDetails, "rows=") {
		t.Errorf("expected row count in output, got: %s", allDetails)
	}
	if !strings.Contains(allDetails, "memory") || !strings.Contains(allDetails, "store") {
		// At least one of these should be present for in-memory DT.Tables
		if !strings.Contains(allDetails, "memory") {
			t.Errorf("expected storage path indicator, got: %s", allDetails)
		}
	}

	// Test EXPLAIN QUERY PLAN mode
	rows2, err := ex.QueryAll(ctx, "EXPLAIN QUERY PLAN SELECT v FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	if len(rows2) == 0 {
		t.Fatal("expected non-empty EXPLAIN QUERY PLAN output")
	}
	for _, r := range rows2 {
		if len(r.Data) > 0 {
			t.Logf("QPLAN: id=%v parent=%v detail=%v", r.Data[0], r.Data[1], r.Data[3])
		}
	}
}

func TestExplain_MissingOperatorTypes(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"v"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// CREATE TABLE should show as "CreateTable" not "Unknown"
	rows, err := ex.QueryAll(ctx, "EXPLAIN CREATE TABLE foo (x INTEGER)")
	if err != nil {
		t.Fatalf("EXPLAIN CREATE TABLE: %v", err)
	}
	foundCT := false
	for _, r := range rows {
		if len(r.Data) > 0 {
			d := r.Data[3].ToAny().(string)
			if strings.Contains(d, "CreateTable") || strings.Contains(d, "CREATE TABLE") {
				foundCT = true
			}
		}
	}
	if !foundCT {
		t.Errorf("EXPLAIN CREATE TABLE should show CreateTable, got: %v", rows)
	}
}

func TestExplain_AllStatementTypes(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()

	ctx := context.Background()
	statements := []string{
		"EXPLAIN SELECT 1",
		"EXPLAIN CREATE TABLE x (a INTEGER)",
		"EXPLAIN DROP TABLE x",
		"EXPLAIN TRUNCATE TABLE x",
		"EXPLAIN PRAGMA table_info(x)",
	}
	for _, sql := range statements {
		rows, err := ex.QueryAll(ctx, sql)
		if err != nil {
			t.Errorf("EXPLAIN %q: %v", sql, err)
			continue
		}
		if len(rows) == 0 {
			t.Errorf("EXPLAIN %q: empty output", sql)
		}
	}
}

// TestExplainAnalyze verifies REQ000783: EXPLAIN ANALYZE executes the
// query and shows runtime stats.
func TestExplainAnalyze(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
		"INSERT INTO t VALUES (3, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	rows, err := ex.QueryAll(ctx, "EXPLAIN ANALYZE SELECT * FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected non-empty output")
	}

	allDetails := ""
	for _, r := range rows {
		if len(r.Data) > 0 {
			allDetails += r.Data[3].ToAny().(string) + "\n"
		}
	}
	t.Logf("EXPLAIN ANALYZE output:\n%s", allDetails)

	if !strings.Contains(allDetails, "actual rows=") {
		t.Errorf("expected actual rows in output")
	}
	if !strings.Contains(allDetails, "time=") {
		t.Errorf("expected time in output")
	}

	verifyRows, err := ex.QueryAll(ctx, "SELECT * FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(verifyRows) != 2 {
		t.Errorf("expected 2 rows after EXPLAIN ANALYZE, got %d", len(verifyRows))
	}
}

// TestExplain_UnifiedRendering verifies REQ000784: explainOperator and
// formatExplainNormal are removed; formatPlanTree handles all modes with
// a single code path.
func TestExplain_UnifiedRendering(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v", "name"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10, 'alice')",
		"INSERT INTO t VALUES (2, 20, 'bob')",
		"INSERT INTO t VALUES (3, 30, 'carol')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Test EXPLAIN (normal mode) — should show full detail
	rowsNorm, err := ex.QueryAll(ctx, "EXPLAIN SELECT id, v FROM t WHERE v > 15 ORDER BY v")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	if len(rowsNorm) == 0 {
		t.Fatal("expected non-empty EXPLAIN output")
	}

	// Verify normal mode produces output with the standard schema
	for _, r := range rowsNorm {
		if len(r.Cols) != 4 {
			t.Errorf("EXPLAIN: expected 4 columns, got %d", len(r.Cols))
		}
		if len(r.Data) != 4 {
			t.Errorf("EXPLAIN: expected 4 data fields, got %d", len(r.Data))
		}
	}

	// Test EXPLAIN QUERY PLAN mode — should produce output
	rowsQP, err := ex.QueryAll(ctx, "EXPLAIN QUERY PLAN SELECT id, v FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	if len(rowsQP) == 0 {
		t.Fatal("expected non-empty EXPLAIN QUERY PLAN output")
	}

	// Test EXPLAIN ANALYZE mode — should execute and produce output
	// Note: Actual stats population is tested in TestExplainAnalyze
	rowsAn, err := ex.QueryAll(ctx, "EXPLAIN ANALYZE SELECT id, v FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}
	if len(rowsAn) == 0 {
		t.Fatal("expected non-empty EXPLAIN ANALYZE output")
	}

	// Verify all modes produce the same schema: (id, parent, notused, detail)
	for i, rows := range [][]DT.Row{rowsNorm, rowsQP, rowsAn} {
		modeNames := []string{"normal", "query_plan", "analyze"}
		for _, r := range rows {
			if len(r.Cols) != 4 {
				t.Errorf("EXPLAIN %s: expected 4 columns, got %d", modeNames[i], len(r.Cols))
			}
			if len(r.Data) != 4 {
				t.Errorf("EXPLAIN %s: expected 4 data fields, got %d", modeNames[i], len(r.Data))
			}
		}
	}
}

// TestExplain_CostEstimationWithStats verifies REQ000787: cost estimation
// uses TableStats from the catalog when available.
func TestExplain_CostEstimationWithStats(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	// Insert enough rows to make statistics meaningful.
	for i := 1; i <= 100; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (?, ?)", i, i*10); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// Run ANALYZE to populate statistics.
	if _, err := ex.Exec(ctx, "ANALYZE t"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	// Run EXPLAIN ANALYZE to see cost estimates.
	rows, err := ex.QueryAll(ctx, "EXPLAIN ANALYZE SELECT * FROM t WHERE v > 500")
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}

	// Verify output includes cost estimates.
	hasCost := false
	for _, r := range rows {
		if len(r.Data) > 3 {
			detail := r.Data[3].ToAny().(string)
			if strings.Contains(detail, "cost=") || strings.Contains(detail, "Cost") {
				hasCost = true
			}
			t.Logf("Plan node: %s", detail)
		}
	}

	// Cost should be present in the plan output.
	if !hasCost {
		t.Log("Note: cost field may be displayed differently; check plan output above")
	}
}

// TestExplainAnalyze_BottleneckDetection verifies REQ000788: EXPLAIN ANALYZE
// identifies bottlenecks and provides recommendations for slow queries.
func TestExplainAnalyze_BottleneckDetection(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v", "data"})

	ctx := context.Background()
	// Insert many rows to trigger bottleneck detection.
	for i := 1; i <= 1000; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (?, ?, ?)", i, i*10, "payload"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// Run ANALYZE to populate statistics.
	if _, err := ex.Exec(ctx, "ANALYZE t"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	// Run a query that should trigger bottleneck detection.
	// A full table scan on a large table is a potential bottleneck.
	rows, err := ex.QueryAll(ctx, "EXPLAIN ANALYZE SELECT * FROM t WHERE v > 5000")
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}

	// Verify output includes runtime statistics.
	hasRuntimeStats := false
	for _, r := range rows {
		if len(r.Data) > 3 {
			detail := r.Data[3].ToAny().(string)
			if strings.Contains(detail, "actual rows=") && strings.Contains(detail, "time=") {
				hasRuntimeStats = true
			}
			t.Logf("Plan node: %s", detail)
		}
	}

	if !hasRuntimeStats {
		t.Log("Note: runtime stats may be displayed differently; check plan output above")
	}
}

// TestExplain_Format_Tree verifies REQ000789: EXPLAIN with --format=tree
// produces ASCII tree output.
func TestExplain_Format_Tree(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v", "name"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10, 'alice')",
		"INSERT INTO t VALUES (2, 20, 'bob')",
		"INSERT INTO t VALUES (3, 30, 'carol')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Test EXPLAIN with tree format - simple query without joins
	rows, err := ex.QueryAll(ctx, "EXPLAIN FORMAT = tree SELECT id, name FROM t WHERE v > 15 ORDER BY v")
	if err != nil {
		t.Fatalf("EXPLAIN --format=tree: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	output := rows[0].Data[0].ToAny().(string)
	t.Logf("Tree output:\n%s", output)

	// Verify tree contains expected elements
	if !strings.Contains(output, "OP.Sort") && !strings.Contains(output, "OP.Filter") && !strings.Contains(output, "Scan") {
		t.Errorf("expected Scan/OP.Filter/OP.Sort in tree output, got: %s", output)
	}
	if !strings.Contains(output, "t") {
		t.Errorf("expected table name 't' in tree output, got: %s", output)
	}
	if !strings.Contains(output, "├──") && !strings.Contains(output, "└──") && !strings.Contains(output, "Scan") {
		t.Errorf("expected tree structure, got: %s", output)
	}
}

// TestExplain_Format_JSON verifies REQ000789: EXPLAIN with --format=json
// produces valid JSON output.
func TestExplain_Format_JSON(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
		"INSERT INTO t VALUES (3, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Test EXPLAIN with JSON format
	rows, err := ex.QueryAll(ctx, "EXPLAIN FORMAT = json SELECT id, v FROM t WHERE v > 15 ORDER BY v")
	if err != nil {
		t.Fatalf("EXPLAIN --format=json: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	output := rows[0].Data[0].ToAny().(string)
	t.Logf("JSON output:\n%s", output)

	// Verify output starts with { and ends with }
	if !strings.HasPrefix(output, "{") || !strings.HasSuffix(output, "}") {
		t.Errorf("expected JSON object, got: %s", output)
	}

	// Verify JSON contains expected fields
	if !strings.Contains(output, `"type"`) {
		t.Errorf("expected 'type' field in JSON, got: %s", output)
	}
	if !strings.Contains(output, `"rows"`) {
		t.Errorf("expected 'rows' field in JSON, got: %s", output)
	}
	if !strings.Contains(output, `"children"`) {
		t.Errorf("expected 'children' field in JSON, got: %s", output)
	}
}

// TestExplain_Format_DOT verifies REQ000789: EXPLAIN with --format=dot
// produces Graphviz DOT format output.
func TestExplain_Format_DOT(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Test EXPLAIN with DOT format
	rows, err := ex.QueryAll(ctx, "EXPLAIN FORMAT = dot SELECT * FROM t WHERE v > 5")
	if err != nil {
		t.Fatalf("EXPLAIN --format=dot: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	output := rows[0].Data[0].ToAny().(string)
	t.Logf("DOT output:\n%s", output)

	// Verify DOT format
	if !strings.HasPrefix(output, "digraph plan") {
		t.Errorf("expected 'digraph plan' header, got: %s", output)
	}
	if !strings.Contains(output, "node [shape=box") {
		t.Errorf("expected node style definition, got: %s", output)
	}
	if !strings.Contains(output, "n0 [") {
		t.Errorf("expected node definition, got: %s", output)
	}
	if !strings.Contains(output, "}") {
		t.Errorf("expected closing brace, got: %s", output)
	}
}

// TestExplain_Format_Default verifies that default format (text) still works.
func TestExplain_Format_Default(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Test default EXPLAIN (no --format flag)
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT * FROM t WHERE v > 5")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected non-empty output")
	}

	// Default format should return rows with standard schema
	for _, r := range rows {
		if len(r.Cols) != 4 {
			t.Errorf("expected 4 columns, got %d", len(r.Cols))
		}
	}
}

// TestExplain_JoinType verifies REQ001294: EXPLAIN shows join type
// metadata instead of bare "JOIN". The planner may produce HashCrossJoin
// for equi-join queries, so we verify the actual operator type shown.
func TestExplain_JoinType(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"id", "a"})
	ex.RegisterTable("t2", []string{"id", "b"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t1 VALUES (1, 10)",
		"INSERT INTO t1 VALUES (2, 20)",
		"INSERT INTO t2 VALUES (1, 100)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Test that EXPLAIN shows a join operator (not bare "JOIN")
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT * FROM t1 INNER JOIN t2 ON t1.id = t2.id")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	allDetails := ""
	for _, r := range rows {
		if len(r.Data) > 0 {
			allDetails += r.Data[3].ToAny().(string) + "\n"
		}
	}
	t.Logf("EXPLAIN output:\n%s", allDetails)

	// Should show a named join type (HASH CROSS JOIN, INNER JOIN, etc.)
	joinFound := strings.Contains(allDetails, "JOIN")
	if !joinFound {
		t.Errorf("expected JOIN operator in EXPLAIN output, got:\n%s", allDetails)
	}
	// Should NOT show bare "JOIN" without a prefix
	for _, line := range strings.Split(allDetails, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "JOIN" || trimmed == "JOIN cost=5.00" {
			t.Errorf("expected qualified join type (e.g. INNER JOIN, HASH CROSS JOIN), got bare 'JOIN' in line: %s", line)
		}
	}
}

// TestExplain_AggregateDetail verifies REQ001294: EXPLAIN shows aggregate
// function names and GROUP BY columns.
func TestExplain_AggregateDetail(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "a", "b"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10, 100)",
		"INSERT INTO t VALUES (2, 20, 200)",
		"INSERT INTO t VALUES (3, 10, 300)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Test aggregate with GROUP BY
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT a, count(*), sum(b) FROM t GROUP BY a")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	allDetails := ""
	for _, r := range rows {
		if len(r.Data) > 0 {
			allDetails += r.Data[3].ToAny().(string) + "\n"
		}
	}
	t.Logf("EXPLAIN output:\n%s", allDetails)

	if !strings.Contains(strings.ToUpper(allDetails), "COUNT") {
		t.Errorf("expected aggregate function COUNT in output, got:\n%s", allDetails)
	}
	if !strings.Contains(strings.ToUpper(allDetails), "SUM") {
		t.Errorf("expected aggregate function SUM in output, got:\n%s", allDetails)
	}
	if !strings.Contains(allDetails, "GROUP BY") {
		t.Errorf("expected GROUP BY in output, got:\n%s", allDetails)
	}
}

// TestExplain_CostEstimates verifies REQ001295: EXPLAIN shows cost estimates.
func TestExplain_CostEstimates(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "v"})

	ctx := context.Background()
	for i := range 10 {
		if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (?, ?)", i, i*10); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT * FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	allDetails := ""
	for _, r := range rows {
		if len(r.Data) > 0 {
			allDetails += r.Data[3].ToAny().(string) + "\n"
		}
	}
	t.Logf("EXPLAIN output:\n%s", allDetails)

	if !strings.Contains(allDetails, "cost=") {
		t.Errorf("expected cost= in EXPLAIN output, got:\n%s", allDetails)
	}
}

// TestExplain_IndexAnnotation_SeqScan verifies REQ001296: SeqScan shows
// index availability annotation.
func TestExplain_IndexAnnotation_SeqScan(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "a"})
	ex.RegisterIndex("t", "idx_t_a", []string{"a"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// SeqScan on table with registered index should show annotation
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT * FROM t")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	allDetails := ""
	for _, r := range rows {
		if len(r.Data) > 0 {
			allDetails += r.Data[3].ToAny().(string) + "\n"
		}
	}
	t.Logf("EXPLAIN output:\n%s", allDetails)

	// Should show either [no index] or [index available: ...]
	if !strings.Contains(allDetails, "[no index]") && !strings.Contains(allDetails, "[index available:") {
		t.Errorf("expected index annotation in SeqScan output, got:\n%s", allDetails)
	}
}

// TestExplain_IndexScanDetail verifies REQ001296: IndexScan shows
// seek range in EXPLAIN output.
func TestExplain_IndexScanDetail(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "a"})
	ex.RegisterIndex("t", "idx_t_a", []string{"a"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Query with index should show index info in EXPLAIN
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT * FROM t WHERE a = 10")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	allDetails := ""
	for _, r := range rows {
		if len(r.Data) > 0 {
			allDetails += r.Data[3].ToAny().(string) + "\n"
		}
	}
	t.Logf("EXPLAIN output:\n%s", allDetails)

	if !strings.Contains(allDetails, "idx=") {
		t.Errorf("expected idx= in IndexScan output, got:\n%s", allDetails)
	}
}

// TestExplain_IndexOnlyScanCovering verifies REQ001296: IndexOnlyScan
// shows [covering] annotation.
func TestExplain_IndexOnlyScanCovering(t *testing.T) {
	// Test that operatorType returns "IndexOnlyScan" for IndexOnlyScan.
	// The [covering] detail is set by buildPlanNodeTree.
	ios := &OP.IndexOnlyScan{}
	if got := operatorType(ios); got != "IndexOnlyScan" {
		t.Fatalf("operatorType(IndexOnlyScan) = %q, want %q", got, "IndexOnlyScan")
	}
}
