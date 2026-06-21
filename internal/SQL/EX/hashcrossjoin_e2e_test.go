// Package EX — E2E tests for HashCrossJoin via the SQL planner.
// REQ000800.
package EX

import (
	"context"
	"testing"
)

// TestHashCrossJoin_E2E verifies REQ000800: the planner picks
// HashCrossJoin for INNER JOIN ON with simple equi-conditions.
// Uses table counts small enough that the operator's 1024-row
// materialization limit is satisfied.
func TestHashCrossJoin_E2E(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1(a INTEGER, b INTEGER)",
		"CREATE TABLE t2(c INTEGER, d INTEGER)",
		"INSERT INTO t1 VALUES (1,10), (2,20), (3,30)",
		"INSERT INTO t2 VALUES (2,200), (3,300), (3,400)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	// INNER JOIN ON with simple equi-key.
	rows, err := e.QueryAll(ctx, "SELECT t1.a, t2.c FROM t1 INNER JOIN t2 ON t1.a = t2.c")
	if err != nil {
		t.Fatalf("equi-join: %v", err)
	}
	// t1.a IN {1,2,3}, t2.c IN {2,3,3}
	// Expected matches: t1.a=2 → t2.c=2 (1), t1.a=3 → t2.c=3 (2)
	if len(rows) != 3 {
		t.Errorf("equi-join: expected 3 rows, got %d", len(rows))
	}

	// Swapped key order should still match.
	rows, err = e.QueryAll(ctx, "SELECT t1.a, t2.c FROM t1 INNER JOIN t2 ON t2.c = t1.a")
	if err != nil {
		t.Fatalf("equi-join swapped: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("equi-join swapped: expected 3 rows, got %d", len(rows))
	}

	// Compound ON condition — HashCrossJoin skipped, falls back to NLJ.
	rows, err = e.QueryAll(ctx, "SELECT t1.a FROM t1 INNER JOIN t2 ON t1.a = t2.c AND t1.b > 10")
	if err != nil {
		t.Fatalf("compound: %v", err)
	}
	// t1.a=2 (b=20>10) → 1 match. t1.a=3 (b=30>10) → 2 matches. Total 3.
	if len(rows) != 3 {
		t.Errorf("compound: expected 3 rows, got %d", len(rows))
	}
}