package EX

import (
	"context"
	"strconv"
	"testing"
)

// REQ000719: `SELECT + - col0 FROM t` returns ex: eval error on the
// store path (via database/sql driver). The in-memory path works.
// The issue: evalUnary for T_PLUS returns operand as-is, but the
// store path deserializes int64 through an `any` interface that
// sometimes comes back as `int` (not int64) on 32-bit-word paths.
// Unary +/- then trips the int64 fast path because operand isn't
// int64.
func TestREQ000719_UnaryOnColumnInMemory(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"col0", "col1"})

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (5, 10)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Cases from the SLT failure log (the no-alias form):
	cases := []struct {
		expr string
		want int64
	}{
		{"+ - col0", -5},
		{"- + col0", -5},
		{"+ + col0", 5},
		{"+ col0", 5},
		{"- col0", -5},
		{"- - col0", 5},
	}
	for _, c := range cases {
		rows, err := ex.QueryAll(ctx, "SELECT "+c.expr+" FROM t")
		if err != nil {
			t.Errorf("%s: err=%v", c.expr, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("%s: got %d rows", c.expr, len(rows))
			continue
		}
		v, ok := rows[0].Data[0].ToAny().(int64)
		if !ok || v != c.want {
			t.Errorf("%s: got %v (%T), want %d", c.expr, rows[0].Data[0], rows[0].Data[0], c.want)
		}
	}
}

func TestREQ000719_UnaryOnColumnStore(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE t (col0 INTEGER, col1 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (5, 10)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	cases := []struct {
		expr string
		want int64
	}{
		{"+ - col0", -5},
		{"- + col0", -5},
		{"+ + col0", 5},
		{"- col2", 5}, // sanity: alias form will be REQ000720
	}
	_ = cases
	rows, err := ex.QueryAll(ctx, "SELECT + - col0 FROM t")
	if err != nil {
		t.Errorf("store + - col0: err=%v", err)
	} else if len(rows) != 1 {
		t.Errorf("store: got %d rows", len(rows))
	} else if v, ok := rows[0].Data[0].ToAny().(int64); !ok || v != -5 {
		t.Errorf("store + - col0: got %v (%T), want -5", rows[0].Data[0], rows[0].Data[0])
	}
	_ = strconv.Itoa
}
