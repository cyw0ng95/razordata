// Package EX join strategy tests.
//
// REQ000980: verifies JoinStrategy interface conformance for all
// strategy types and that JoinStrategy-based dispatch is
// type-discriminating.
package EX

import (
	"context"
	"errors"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestJoinStrategy_InterfaceConformance is a compile-time guard
// ensuring all strategy types implement the JoinStrategy interface.
func TestJoinStrategy_InterfaceConformance(t *testing.T) {
	var _ JoinStrategy = (*InnerNLJStrategy)(nil)
	var _ JoinStrategy = (*HashNLJStrategy)(nil)
	var _ JoinStrategy = (*BlockNLJStrategy)(nil)
	var _ JoinStrategy = (*LeftOuterNLJStrategy)(nil)
	var _ JoinStrategy = (*RightOuterNLJStrategy)(nil)
	var _ JoinStrategy = (*HashCrossNLJStrategy)(nil)
}

// TestJoinStrategy_EmptyStreamsReturnNoRows verifies that all
// strategies return ErrNoRows when the underlying operators are
// exhausted. This is the common "end of input" behavior; the
// strategies in the JoinStrategy interface are markers and the
// production code paths live in NestedLoopJoin.{hash,block,
// leftOuter,rightOuter,hashCross}Mode.
func TestJoinStrategy_EmptyStreamsReturnNoRows(t *testing.T) {
	strategies := []JoinStrategy{
		NewHashNLJStrategy(emptyOp{}, emptyOp{}, nil, nil),
		NewBlockNLJStrategy(emptyOp{}, emptyOp{}, nil, 32),
		NewLeftOuterNLJStrategy(emptyOp{}, emptyOp{}, nil),
		NewRightOuterNLJStrategy(emptyOp{}, emptyOp{}, nil),
		NewHashCrossNLJStrategy(emptyOp{}, emptyOp{}),
	}
	for i, s := range strategies {
		_, err := s.Next(context.Background())
		if !errors.Is(err, DT.ErrNoRows) {
			t.Errorf("strategy %d: got err=%v, want ErrNoRows", i, err)
		}
		if err := s.Close(); err != nil {
			t.Errorf("strategy %d Close: %v", i, err)
		}
	}
}

// TestJoinStrategy_InnerNLJ_Smoke drives the InnerNLJStrategy over
// a tiny in-memory scan pair (2x3) and verifies the cartesian
// product is produced correctly.
func TestJoinStrategy_InnerNLJ_Smoke(t *testing.T) {
	left := &memScan{rows: []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(1)}},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(2)}},
	}}
	right := &memScan{rows: []Row{
		{Cols: []string{"b"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(10)}},
		{Cols: []string{"b"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(20)}},
		{Cols: []string{"b"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(30)}},
	}}
	strat := NewInnerNLJStrategy(left, right, nil)
	defer strat.Close()
	ctx := context.Background()
	got := 0
	for {
		row, err := strat.Next(ctx)
		if errors.Is(err, DT.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if len(row.Data) != 2 {
			t.Errorf("row.Data len = %d, want 2", len(row.Data))
		}
		got++
	}
	if got != 6 {
		t.Errorf("got %d joined rows, want 6 (2x3)", got)
	}
}

// TestJoinStrategy_InnerNLJ_Limit verifies the SetLimit budget
// caps emission.
func TestJoinStrategy_InnerNLJ_Limit(t *testing.T) {
	left := &memScan{rows: []Row{
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(1)}},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(2)}},
		{Cols: []string{"a"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(3)}},
	}}
	right := &memScan{rows: []Row{
		{Cols: []string{"b"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(10)}},
		{Cols: []string{"b"}, Types: []LX.TokenType{LX.T_INT_KW}, Data: []Value{NewIntValue(20)}},
	}}
	strat := NewInnerNLJStrategy(left, right, nil)
	strat.SetLimit(3)
	defer strat.Close()
	ctx := context.Background()
	got := 0
	for {
		_, err := strat.Next(ctx)
		if errors.Is(err, DT.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got++
	}
	if got != 3 {
		t.Errorf("got %d rows under limit, want 3", got)
	}
}

// memScan is a minimal in-memory pl.Operator used by the strategy
// tests. It satisfies the pl.Operator interface (Next, Close,
// WithParams).
type memScan struct {
	rows []Row
	pos  int
}

func (m *memScan) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if m.pos >= len(m.rows) {
		return Row{}, DT.ErrNoRows
	}
	r := m.rows[m.pos]
	m.pos++
	return r, nil
}

func (m *memScan) Close() error { return nil }

func (m *memScan) WithParams(p []any) pl.Operator { m.rows = nil; return m }

// emptyOp is an pl.Operator that immediately returns ErrNoRows. It
// is used to drive strategy boundary cases.
type emptyOp struct{}

func (emptyOp) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, DT.ErrNoRows
}
func (emptyOp) Close() error                   { return nil }
func (emptyOp) WithParams(p []any) pl.Operator { return emptyOp{} }
func TestJOIN_DuplicateRows(t *testing.T) {
	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t1 (id INTEGER PRIMARY KEY, val INTEGER)",
		"CREATE TABLE t2 (id INTEGER PRIMARY KEY, val INTEGER)",
		"INSERT INTO t1 VALUES (1, 10), (2, 20), (3, 30)",
		"INSERT INTO t2 VALUES (1, 100), (2, 200)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT * FROM t1, t2")
	if err != nil {
		t.Fatalf("cross join: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("CROSS JOIN: got %d rows, want 6; data=%v", len(rows), rows)
	}

	rows, err = e.QueryAll(ctx, "SELECT * FROM t1 CROSS JOIN t2")
	if err != nil {
		t.Fatalf("cross join: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("CROSS JOIN explicit: got %d rows, want 6", len(rows))
	}

	rows, err = e.QueryAll(ctx, "SELECT * FROM t1 INNER JOIN t2 ON t1.id = t2.id")
	if err != nil {
		t.Fatalf("inner join: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("INNER JOIN: got %d rows, want 2; data=%v", len(rows), rows)
	}

	rows, err = e.QueryAll(ctx, "SELECT * FROM t1 LEFT JOIN t2 ON t1.id = t2.id")
	if err != nil {
		t.Fatalf("left join: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("LEFT JOIN: got %d rows, want 3", len(rows))
	}
}

// TestNLJ_SelfJoin_DataIndependence verifies that a self-join cross product
// produces 9 distinct rows with correct data (REQ000961).
func TestNLJ_SelfJoin_DataIndependence(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	ctx := context.Background()

	for _, s := range []string{
		"CREATE TABLE t (id INTEGER, val INTEGER)",
		"INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)",
	} {
		if _, err := e.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	rows, err := e.QueryAll(ctx, "SELECT a.id, a.val, b.id, b.val FROM t a, t b ORDER BY a.id, b.id")
	if err != nil {
		t.Fatalf("self-join: %v", err)
	}
	if len(rows) != 9 {
		t.Fatalf("expected 9 rows (3×3), got %d", len(rows))
	}

	// Verify all 9 combinations are correct.
	expected := []struct {
		aID, aVal, bID, bVal int64
	}{
		{1, 10, 1, 10}, {1, 10, 2, 20}, {1, 10, 3, 30},
		{2, 20, 1, 10}, {2, 20, 2, 20}, {2, 20, 3, 30},
		{3, 30, 1, 10}, {3, 30, 2, 20}, {3, 30, 3, 30},
	}
	for i, exp := range expected {
		r := rows[i]
		if len(r.Data) != 4 {
			t.Fatalf("row %d: expected 4 cols, got %d", i, len(r.Data))
		}
		got := []int64{
			r.Data[0].ToAny().(int64),
			r.Data[1].ToAny().(int64),
			r.Data[2].ToAny().(int64),
			r.Data[3].ToAny().(int64),
		}
		want := []int64{exp.aID, exp.aVal, exp.bID, exp.bVal}
		if got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
			t.Fatalf("row %d: got %v, want %v", i, got, want)
		}
	}
}
