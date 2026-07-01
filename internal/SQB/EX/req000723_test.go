package EX

import (
	"context"
	"strconv"
	"testing"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT")

// REQ000723: NOT(...) filter returns wrong results on indexed DT.Tables
// via driver path. Same root cause as REQ000722 — cannot reproduce
// in minimal harness. Guard test.
func TestREQ000723_NOTFilterOnIndex(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE tab0 (pk INTEGER PRIMARY KEY, col0 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ex.Exec(ctx, "CREATE INDEX idx ON tab0(col0)"); err != nil {
		t.Fatalf("create index: %v", err)
	}
	for i := 1; i <= 10; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO tab0 VALUES ("+strconv.Itoa(i)+", "+strconv.Itoa(i*10)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT pk FROM tab0 WHERE NOT ((col0 > 68))")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("expected >0 rows, got 0")
	}
	_ = eng
}

// REQ000724: Complex OR/AND/IN on indexed DT.Tables. Same root cause as 722.
func TestREQ000724_ComplexORANDIN(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE tab1 (pk INTEGER PRIMARY KEY, col0 INTEGER, col1 REAL)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 10; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO tab1 VALUES ("+strconv.Itoa(i)+", "+strconv.Itoa(i*10)+", "+strconv.FormatFloat(float64(i)+0.5, 'f', 2, 64)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT pk FROM tab1 WHERE col1 > 5.5 OR (col0 IN (10,20,30)) AND col0 > 8")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("expected >0 rows, got 0")
	}
	_ = eng
}
