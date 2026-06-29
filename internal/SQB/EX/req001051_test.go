package EX

import (
	"context"
	"fmt"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001051: ParallelIndexRangeScan filters IN-list values correctly.
func TestParallelIndexRangeScan_Basic(t *testing.T) {
	pool := UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	rows := make([]Row, 20)
	for i := range rows {
		rows[i] = Row{Data: []Value{{Kind: KindInt, I64: int64(i)}}}
	}
	schema := []string{"val"}
	types := []LX.TokenType{LX.T_INT_KW}

	// IN (5, 12, 18) — should match rows at indices 5, 12, 18
	ps := NewParallelIndexRangeScan(rows, schema, types, "val",
		[]any{int64(5), int64(12), int64(18)}, pool)
	defer ps.Close()

	var results []int64
	for {
		r, err := ps.Next(ctx)
		if err != nil {
			break
		}
		results = append(results, r.Data[0].I64)
	}
	if len(results) != 3 {
		t.Errorf("got %d rows, want 3", len(results))
	}
	expected := map[int64]bool{5: true, 12: true, 18: true}
	for _, v := range results {
		if !expected[v] {
			t.Errorf("unexpected value %d", v)
		}
	}
}

// REQ001051: ParallelIndexRangeScan empty rows returns nothing.
func TestParallelIndexRangeScan_EmptyRows(t *testing.T) {
	pool := 
UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	ps := NewParallelIndexRangeScan(nil, []string{"val"}, []LX.TokenType{LX.T_INT_KW}, "val",
		[]any{int64(1)}, pool)
	defer ps.Close()

	_, err := ps.Next(ctx)
	if err == nil {
		t.Error("expected ErrNoRows for empty rows")
	}
}

// REQ001051: ParallelIndexRangeScan empty values returns nothing.
func TestParallelIndexRangeScan_EmptyValues(t *testing.T) {
	pool := 
UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	rows := []Row{{Data: []Value{{Kind: KindInt, I64: 1}}}}
	ps := NewParallelIndexRangeScan(rows, []string{"val"}, []LX.TokenType{LX.T_INT_KW}, "val", nil, pool)
	defer ps.Close()

	_, err := ps.Next(ctx)
	if err == nil {
		t.Error("expected ErrNoRows for empty values")
	}
}

// REQ001051: ParallelIndexRangeScan with string values.
func TestParallelIndexRangeScan_String(t *testing.T) {
	pool := 
UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	rows := []Row{
		{Data: []Value{{Kind: KindText, S: "a"}}},
		{Data: []Value{{Kind: KindText, S: "b"}}},
		{Data: []Value{{Kind: KindText, S: "c"}}},
		{Data: []Value{{Kind: KindText, S: "b"}}},
		{Data: []Value{{Kind: KindText, S: "d"}}},
	}
	schema := []string{"s"}
	types := []LX.TokenType{LX.T_TEXT}

	ps := NewParallelIndexRangeScan(rows, schema, types, "s",
		[]any{"b", "d"}, pool)
	defer ps.Close()

	var results []string
	for {
		r, err := ps.Next(ctx)
		if err != nil {
			break
		}
		results = append(results, r.Data[0].S)
	}
	if len(results) != 3 {
		t.Errorf("got %d rows, want 3", len(results))
	}
}

// REQ001051: ParallelIndexRangeScan with multiple columns filters by correct column.
func TestParallelIndexRangeScan_MultiCol(t *testing.T) {
	pool := 
UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	rows := make([]Row, 10)
	for i := range rows {
		rows[i] = Row{Data: []Value{
			{Kind: KindInt, I64: int64(i)},
			{Kind: KindText, S: fmt.Sprintf("val%d", i)},
		}}
	}
	schema := []string{"id", "name"}
	types := []LX.TokenType{LX.T_INT_KW, LX.T_TEXT}

	// Filter by id IN (2, 5, 8)
	ps := NewParallelIndexRangeScan(rows, schema, types, "id",
		[]any{int64(2), int64(5), int64(8)}, pool)
	defer ps.Close()

	var results []string
	for {
		r, err := ps.Next(ctx)
		if err != nil {
			break
		}
		results = append(results, r.Data[1].S)
	}
	if len(results) != 3 {
		t.Errorf("got %d rows, want 3", len(results))
	}
	expected := map[string]bool{"val2": true, "val5": true, "val8": true}
	for _, v := range results {
		if !expected[v] {
			t.Errorf("unexpected value %q", v)
		}
	}
}

// REQ001051: ParallelIndexRangeScan no match returns empty.
func TestParallelIndexRangeScan_NoMatch(t *testing.T) {
	pool := 
UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	rows := []Row{
		{Data: []Value{{Kind: KindInt, I64: 1}}},
		{Data: []Value{{Kind: KindInt, I64: 2}}},
	}
	ps := NewParallelIndexRangeScan(rows, []string{"val"}, []LX.TokenType{LX.T_INT_KW}, "val",
		[]any{int64(99), int64(100)}, pool)
	defer ps.Close()

	_, err := ps.Next(ctx)
	if err == nil {
		t.Error("expected ErrNoRows for no match")
	}
}

// REQ001051: ParallelIndexRangeScan single worker still works.
func TestParallelIndexRangeScan_SingleWorker(t *testing.T) {
	pool := 
UT.NewWorkerPool(1)
	defer pool.Close()
	ctx := context.Background()

	rows := make([]Row, 50)
	for i := range rows {
		rows[i] = Row{Data: []Value{{Kind: KindInt, I64: int64(i)}}}
	}
	ps := NewParallelIndexRangeScan(rows, []string{"val"}, []LX.TokenType{LX.T_INT_KW}, "val",
		[]any{int64(0), int64(25), int64(49)}, pool)
	defer ps.Close()

	count := 0
	for {
		_, err := ps.Next(ctx)
		if err != nil {
			break
		}
		count++
	}
	if count != 3 {
		t.Errorf("got %d rows, want 3", count)
	}
}

// BenchmarkParallelIndexRangeScan_INList_100Values benchmarks 100-value IN-list.
func BenchmarkParallelIndexRangeScan_100Values(b *testing.B) {
	pool := 
UT.NewWorkerPool(2)
	defer pool.Close()
	ctx := context.Background()

	n := 10000
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{Data: []Value{{Kind: KindInt, I64: int64(i)}}}
	}

	values := make([]any, 100)
	for i := 0; i < 100; i++ {
		values[i] = int64(i * 10)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ps := NewParallelIndexRangeScan(rows, []string{"val"}, []LX.TokenType{LX.T_INT_KW}, "val", values, pool)
		for {
			_, err := ps.Next(ctx)
			if err != nil {
				break
			}
		}
		ps.Close()
	}
}
