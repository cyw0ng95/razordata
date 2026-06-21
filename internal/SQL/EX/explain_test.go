package EX

import (
	"context"
	"strings"
	"testing"
)

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
			allDetails += r.Data[3].(string) + "\n"
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
		// At least one of these should be present for in-memory tables
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
			d := r.Data[3].(string)
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
