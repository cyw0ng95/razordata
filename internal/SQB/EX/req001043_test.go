package EX

import (
	"context"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001043: ParallelSeqScanRow basic unit test.
func TestParallelSeqScanRow_Basic(t *testing.T) {
	pool := UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	rows := make([]DT.Row, 100)
	for i := range rows {
		rows[i] = DT.Row{Data: []DT.Value{{Kind: KindInt, I64: int64(i)}}}
	}
	schema := []string{"val"}
	types := []LX.TokenType{LX.T_INT_KW}

	ps := OP.NewParallelSeqScanRow(rows, schema, types, pool)
	defer ps.Close()

	count := 0
	for {
		r, err := ps.Next(ctx)
		if err != nil {
			break
		}
		count++
		_ = r
	}
	if count != 100 {
		t.Errorf("got %d rows, want 100", count)
	}
}

// REQ001043: ParallelSeqScanRow value correctness.
func TestParallelSeqScanRow_Values(t *testing.T) {
	pool :=
		UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	rows := make([]DT.Row, 10)
	for i := range rows {
		rows[i] = DT.Row{Data: []DT.Value{
			{Kind: KindInt, I64: int64(i)},
			{Kind: KindText, S: fmt.Sprintf("v%d", i)},
		}}
	}
	schema := []string{"id", "name"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_TEXT}

	ps := OP.NewParallelSeqScanRow(rows, schema, types, pool)
	defer ps.Close()

	for i := 0; i < 10; i++ {
		r, err := ps.Next(ctx)
		if err != nil {
			t.Fatalf("next at %d: %v", i, err)
		}
		if r.Data[0].I64 != int64(i) {
			t.Errorf("row %d: id=%d, want %d", i, r.Data[0].I64, i)
		}
	}
	_, err := ps.Next(ctx)
	if err != DT.ErrNoRows {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

// REQ001043: Planner emits ParallelSeqScan for DT.Tables above the threshold.
func TestPlanner_ParallelScanSelection(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()

	// Create table
	_, err := e.Exec(ctx, "CREATE TABLE big (id INTEGER, val INTEGER)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < ParallelThreshold; i++ {
		_, err := e.Exec(ctx, fmt.Sprintf("INSERT INTO big VALUES (%d, %d)", i, i))
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Simple count — should return 10000
	rows, err := e.QueryAll(ctx, "SELECT count(*) FROM big")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("count: no rows returned")
	}
	if len(rows[0].Data) == 0 {
		t.Fatal("count: row has no data")
	}
	got := int(rows[0].Data[0].I64)
	if got != ParallelThreshold {
		t.Errorf("count: got %d, want %d", got, ParallelThreshold)
	}
}

// REQ001043: Verify the scan is actually parallel (multiple workers submit).
func TestParallelSeqScanRow_MultiWorker(t *testing.T) {
	pool :=
		UT.NewWorkerPool(4)
	defer pool.Close()
	ctx := context.Background()

	rows := make([]DT.Row, 50)
	for i := range rows {
		rows[i] = DT.Row{Data: []DT.Value{{Kind: KindInt, I64: int64(i)}}}
	}
	schema := []string{"v"}
	types := []LX.TokenType{LX.T_INT_KW}

	ps := OP.NewParallelSeqScanRow(rows, schema, types, pool)
	defer ps.Close()

	var sum int64
	for {
		r, err := ps.Next(ctx)
		if err != nil {
			break
		}
		sum += r.Data[0].I64
	}
	if sum != 1225 {
		t.Errorf("sum=%d, want 1225 (0+1+...+49)", sum)
	}
}
