package EX

import (
	"context"
	"strconv"
	"testing"
)

// REQ000720: SELECT cor0.col1 FROM tab1 AS cor0 returns the string
// "cor0.col1" instead of the column value via the database/sql driver
// path. Same query works on the in-memory executor. Root cause: SeqScan
// on the store path produces rows with column names that don't have
// the alias prefix, so the QualifiedName lookup `cor0.col1` fails and
// falls back to returning the literal "cor0.col1".
func TestREQ000720_AliasedQualifiedName(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("tab1", []string{"col1"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO tab1 VALUES (42)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := ex.QueryAll(ctx, "SELECT cor0.col1 FROM tab1 AS cor0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	t.Logf("rows: %v", rows)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	v, ok := rows[0].Data[0].ToAny().(int64)
	if !ok || v != 42 {
		t.Errorf("expected int64(42), got %v (%T)", rows[0].Data[0], rows[0].Data[0])
	}
	// also test group by
	rows2, err := ex.QueryAll(ctx, "SELECT cor0.col1 FROM tab1 AS cor0 GROUP BY cor0.col1")
	if err != nil {
		t.Fatalf("group by: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("group by: expected 1 row, got %d", len(rows2))
	}
	v2, ok := rows2[0].Data[0].ToAny().(int64)
	if !ok || v2 != 42 {
		t.Errorf("group by: expected int64(42), got %v (%T)", rows2[0].Data[0], rows2[0].Data[0])
	}
	if len(rows2[0].Cols) == 0 || rows2[0].Cols[0] != "cor0.col1" {
		t.Errorf("group by: expected Cols[0]=cor0.col1, got %v", rows2[0].Cols)
	}
}

func TestREQ000720_AliasedQualifiedNameStore(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE tab1 (col1 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO tab1 VALUES (42)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := ex.QueryAll(ctx, "SELECT cor0.col1 FROM tab1 AS cor0")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	t.Logf("rows: %v", rows)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	v, ok := rows[0].Data[0].ToAny().(int64)
	if !ok || v != 42 {
		t.Errorf("expected int64(42), got %v (%T)", rows[0].Data[0], rows[0].Data[0])
	}
	_ = strconv.Itoa
}
