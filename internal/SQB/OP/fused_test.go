package OP

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func makeFusedTable(name string, rows []DT.Row) {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	DT.Tables[name] = rows
}

func TestFusedScan_SelectAll(t *testing.T) {
	makeFusedTable("t_fs1", []DT.Row{
		{Cols: []string{"a", "b"}, Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(10)}},
		{Cols: []string{"a", "b"}, Data: []DT.Value{DT.NewIntValue(2), DT.NewIntValue(20)}},
		{Cols: []string{"a", "b"}, Data: []DT.Value{DT.NewIntValue(3), DT.NewIntValue(30)}},
	})

	f := NewFusedScan("t_fs1", nil, nil)
	if f == nil {
		t.Fatal("NewFusedScan returned nil")
	}
	if err := f.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	ctx := context.Background()
	count := 0
	for {
		_, err := f.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatal(err)
		}
		count++
	}
	if count != 3 {
		t.Errorf("got %d rows, want 3", count)
	}
}

func TestFusedScan_WithFilter(t *testing.T) {
	makeFusedTable("t_fs2", []DT.Row{
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(100)}},
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(200)}},
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(150)}},
	})

	pred := &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "a"},
		Right: &PS.NumberLiteral{Val: 150},
	}
	f := NewFusedScan("t_fs2", pred, nil)
	if f == nil {
		t.Fatal("NewFusedScan returned nil")
	}
	if err := f.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	ctx := context.Background()
	var collected []int64
	for {
		row, err := f.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatal(err)
		}
		collected = append(collected, row.Data[0].I64)
	}
	if len(collected) != 1 || collected[0] != 200 {
		t.Errorf("got %v, want [200]", collected)
	}
}

func TestFusedScan_WithProjection(t *testing.T) {
	makeFusedTable("t_fs3", []DT.Row{
		{Cols: []string{"a", "b"}, Data: []DT.Value{DT.NewIntValue(1), DT.NewIntValue(10)}},
		{Cols: []string{"a", "b"}, Data: []DT.Value{DT.NewIntValue(2), DT.NewIntValue(20)}},
	})

	proj := []PS.Expr{
		&PS.BinaryExpr{
			Op:    LX.T_PLUS,
			Left:  &PS.Ident{Name: "a"},
			Right: &PS.Ident{Name: "b"},
		},
	}
	f := NewFusedScan("t_fs3", nil, proj)
	if f == nil {
		t.Fatal("NewFusedScan returned nil (project compile failed)")
	}
	if err := f.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	ctx := context.Background()
	var got []int64
	for {
		row, err := f.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatal(err)
		}
		got = append(got, row.Data[0].I64)
	}
	if len(got) != 2 || got[0] != 11 || got[1] != 22 {
		t.Errorf("got %v, want [11 22]", got)
	}
}

func TestFusedScan_EmptyTable(t *testing.T) {
	makeFusedTable("t_fs4", []DT.Row{})
	f := NewFusedScan("t_fs4", nil, nil)
	if err := f.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	_, err := f.Next(context.Background())
	if err != ErrNoRows {
		t.Errorf("got err %v, want ErrNoRows", err)
	}
}

func TestFusedScan_Close(t *testing.T) {
	makeFusedTable("t_fs5", []DT.Row{
		{Cols: []string{"a"}, Data: []DT.Value{DT.NewIntValue(1)}},
	})
	f := NewFusedScan("t_fs5", nil, nil)
	if err := f.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Drain to empty.
	for {
		_, err := f.Next(context.Background())
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// After Close, the cursor resets and the source is restored from
	// the snapshot. The next Next() should return the first row.
	f.Close()
	row, err := f.Next(context.Background())
	if err != nil {
		t.Errorf("after Close+reset got err %v, want nil", err)
	}
	if len(row.Data) != 1 || row.Data[0].I64 != 1 {
		t.Errorf("after Close got row %v, want [1]", row.Data[0].I64)
	}
}

func BenchmarkFusedScan_Star(b *testing.B) {
	rows := make([]DT.Row, 20)
	for i := 0; i < 20; i++ {
		rows[i] = DT.Row{
			Cols: []string{"a", "b"},
			Data: []DT.Value{DT.NewIntValue(int64(i)), DT.NewIntValue(int64(i * 10))},
		}
	}
	makeFusedTable("t_fsbench", rows)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := NewFusedScan("t_fsbench", nil, nil)
		_ = f.Open()
		for {
			_, err := f.Next(context.Background())
			if err == ErrNoRows {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		f.Close()
	}
}

func BenchmarkSeqScanFilterProject_Star(b *testing.B) {
	rows := make([]DT.Row, 20)
	for i := 0; i < 20; i++ {
		rows[i] = DT.Row{
			Cols: []string{"a", "b"},
			Data: []DT.Value{DT.NewIntValue(int64(i)), DT.NewIntValue(int64(i * 10))},
		}
	}
	makeFusedTable("t_seqsbench", rows)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate the full SeqScan → Filter(nop) → Project chain.
		scan := NewSeqScan("t_seqsbench")
		filter := NewFilter(scan, nil, nil)
		project := NewProject(filter, nil)
		for {
			_, err := project.Next(context.Background())
			if err == ErrNoRows {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		project.Close()
	}
}
