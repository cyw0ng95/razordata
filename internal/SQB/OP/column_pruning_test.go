package OP

import (
	"context"
	"fmt"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func registerMemTable(t testing.TB, table string, rows []PL.Row) {
	t.Helper()
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	if DT.Tables == nil {
		DT.Tables = make(map[string][]PL.Row)
	}
	DT.Tables[table] = rows
}

func unregisterMemTable(t testing.TB, table string) {
	t.Helper()
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	delete(DT.Tables, table)
}

// TestSeqScan_ColumnPruning_Select1Patterns verifies that
// RequestedCols pruning on SeqScan produces output rows with only
// the projected columns. REQ001412 / REQ001080.
func TestSeqScan_ColumnPruning_Select1Patterns(t *testing.T) {
	table := "t_prune"
	src := []PL.Row{
		{Cols: []string{"id", "name", "age"}, Data: []Value{DT.NewIntValue(1), DT.NewTextValue("alice"), DT.NewIntValue(30)}},
		{Cols: []string{"id", "name", "age"}, Data: []Value{DT.NewIntValue(2), DT.NewTextValue("bob"), DT.NewIntValue(25)}},
		{Cols: []string{"id", "name", "age"}, Data: []Value{DT.NewIntValue(3), DT.NewTextValue("carol"), DT.NewIntValue(35)}},
	}
	registerMemTable(t, table, src)
	defer unregisterMemTable(t, table)

	t.Run("project_2_of_3_cols", func(t *testing.T) {
		s := NewSeqScan(table)
		s.RequestedCols = []int{0, 2} // id, age only

		ctx := context.Background()
		var rows []PL.Row
		for {
			row, err := s.Next(ctx)
			if err == PL.ErrNoRows {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			rows = append(rows, row)
		}
		if len(rows) != 3 {
			t.Fatalf("got %d rows, want 3", len(rows))
		}
		for i, r := range rows {
			if len(r.Cols) != 2 {
				t.Errorf("row %d: len(Cols)=%d, want 2", i, len(r.Cols))
			}
			if len(r.Data) != 2 {
				t.Errorf("row %d: len(Data)=%d, want 2", i, len(r.Data))
			}
		}
	})

	t.Run("no_pruning_full_row", func(t *testing.T) {
		s := NewSeqScan(table)
		s.RequestedCols = nil // no pruning

		ctx := context.Background()
		var rows []PL.Row
		for {
			row, err := s.Next(ctx)
			if err == PL.ErrNoRows {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			rows = append(rows, row)
		}
		if len(rows) != 3 {
			t.Fatalf("got %d rows, want 3", len(rows))
		}
		for i, r := range rows {
			if len(r.Cols) != 3 {
				t.Errorf("row %d: len(Cols)=%d, want 3", i, len(r.Cols))
			}
			if len(r.Data) != 3 {
				t.Errorf("row %d: len(Data)=%d, want 3", i, len(r.Data))
			}
		}
	})
}

// BenchmarkSeqScan_ColumnPruning_vs_FullScan measures I/O reduction
// when column pruning is active. REQ001412.
func BenchmarkSeqScan_ColumnPruning_vs_FullScan(b *testing.B) {
	table := "b_prune"
	nrows := 1000
	src := make([]PL.Row, nrows)
	for i := range nrows {
		src[i] = PL.Row{
			Cols: []string{"id", "name", "age", "email", "score"},
			Data: []Value{
				DT.NewIntValue(int64(i)),
				DT.NewTextValue("user-" + fmt.Sprintf("%d", i)),
				DT.NewIntValue(int64(i % 100)),
				DT.NewTextValue("user" + fmt.Sprintf("%d", i) + "@test.com"),
				DT.NewFloatValue(float64(i) * 0.5),
			},
		}
	}
	registerMemTable(b, table, src)
	defer unregisterMemTable(b, table)

	ctx := context.Background()

	b.Run("full_scan_5cols", func(b *testing.B) {
		for range b.N {
			s := NewSeqScan(table)
			b.StopTimer()
			s.RequestedCols = nil
			b.StartTimer()
			for {
				_, err := s.Next(ctx)
				if err == PL.ErrNoRows {
					break
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("pruned_2cols", func(b *testing.B) {
		for range b.N {
			s := NewSeqScan(table)
			b.StopTimer()
			s.RequestedCols = []int{0, 2} // id, age
			b.StartTimer()
			for {
				_, err := s.Next(ctx)
				if err == PL.ErrNoRows {
					break
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}