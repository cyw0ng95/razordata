package EX

import (
	"context"
	"strings"
	"testing"
)

func TestOffset_Operator(t *testing.T) {
	src := &sliceOp{rows: []Row{
		{Cols: []string{"a"}, Data: []interface{}{int64(1)}},
		{Cols: []string{"a"}, Data: []interface{}{int64(2)}},
		{Cols: []string{"a"}, Data: []interface{}{int64(3)}},
		{Cols: []string{"a"}, Data: []interface{}{int64(4)}},
	}}
	off := NewOffset(src, 2)
	defer off.Close()
	ctx := context.Background()
	var got []int64
	for {
		row, err := off.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, row.Data[0].(int64))
	}
	want := []int64{3, 4}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestOffset_ExceedingRows(t *testing.T) {
	src := &sliceOp{rows: []Row{
		{Cols: []string{"a"}, Data: []interface{}{int64(1)}},
	}}
	off := NewOffset(src, 5)
	defer off.Close()
	ctx := context.Background()
	for {
		_, err := off.Next(ctx)
		if err == ErrNoRows {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLimitOffset_Combined(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"a"})
	ctx := context.Background()
	for _, v := range []string{
		"INSERT INTO t VALUES (1)",
		"INSERT INTO t VALUES (2)",
		"INSERT INTO t VALUES (3)",
		"INSERT INTO t VALUES (4)",
	} {
		if _, err := ex.Exec(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT a FROM t LIMIT 2 OFFSET 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got := []int64{rows[0].Data[0].(int64), rows[1].Data[0].(int64)}
	if got[0] != 2 || got[1] != 3 {
		t.Errorf("got %v, want [2 3]", got)
	}
}

func TestPlanSelect_OrderByPKDropsSort(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "name"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'alice')",
		"INSERT INTO t VALUES (2, 'bob')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := ex.Explain("SELECT * FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan, "Sort") {
		t.Errorf("expected no Sort for ORDER BY pk, got plan:\n%s", plan)
	}
}

func TestPlanSelect_OrderByNonPKKeepsSort(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "name"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 'alice')",
		"INSERT INTO t VALUES (2, 'bob')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := ex.Explain("SELECT * FROM t ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "Sort") {
		t.Errorf("expected Sort for ORDER BY non-pk, got plan:\n%s", plan)
	}
}

func TestPlanSelect_OrderByPKDescKeepsSort(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "name"}, "id")
	plan, err := ex.Explain("SELECT * FROM t ORDER BY id DESC")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "Sort") {
		t.Errorf("expected Sort for ORDER BY pk DESC (engine does not yield reverse order), got plan:\n%s", plan)
	}
}

type sliceOp struct {
	rows []Row
	pos  int
}

func (s *sliceOp) Next(ctx context.Context) (Row, error) {
	if s.pos >= len(s.rows) {
		return Row{}, ErrNoRows
	}
	r := s.rows[s.pos]
	s.pos++
	return r, nil
}

func (s *sliceOp) Close() error { return nil }
