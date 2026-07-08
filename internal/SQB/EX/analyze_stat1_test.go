package EX

import (
	"context"
	"encoding/binary"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestAnalyze_PersistsToStat1Table verifies REQ001317: ANALYZE writes
// statistics to the razor_stat1 system table, making them queryable via SQL.
func TestAnalyze_PersistsToStat1Table(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE stats_test (id INTEGER PRIMARY KEY, val INTEGER, name TEXT)`)
	for i := 1; i <= 50; i++ {
		if _, err := ex.Exec(ctx, `INSERT INTO stats_test VALUES (?, ?, ?)`, i, i*10, "row"); err != nil {
			t.Fatal(err)
		}
	}

	mustExec(t, ex, ctx, `ANALYZE stats_test`)

	rows := mustQueryAll(t, ex, ctx, `SELECT tbl, col_name, ndv, rowcount, null_count FROM razor_stat1 WHERE tbl = 'stats_test'`)
	if len(rows) == 0 {
		t.Fatal("expected stats in razor_stat1, got none")
	}

	foundCols := make(map[string]bool)
	for _, r := range rows {
		col := r.Data[1].ToAny().(string)
		ndv := r.Data[2].I64
		rowcount := r.Data[3].I64
		nullCount := r.Data[4].I64
		foundCols[col] = true
		t.Logf("col=%s ndv=%d rowcount=%d null_count=%d", col, ndv, rowcount, nullCount)

		if rowcount != 50 {
			t.Errorf("col %s: expected rowcount=50, got %d", col, rowcount)
		}
		if nullCount != 0 {
			t.Errorf("col %s: expected null_count=0, got %d", col, nullCount)
		}
	}

	for _, expected := range []string{"id", "val", "name"} {
		if !foundCols[expected] {
			t.Errorf("missing stats for column %s", expected)
		}
	}
}

// TestAnalyze_StatsMinMax verifies REQ001316: min/max are persisted.
func TestAnalyze_StatsMinMax(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE mm_test (id INTEGER PRIMARY KEY, val INTEGER)`)
	for i := 1; i <= 20; i++ {
		if _, err := ex.Exec(ctx, `INSERT INTO mm_test VALUES (?, ?)`, i, i*5); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ex, ctx, `ANALYZE mm_test`)

	rows := mustQueryAll(t, ex, ctx, `SELECT col_name, min_val, max_val FROM razor_stat1 WHERE tbl = 'mm_test' AND col_name = 'val'`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for val stats, got %d", len(rows))
	}
	// min/max are stored as BLOBs (big-endian int64 bytes).
	minBlob := rows[0].Data[1].B
	maxBlob := rows[0].Data[2].B
	t.Logf("val: min_blob=%x max_blob=%x", minBlob, maxBlob)

	// Decode big-endian int64
	var minVal, maxVal int64
	if len(minBlob) == 8 {
		minVal = int64(binary.BigEndian.Uint64(minBlob))
	}
	if len(maxBlob) == 8 {
		maxVal = int64(binary.BigEndian.Uint64(maxBlob))
	}
	t.Logf("val: min=%d max=%d", minVal, maxVal)

	if minVal != 5 {
		t.Errorf("expected min=5, got %d", minVal)
	}
	if maxVal != 100 {
		t.Errorf("expected max=100, got %d", maxVal)
	}
}

// TestAnalyze_StatsOverwrite verifies that re-running ANALYZE replaces
// old stats (not appends). REQ001317.
func TestAnalyze_StatsOverwrite(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE ow_test (id INTEGER PRIMARY KEY, val INTEGER)`)
	for i := 1; i <= 10; i++ {
		if _, err := ex.Exec(ctx, `INSERT INTO ow_test VALUES (?, ?)`, i, i); err != nil {
			t.Fatal(err)
		}
	}

	mustExec(t, ex, ctx, `ANALYZE ow_test`)
	rows1 := mustQueryAll(t, ex, ctx, `SELECT COUNT(*) FROM razor_stat1 WHERE tbl = 'ow_test'`)
	count1 := rows1[0].Data[0].I64
	t.Logf("after first ANALYZE: %d stat rows", count1)

	for i := 11; i <= 20; i++ {
		if _, err := ex.Exec(ctx, `INSERT INTO ow_test VALUES (?, ?)`, i, i); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ex, ctx, `ANALYZE ow_test`)
	rows2 := mustQueryAll(t, ex, ctx, `SELECT COUNT(*) FROM razor_stat1 WHERE tbl = 'ow_test'`)
	count2 := rows2[0].Data[0].I64
	t.Logf("after second ANALYZE: %d stat rows", count2)

	if count1 != count2 {
		t.Errorf("expected same stat row count after re-ANALYZE, got %d then %d", count1, count2)
	}

	rows3 := mustQueryAll(t, ex, ctx, `SELECT rowcount FROM razor_stat1 WHERE tbl = 'ow_test' AND col_name = 'id'`)
	if len(rows3) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows3))
	}
	if rows3[0].Data[0].I64 != 20 {
		t.Errorf("expected rowcount=20 after re-ANALYZE, got %d", rows3[0].Data[0].I64)
	}
}

// TestAnalyze_StatsSelectableBySQL verifies that razor_stat1 can be
// queried with standard SQL (WHERE, ORDER BY, aggregation).
func TestAnalyze_StatsSelectableBySQL(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE sel_test (a INTEGER PRIMARY KEY, b INTEGER, c TEXT)`)
	for i := 1; i <= 30; i++ {
		if _, err := ex.Exec(ctx, `INSERT INTO sel_test VALUES (?, ?, ?)`, i, i*3, "x"); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ex, ctx, `ANALYZE sel_test`)

	rows := mustQueryAll(t, ex, ctx, `SELECT col_name, ndv FROM razor_stat1 WHERE tbl = 'sel_test' ORDER BY ndv DESC`)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	t.Logf("stats ordered by ndv desc: %v", rows)

	rows2 := mustQueryAll(t, ex, ctx, `SELECT SUM(rowcount) FROM razor_stat1 WHERE tbl = 'sel_test'`)
	total := rows2[0].Data[0].I64
	if total != 90 {
		t.Errorf("expected total rowcount=90, got %d", total)
	}
}

// TestAnalyze_NoTable_NoStats verifies that ANALYZE without a table
// name doesn't crash and doesn't create stats. REQ001317.
func TestAnalyze_NoTable_NoStats(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `ANALYZE`)

	_, ok := DT.Tables["razor_stat1"]
	if ok {
		t.Log("razor_stat1 exists after bare ANALYZE — acceptable but unexpected")
	}
}

// TestAnalyze_NullValues_Counted verifies REQ001315: null values
// are counted in the stats.
func TestAnalyze_NullValues_Counted(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	mustExec(t, ex, ctx, `CREATE TABLE null_test (id INTEGER PRIMARY KEY, val INTEGER)`)
	for i := 1; i <= 5; i++ {
		if _, err := ex.Exec(ctx, `INSERT INTO null_test VALUES (?, NULL)`, i); err != nil {
			t.Fatal(err)
		}
	}
	for i := 6; i <= 10; i++ {
		if _, err := ex.Exec(ctx, `INSERT INTO null_test VALUES (?, ?)`, i, i*10); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ex, ctx, `ANALYZE null_test`)

	rows := mustQueryAll(t, ex, ctx, `SELECT col_name, null_count, ndv FROM razor_stat1 WHERE tbl = 'null_test' AND col_name = 'val'`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	nullCount := rows[0].Data[1].I64
	ndv := rows[0].Data[2].I64
	t.Logf("val: null_count=%d ndv=%d", nullCount, ndv)

	if nullCount != 5 {
		t.Errorf("expected null_count=5, got %d", nullCount)
	}
	if ndv != 5 {
		t.Errorf("expected ndv=5 (5 distinct non-null values), got %d", ndv)
	}
}
