//go:build !slt_corpus_full

package EX

import (
	"context"
	"strings"
	"testing"
)

// TestAggregate_Distinct_Count verifies REQ000437: COUNT(DISTINCT col)
// deduplicates before counting. REQ000437 (iter-27).
func TestAggregate_Distinct_Count(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 10)",
		"INSERT INTO t VALUES (3, 20)",
		"INSERT INTO t VALUES (4, 20)",
		"INSERT INTO t VALUES (5, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT COUNT(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("COUNT(DISTINCT): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0] != int64(3) {
		t.Errorf("COUNT(DISTINCT v) = %v, want 3", rows[0].Data[0])
	}
}

// TestAggregate_Distinct_Sum verifies REQ000437: SUM(DISTINCT col)
// deduplicates before summing.
func TestAggregate_Distinct_Sum(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 10)",
		"INSERT INTO t VALUES (3, 20)",
		"INSERT INTO t VALUES (4, 30)",
		"INSERT INTO t VALUES (5, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT SUM(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("SUM(DISTINCT): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	// Distinct values: {10, 20, 30}; sum = 60
	if rows[0].Data[0] != int64(60) {
		t.Errorf("SUM(DISTINCT v) = %v, want 60", rows[0].Data[0])
	}
}

// TestAggregate_Distinct_Avg verifies REQ000437: AVG(DISTINCT col)
// deduplicates before averaging.
func TestAggregate_Distinct_Avg(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 10)",
		"INSERT INTO t VALUES (3, 20)",
		"INSERT INTO t VALUES (4, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT AVG(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("AVG(DISTINCT): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	// Distinct values: {10, 20, 30}; avg = 60/3 = 20
	got, ok := rows[0].Data[0].(float64)
	if !ok {
		t.Fatalf("AVG(DISTINCT v) = %v (type %T), want float64", rows[0].Data[0], rows[0].Data[0])
	}
	if got != 20.0 {
		t.Errorf("AVG(DISTINCT v) = %v, want 20.0", got)
	}
}

// TestAggregate_Distinct_MinMax verifies REQ000437: MIN/MAX(DISTINCT col).
func TestAggregate_Distinct_MinMax(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 10)",
		"INSERT INTO t VALUES (3, 50)",
		"INSERT INTO t VALUES (4, 20)",
		"INSERT INTO t VALUES (5, 20)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT MIN(DISTINCT v), MAX(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("MIN/MAX(DISTINCT): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0] != int64(10) {
		t.Errorf("MIN(DISTINCT v) = %v, want 10", rows[0].Data[0])
	}
	if rows[0].Data[1] != int64(50) {
		t.Errorf("MAX(DISTINCT v) = %v, want 50", rows[0].Data[1])
	}
}

// TestAggregate_Distinct_GroupConcat verifies REQ000437:
// GROUP_CONCAT(DISTINCT col) deduplicates before concatenating.
func TestAggregate_Distinct_GroupConcat(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'a')",
		"INSERT INTO t VALUES (2, 'a')",
		"INSERT INTO t VALUES (3, 'b')",
		"INSERT INTO t VALUES (4, 'c')",
		"INSERT INTO t VALUES (5, 'b')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT GROUP_CONCAT(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("GROUP_CONCAT(DISTINCT): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got, _ := rows[0].Data[0].(string)
	// Order: a, b, c (insertion order, duplicates removed)
	want := "a,b,c"
	if got != want {
		t.Errorf("GROUP_CONCAT(DISTINCT v) = %q, want %q", got, want)
	}
}

// TestAggregate_Distinct_GroupBy verifies REQ000437: DISTINCT aggregates
// compose correctly with GROUP BY.
func TestAggregate_Distinct_GroupBy(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "grp", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'x', 10)",
		"INSERT INTO t VALUES (2, 'x', 10)",
		"INSERT INTO t VALUES (3, 'x', 20)",
		"INSERT INTO t VALUES (4, 'y', 30)",
		"INSERT INTO t VALUES (5, 'y', 30)",
		"INSERT INTO t VALUES (6, 'y', 40)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT grp, SUM(DISTINCT v) FROM t GROUP BY grp ORDER BY grp")
	if err != nil {
		t.Fatalf("GROUP BY SUM(DISTINCT): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// grp=x: distinct {10, 20} = 30
	// grp=y: distinct {30, 40} = 70
	if rows[0].Data[0] != "x" || rows[0].Data[1] != int64(30) {
		t.Errorf("grp=x: got %v, want [x, 30]", rows[0].Data)
	}
	if rows[1].Data[0] != "y" || rows[1].Data[1] != int64(70) {
		t.Errorf("grp=y: got %v, want [y, 70]", rows[1].Data)
	}
}

// TestAggregate_Distinct_Nulls verifies REQ000437: NULL values are
// skipped before dedup (NULLs do not count as duplicates).
func TestAggregate_Distinct_Nulls(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 10)",
		"INSERT INTO t VALUES (3, NULL)",
		"INSERT INTO t VALUES (4, NULL)",
		"INSERT INTO t VALUES (5, 20)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT COUNT(DISTINCT v), SUM(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("DISTINCT with NULLs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	// NULLs are skipped, distinct non-null = {10, 20}
	if rows[0].Data[0] != int64(2) {
		t.Errorf("COUNT(DISTINCT v) = %v, want 2 (NULLs excluded)", rows[0].Data[0])
	}
	if rows[0].Data[1] != int64(30) {
		t.Errorf("SUM(DISTINCT v) = %v, want 30", rows[0].Data[1])
	}
}

// TestAggregate_Distinct_AllUnique verifies REQ000437: when all values
// are unique, DISTINCT produces the same result as the non-DISTINCT form.
func TestAggregate_Distinct_AllUnique(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 1)",
		"INSERT INTO t VALUES (2, 2)",
		"INSERT INTO t VALUES (3, 3)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT COUNT(DISTINCT v), COUNT(v), SUM(DISTINCT v), SUM(v) FROM t")
	if err != nil {
		t.Fatalf("all-unique DISTINCT: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Data[0] != int64(3) {
		t.Errorf("COUNT(DISTINCT) = %v, want 3", rows[0].Data[0])
	}
	if rows[0].Data[1] != int64(3) {
		t.Errorf("COUNT = %v, want 3", rows[0].Data[1])
	}
	if rows[0].Data[2] != int64(6) {
		t.Errorf("SUM(DISTINCT) = %v, want 6", rows[0].Data[2])
	}
	if rows[0].Data[3] != int64(6) {
		t.Errorf("SUM = %v, want 6", rows[0].Data[3])
	}
}

// TestAggregate_Distinct_EmptyTable verifies REQ000437: DISTINCT on
// an empty table returns NULL (same as non-DISTINCT).
func TestAggregate_Distinct_EmptyTable(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	rows, err := ex.QueryAll(ctx, "SELECT SUM(DISTINCT v), AVG(DISTINCT v), MIN(DISTINCT v), MAX(DISTINCT v), GROUP_CONCAT(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("DISTINCT empty: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	for i, agg := range []string{"SUM", "AVG", "MIN", "MAX", "GROUP_CONCAT"} {
		if rows[0].Data[i] != nil {
			t.Errorf("empty %s(DISTINCT) = %v, want nil", agg, rows[0].Data[i])
		}
	}
}

// TestAggregate_Distinct_ParseError verifies REQ000437: the parser
// does not emit the "expected expression, got DISTINCT" error for
// aggregate DISTINCT (regression test for REQ000437 root cause).
func TestAggregate_Distinct_ParseError(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 1)"); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"SELECT COUNT(DISTINCT v) FROM t",
		"SELECT SUM(DISTINCT v) FROM t",
		"SELECT AVG(DISTINCT v) FROM t",
		"SELECT MIN(DISTINCT v) FROM t",
		"SELECT MAX(DISTINCT v) FROM t",
		"SELECT GROUP_CONCAT(DISTINCT v) FROM t",
	}
	for _, q := range queries {
		_, err := ex.QueryAll(ctx, q)
		if err != nil {
			if strings.Contains(err.Error(), "got DISTINCT") {
				t.Errorf("query %q: parser error regressed: %v", q, err)
			}
		}
	}
}
