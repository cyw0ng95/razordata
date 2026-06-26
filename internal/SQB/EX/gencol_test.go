package EX

import (
	"context"
	"testing"
)

// TestGeneratedColumnMaterialize verifies REQ000248/249:
// generated columns are parsed from CREATE TABLE and materialized
// on INSERT. STORED columns compute the expression at write time;
// VIRTUAL columns are accepted in the parser but treated as
// regular columns (deferred materialization, v0.28+).
func TestGeneratedColumnMaterialize(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "CREATE TABLE t (a INTEGER, b INTEGER AS (a + 1) STORED)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (10, NULL)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := ex.QueryAll(ctx, "SELECT a, b FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if v, ok := rows[0].Data[1].ToAny().(int64); !ok || v != 11 {
		t.Errorf("expected b=11 (a+1), got %v", rows[0].Data[1])
	}

	// Test multiple generated columns
	if _, err := ex.Exec(ctx, "CREATE TABLE t2 (x INTEGER, y INTEGER, s INTEGER AS (x + y) STORED)"); err != nil {
		t.Fatalf("create t2: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t2 VALUES (3, 4, NULL)"); err != nil {
		t.Fatalf("insert t2: %v", err)
	}
	rows2, _ := ex.QueryAll(ctx, "SELECT x, y, s FROM t2")
	if v, ok := rows2[0].Data[2].ToAny().(int64); !ok || v != 7 {
		t.Errorf("expected s=7, got %v", rows2[0].Data[2])
	}

	// Test VIRTUAL column syntax accepted
	if _, err := ex.Exec(ctx, "CREATE TABLE t3 (a INTEGER, b INTEGER AS (a * 2) VIRTUAL)"); err != nil {
		t.Errorf("VIRTUAL column syntax rejected: %v", err)
	}
}
