//go:build slt_corpus

package slt

import (
	"context"
	"testing"
)

// REQ001730 regression: SUM(DISTINCT <constant>) must apply DISTINCT
// even when the plan cache already contains a non-DISTINCT plan for
// the same aggregate name and arg shape. The bug was that EncodeMemoKey
// did not include the Distinct flag, so SUM(28) and SUM(DISTINCT 28)
// shared the same cached plan — whichever ran first determined the
// behavior for both.
func TestREQ001730_DistinctPlanCacheCollision(t *testing.T) {
	ctx := context.Background()
	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = driver.Close(ctx) }()

	if err := driver.Exec(ctx, "CREATE TABLE tab0(col0 INTEGER, col1 INTEGER, col2 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := driver.Exec(ctx, "INSERT INTO tab0 VALUES(97,1,99)"); err != nil {
		t.Fatalf("insert1: %v", err)
	}
	if err := driver.Exec(ctx, "INSERT INTO tab0 VALUES(15,81,47)"); err != nil {
		t.Fatalf("insert2: %v", err)
	}
	if err := driver.Exec(ctx, "INSERT INTO tab0 VALUES(87,21,10)"); err != nil {
		t.Fatalf("insert3: %v", err)
	}

	// First: non-DISTINCT version populates the plan cache
	rs1, err := driver.QueryRaw(ctx, "SELECT ALL SUM(28) FROM tab0")
	if err != nil {
		t.Fatalf("non-distinct query error: %v", err)
	}
	if len(rs1.Rows) != 1 || rs1.Rows[0][0].Int != 84 {
		t.Fatalf("SUM(28): expected 84, got %v", rs1.Rows)
	}

	// Second: DISTINCT version must NOT use the cached non-DISTINCT plan
	rs2, err := driver.QueryRaw(ctx, "SELECT ALL SUM ( DISTINCT 28 ) FROM tab0")
	if err != nil {
		t.Fatalf("distinct query error: %v", err)
	}
	if len(rs2.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs2.Rows))
	}
	got := rs2.Rows[0][0].Int
	if got != 28 {
		t.Fatalf("SUM(DISTINCT 28): expected 28, got %d (plan cache collision)", got)
	}
}

// REQ001730 regression: verify SUM(DISTINCT constant) works correctly
// after running various other queries that may populate the plan cache
// with different aggregate shapes.
func TestREQ001730_SumDistinctConstant_AfterManyQueries(t *testing.T) {
	ctx := context.Background()
	driver := NewRazorDriver()
	if err := driver.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = driver.Close(ctx) }()

	if err := driver.Exec(ctx, "CREATE TABLE tab0(col0 INTEGER, col1 INTEGER, col2 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := driver.Exec(ctx, "INSERT INTO tab0 VALUES(97,1,99)"); err != nil {
		t.Fatalf("insert1: %v", err)
	}
	if err := driver.Exec(ctx, "INSERT INTO tab0 VALUES(15,81,47)"); err != nil {
		t.Fatalf("insert2: %v", err)
	}
	if err := driver.Exec(ctx, "INSERT INTO tab0 VALUES(87,21,10)"); err != nil {
		t.Fatalf("insert3: %v", err)
	}

	// Run various queries first to exercise different code paths
	warmupQueries := []string{
		"SELECT COUNT(*) FROM tab0",
		"SELECT SUM(col0) FROM tab0",
		"SELECT SUM(DISTINCT col0) FROM tab0",
		"SELECT MIN(col0) FROM tab0",
		"SELECT MAX(col0) FROM tab0",
		"SELECT AVG(col0) FROM tab0",
		"SELECT COUNT(DISTINCT col0) FROM tab0",
		"SELECT * FROM tab0",
		"SELECT col0 FROM tab0 WHERE col1 > 0",
		"SELECT col0 + col1 FROM tab0",
	}
	for _, q := range warmupQueries {
		_, err := driver.QueryRaw(ctx, q)
		if err != nil {
			t.Fatalf("warmup query %q error: %v", q, err)
		}
	}

	// Now the failing query
	rs, err := driver.QueryRaw(ctx, "SELECT ALL SUM ( DISTINCT 28 ) FROM tab0")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	t.Logf("result after warmup: %+v", rs)
	if len(rs.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs.Rows))
	}
	got := rs.Rows[0][0].Int
	if got != 28 {
		t.Fatalf("SUM(DISTINCT 28): expected 28, got %d", got)
	}
}
