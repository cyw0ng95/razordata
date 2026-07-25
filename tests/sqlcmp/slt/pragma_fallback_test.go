package slt

import (
	"context"
	"testing"
	"time"
)

// TestPragmaEvalFallbackStats verifies that PRAGMA eval_fallback_stats
// reads and resets the global batch→row fallback counter. REQ001994.
//
// Notes on the harness:
//   - PRAGMA is classified as DML by the streaming executor
//     (QueryStreamCompiled at stream.go:443 rejects it). Therefore we use
//     Exec for the reset side-effect, and QueryRaw (which goes through
//     ExecuteCompiled, not QueryStreamCompiled) for the read.
//   - abs() in vectorized mode falls back to row-at-a-time because
//     scalar function calls don't have a batch kernel.
func TestPragmaEvalFallbackStats(t *testing.T) {
	if testing.Short() {
		t.Skip("slt: pragma test skipped in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("driver.Connect: %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(ctx) })

	// Force vectorized mode so the fallback path is exercised when an
	// expression isn't vectorized.
	if err := driver.Exec(ctx, "PRAGMA vectorized_mode = on"); err != nil {
		t.Fatalf("PRAGMA vectorized_mode: %v", err)
	}

	// Setup table.
	if err := driver.Exec(ctx, "CREATE TABLE fb (id INTEGER, v INTEGER)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if err := driver.Exec(ctx, "INSERT INTO fb VALUES (1, 10), (2, 20), (3, 30)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	// Step 1: drain any baseline counter via PRAGMA-as-Exec (the reset
	// side-effect runs regardless of row consumption).
	if err := driver.Exec(ctx, "PRAGMA eval_fallback_stats"); err != nil {
		t.Logf("PRAGMA exec reset (ignored): %v", err)
	}

	// Step 2: run a fully-vectorized SELECT (comparison + arithmetic).
	// These have vectorized paths; should NOT bump the counter.
	if _, err := driver.Query(ctx, "SELECT v + 1 FROM fb WHERE v > 5"); err != nil {
		t.Fatalf("vectorized select: %v", err)
	}

	// Step 3: run a SELECT that triggers row fallback. printf() has no
	// batch kernel (only abs/length/mod/upper/lower/sign/octet_length
	// do), so it falls through to evalRowFallbackColumn — which calls
	// batchToRow per row and bumps the counter.
	if _, err := driver.Query(ctx, "SELECT printf('%d', v) FROM fb"); err != nil {
		// Some engines may not support printf; fall back to coalesce.
		if _, err2 := driver.Query(ctx, "SELECT coalesce(v, 0) FROM fb"); err2 != nil {
			t.Fatalf("fallback select: %v / %v", err, err2)
		}
	}

	// Step 4: read counter via QueryRaw (bypasses streaming DML gate).
	rs, err := driver.QueryRaw(ctx, "PRAGMA eval_fallback_stats")
	if err != nil {
		t.Fatalf("PRAGMA eval_fallback_stats (read): %v", err)
	}
	if len(rs.Rows) == 0 || len(rs.Rows[0]) == 0 {
		t.Fatalf("PRAGMA returned no rows: %+v", rs)
	}
	val := rs.Rows[0][0]
	if val.Kind != TypeInteger {
		t.Fatalf("unexpected value kind: %v (val=%+v)", val.Kind, val)
	}
	hits := val.Int
	if hits < 1 {
		t.Errorf("expected fallback hits >= 1 after printf(), got %d", hits)
	}

	// Step 5: second PRAGMA call resets → should return 0.
	rs2, err := driver.QueryRaw(ctx, "PRAGMA eval_fallback_stats")
	if err != nil {
		t.Fatalf("PRAGMA eval_fallback_stats (reset verify): %v", err)
	}
	if len(rs2.Rows) > 0 && len(rs2.Rows[0]) > 0 {
		v2 := rs2.Rows[0][0]
		if v2.Int != 0 {
			t.Errorf("expected reset to 0, got %d", v2.Int)
		}
	}
}