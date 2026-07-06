// REQ001246: TopN operator tests and benchmark.
package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestTopNSort_Asc verifies that TopNSort returns the K smallest values
// in ascending order.
func TestTopNSort_Asc(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t", []DT.Row{
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(3))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(1))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(5))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(2))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(4))}},
	})

	ctx := context.Background()
	key := &PS.OrderItem{
		Expr: &PS.Ident{Name: "v"},
		Desc: false,
	}
	scan := OP.NewSeqScan("t")
	topn := OP.NewTopNSort(scan, []PS.OrderItem{*key}, 3)

	want := []int64{1, 2, 3}
	for i, w := range want {
		row, err := topn.Next(ctx)
		if err != nil {
			t.Fatalf("Next #%d: %v", i, err)
		}
		if v, ok := row.Data[0].Int64(); !ok || v != w {
			t.Errorf("row %d: expected %d, got %v", i, w, row.Data[0])
		}
	}
	// Next should return ErrNoRows after consuming all results.
	_, err := topn.Next(ctx)
	if err != OP.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	_ = topn.Close()
}

// TestTopNSort_Desc verifies that TopNSort returns the K largest values
// in descending order.
func TestTopNSort_Desc(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t", []DT.Row{
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(3))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(1))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(5))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(2))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(4))}},
	})

	ctx := context.Background()
	key := &PS.OrderItem{
		Expr: &PS.Ident{Name: "v"},
		Desc: true,
	}
	scan := OP.NewSeqScan("t")
	topn := OP.NewTopNSort(scan, []PS.OrderItem{*key}, 3)

	want := []int64{5, 4, 3}
	for i, w := range want {
		row, err := topn.Next(ctx)
		if err != nil {
			_ = topn.Close()
			t.Fatalf("Next #%d: %v", i, err)
		}
		if v, ok := row.Data[0].Int64(); !ok || v != w {
			t.Errorf("row %d: expected %d, got %v", i, w, row.Data[0])
		}
	}
	_, err := topn.Next(ctx)
	if err != OP.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	_ = topn.Close()
}

// TestTopNSort_SmallDataset verifies that when K > N, all rows are returned.
func TestTopNSort_SmallDataset(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTable("t", []DT.Row{
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(3))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(1))}},
		{Cols: []string{"v"}, Data: []DT.Value{NewIntValue(int64(2))}},
	})

	ctx := context.Background()
	key := &PS.OrderItem{
		Expr: &PS.Ident{Name: "v"},
		Desc: false,
	}
	scan := OP.NewSeqScan("t")
	topn := OP.NewTopNSort(scan, []PS.OrderItem{*key}, 100)

	want := []int64{1, 2, 3}
	for i, w := range want {
		row, err := topn.Next(ctx)
		if err != nil {
			t.Fatalf("Next #%d: %v", i, err)
		}
		if v, ok := row.Data[0].Int64(); !ok || v != w {
			t.Errorf("row %d: expected %d, got %v", i, w, row.Data[0])
		}
	}
	_, err := topn.Next(ctx)
	if err != OP.ErrNoRows {
		_ = topn.Close()
		t.Errorf("expected ErrNoRows, got %v", err)
	}
	_ = topn.Close()
}