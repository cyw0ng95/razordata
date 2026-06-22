package EX

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// makeParallelTestRows creates N rows with id=0..N-1 and value="row".
func makeParallelTestRows(n int) []Row {
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols:  []string{"id", "value"},
			Types: []int{int(LX.T_INT_KW), int(LX.T_TEXT)},
			Data:  []Value{NewIntValue(int64(i)), NewTextValue("row")},
		}
	}
	return rows
}

// TestParallelSeqScan_Basic verifies correct row count.
func TestParallelSeqScan_Basic(t *testing.T) {
	rows := makeParallelTestRows(100)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id", "value"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_TEXT}
	pool := NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelSeqScan(src, schema, types, pool, rows)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
}

// TestParallelSeqScan_Empty verifies empty source.
func TestParallelSeqScan_Empty(t *testing.T) {
	pool := NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelSeqScan(nil, []string{"id"}, []LX.TokenType{LX.T_INT_KW}, pool, nil)
	defer scan.Close()

	batch, err := scan.NextBatch(context.Background())
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if batch != nil {
		t.Error("expected nil batch for empty rows")
		batch.Put()
	}
}

// TestParallelSeqScan_Large scans 10K rows across 4 workers.
func TestParallelSeqScan_Large(t *testing.T) {
	rows := makeParallelTestRows(10000)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	pool := NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelSeqScan(src, schema, types, pool, rows)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	if total != 10000 {
		t.Errorf("total = %d, want 10000", total)
	}
}

// TestParallelIndexScan_Basic verifies index scan correctness.
func TestParallelIndexScan_Basic(t *testing.T) {
	rows := makeParallelTestRows(50)
	pred := &PS.BinaryExpr{
		Left:  &PS.Ident{Name: "id"},
		Op:    int(LX.T_GE),
		Right: &PS.NumberLiteral{Val: 25},
	}
	pool := NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelIndexScan(rows, "id", []string{"id"}, []LX.TokenType{LX.T_INT_KW}, pred, pool)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	// IDs 25-49 = 25 rows
	if total != 25 {
		t.Errorf("total = %d, want 25", total)
	}
}

// TestParallelIndexScan_NoPred verifies no-predicate path.
func TestParallelIndexScan_NoPred(t *testing.T) {
	rows := makeParallelTestRows(100)
	pool := NewWorkerPool(4)
	defer pool.Close()

	scan := NewParallelIndexScan(rows, "id", []string{"id"}, []LX.TokenType{LX.T_INT_KW}, nil, pool)
	defer scan.Close()

	total := 0
	for {
		batch, err := scan.NextBatch(context.Background())
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if batch == nil {
			break
		}
		total += batch.LogicalSize()
		batch.Put()
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
}

// TestParallelSeqScan_ConcurrentReaders verifies pool safety.
func TestParallelSeqScan_ConcurrentReaders(t *testing.T) {
	rows := makeParallelTestRows(1000)
	src := &rowSourceForTest{rows: rows}
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	pool := NewWorkerPool(4)
	defer pool.Close()

	var wg sync.WaitGroup
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scan := NewParallelSeqScan(src, schema, types, pool, rows)
			defer scan.Close()
			total := 0
			for {
				batch, _ := scan.NextBatch(context.Background())
				if batch == nil {
					break
				}
				total += batch.LogicalSize()
				batch.Put()
			}
		}()
	}
	wg.Wait()
}

// BenchmarkParallelSeqScan measures parallel scan throughput.
func BenchmarkParallelSeqScan(b *testing.B) {
	rows := makeParallelTestRows(10 * 1024)
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	src := &rowSourceForTest{rows: rows}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool := NewWorkerPool(4)
		scan := NewParallelSeqScan(src, schema, types, pool, rows)
		for {
			batch, _ := scan.NextBatch(context.Background())
			if batch == nil {
				break
			}
			batch.Put()
		}
		scan.Close()
		pool.Close()
	}
}

// BenchmarkParallelSeqScanScaling measures scaling with worker count.
func BenchmarkParallelSeqScanScaling(b *testing.B) {
	rows := makeParallelTestRows(10 * 1024)
	schema := []string{"id"}
	types := []LX.TokenType{LX.T_INT_KW}
	src := &rowSourceForTest{rows: rows}

	for _, workers := range []int{1, 2, 4, 8} {
		b.Run("workers="+strconv.Itoa(workers), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pool := NewWorkerPool(workers)
				scan := NewParallelSeqScan(src, schema, types, pool, rows)
				for {
					batch, _ := scan.NextBatch(context.Background())
					if batch == nil {
						break
					}
					batch.Put()
				}
				scan.Close()
				pool.Close()
			}
		})
	}
}
