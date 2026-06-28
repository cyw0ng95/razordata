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

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
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
		if !errors.Is(err, ErrNoRows) {
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
		if errors.Is(err, ErrNoRows) {
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
		if errors.Is(err, ErrNoRows) {
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

// memScan is a minimal in-memory Operator used by the strategy
// tests. It satisfies the Operator interface (Next, Close,
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
		return Row{}, ErrNoRows
	}
	r := m.rows[m.pos]
	m.pos++
	return r, nil
}

func (m *memScan) Close() error { return nil }

func (m *memScan) WithParams(p []any) Operator { m.rows = nil; return m }

// emptyOp is an Operator that immediately returns ErrNoRows. It
// is used to drive strategy boundary cases.
type emptyOp struct{}

func (emptyOp) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	return Row{}, ErrNoRows
}
func (emptyOp) Close() error                    { return nil }
func (emptyOp) WithParams(p []any) Operator     { return emptyOp{} }
