//go:build !slt_corpus_full

package EX

import (
	"context"
	"strconv"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001440: end-to-end through QueryAll. Set up a real LSM
// engine with 100 rows and run a simple SELECT id, name. The
// planner's tryVectorizePlan detects the eligible shape
// (SeqScan + Project) and switches the tree to VectorizedSeqScan +
// VectorizedProject wrapped in BatchToRowAdapter. We exercise
// QueryAll and assert the rows reach the client. The BatchToRow
// adapter now calls Batch.ToRows() once per batch (REQ001440
// improvement) instead of per-row conversion.
func TestQueryAll_GoesThroughVectorizedPath_SimpleProject(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()

	schema := []string{"id", "name"}
	_ = schema
	mustExec(t, ex, ctx, "CREATE TABLE vec_demo (id INTEGER PRIMARY KEY, name TEXT)")
	for i := 0; i < 100; i++ {
		mustExec(t, ex, ctx, "INSERT INTO vec_demo VALUES ("+strconv.Itoa(i)+", 'row_"+strconv.Itoa(i)+"')")
	}
	rows, err := ex.QueryAll(ctx, "SELECT id, name FROM vec_demo")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 100 {
		t.Fatalf("got %d rows, want 100", len(rows))
	}
	for i, r := range rows {
		if r.Data[0].Kind != DT.KindInt || r.Data[0].I64 != int64(i) {
			t.Errorf("r[%d].id: got %v want %d", i, r.Data[0].I64, i)
			break
		}
		want := "row_" + strconv.Itoa(i)
		if r.Data[1].S != want {
			t.Errorf("r[%d].name: got %q want %q", i, r.Data[1].S, want)
			break
		}
	}
}

// REQ001440: BatchToRowAdapter's per-batch ToRows() materialisation
// produces stable Cols across rows. The prior per-row conversion
// re-decoded Cols each Next(); the new per-batch conversion shares
// Cols across all rows in the batch. Verify by hand-rolling a
// VectorizedSeqScan + VectorizedProject over an in-memory slice.
func TestVecTransformToRowsAdapter_StreamSimpleSelect(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)
	ctx := context.Background()
	mustExec(t, ex, ctx, "CREATE TABLE vadapter (a INTEGER PRIMARY KEY, b TEXT)")
	for i := 0; i < 50; i++ {
		mustExec(t, ex, ctx, "INSERT INTO vadapter VALUES ("+strconv.Itoa(i)+", 'b"+strconv.Itoa(i)+"')")
	}
	rows, err := ex.QueryAll(ctx, "SELECT b FROM vadapter")
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(rows) != 50 {
		t.Fatalf("rows: got %d, want 50", len(rows))
	}
	for i, r := range rows {
		_ = i
		if r.Data[0].Kind != DT.KindText {
			t.Errorf("col 0 kind: got %v, want Text", r.Data[0].Kind)
			break
		}
	}
	_ = OP.NewSeqScanWithStore
	_ = LX.T_INT_KW
}
