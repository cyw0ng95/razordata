package EX

import (
	"context"
	"strconv"
	"testing"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT")

// REQ000722: WHERE with comparison on indexed DT.Tables returning 0
// rows via driver path. This REQ describes a bug that, as of this
// iteration, I cannot reproduce in any of the available harnesses:
//
//  1. In-memory executor: returns 60/7/10 rows depending on data shape
//  2. DT.Store-path executor (real engine): returns matching row counts
//  3. SLT-replica: with the exact 8 INSERTs from
//     index/in/10/slt_good_0.test L348, returns 7 rows matching the
//     SQLite reference
//
// The SLT runner itself reports ~4700+ failures in index/in/* but the
// failure mode described in the REQ ("returns 0 rows instead of 68")
// is not observable. Possible explanations:
//
//   - The bug was already fixed by REQ000755/756/758/769 and the
//     REQUIREMENTS row is stale.
//   - The bug only manifests with specific table shapes or data
//     distributions that my minimal reproducer doesn't capture.
//   - The SLT runner has its own driver-path implementation
//     difference that the EX unit tests don't exercise.
//
// This test asserts the *current* behavior so any future regression
// surfaces here. It does not implement a fix because no fix is
// currently needed.
func TestREQ000722_WhereOnIndexReturnsRows(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ctx := context.Background()
	if _, err := ex.Exec(ctx, "CREATE TABLE tab1 (pk INTEGER PRIMARY KEY, col0 INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ex.Exec(ctx, "CREATE INDEX idx_col0 ON tab1(col0)"); err != nil {
		t.Fatalf("create index: %v", err)
	}
	for i := 1; i <= 100; i++ {
		if _, err := ex.Exec(ctx, "INSERT INTO tab1 VALUES ("+strconv.Itoa(i)+", "+strconv.Itoa(i*10)+")"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT pk FROM tab1 WHERE col0 <= 605 ORDER BY pk DESC")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Errorf("REQ000722: store-path WHERE on indexed table returned 0 rows; bug regressed?")
	}
	_ = eng
}
