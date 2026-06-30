package OP

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

type memOp struct {
	mu   sync.Mutex
	rows []pl.Row
	pos  int
	done bool
}

func newMemOp(rows []pl.Row) *memOp { return &memOp{rows: rows} }
func (m *memOp) Next(ctx context.Context) (pl.Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done || m.pos >= len(m.rows) {
		m.done = true
		return pl.Row{}, pl.ErrNoRows
	}
	r := m.rows[m.pos]
	m.pos++
	return r, nil
}
func (m *memOp) Close() error { return nil }

func kvr(k, v int64) pl.Row {
	return pl.Row{
		Cols:  []string{"k", "v"},
		Types: []LX.TokenType{LX.T_INT, LX.T_INT},
		Data: []pl.Value{
			{Kind: pl.KindInt, I64: k},
			{Kind: pl.KindInt, I64: v},
		},
	}
}

func collectHashJoin(t *testing.T, j *HashJoin) []pl.Row {
	t.Helper()
	var out []pl.Row
	for {
		row, err := j.Next(context.Background())
		if errors.Is(err, pl.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, row)
	}
	return out
}

// Row layout: [left.k, left.v, right.k, right.v] for 2-column sides.

func TestHashJoin_LeftOuter(t *testing.T) {
	left := newMemOp([]pl.Row{kvr(1, 10), kvr(2, 20), kvr(3, 30)})
	right := newMemOp([]pl.Row{kvr(1, 100), kvr(3, 300)})
	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0).WithKind(JoinKindLeft)
	defer hj.Close()
	rows := collectHashJoin(t, hj)
	// 2 matches + 1 unmatched left = 3 rows.
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// Verify 2 matched rows have both sides populated and 1 unmatched
	// left row has NULL right side.
	var matched, unmatched int
	for _, r := range rows {
		if !r.Data[2].IsNull() {
			matched++
		} else {
			if r.Data[0].I64 == 2 {
				unmatched++
			}
		}
	}
	if matched != 2 {
		t.Errorf("found %d matched rows, want 2", matched)
	}
	if unmatched != 1 {
		t.Errorf("found %d unmatched-left rows (k=2), want 1", unmatched)
	}
}

func TestHashJoin_RightOuter(t *testing.T) {
	left := newMemOp([]pl.Row{kvr(1, 10)})
	right := newMemOp([]pl.Row{kvr(1, 100), kvr(2, 200), kvr(3, 300)})
	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0).WithKind(JoinKindRight)
	defer hj.Close()
	rows := collectHashJoin(t, hj)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// Row 0: match (1,10)+(1,100).
	// Row 1: unmatched right (2,200). Data = [0,0,2,200].
	// Row 2: unmatched right (3,300). Data = [0,0,3,300].
	if rows[0].Data[3].I64 != 100 {
		t.Errorf("row 0 right.v = %d, want 100", rows[0].Data[3].I64)
	}
	// Check the two unmatched right rows (order unspecified).
	var seen200, seen300 bool
	for _, r := range rows[1:] {
		if r.Data[2].I64 == 2 {
			seen200 = true
		}
		if r.Data[2].I64 == 3 {
			seen300 = true
		}
		if !r.Data[0].IsNull() {
			t.Errorf("unmatched right row has non-NULL left: %v", r.Data)
		}
	}
	if !seen200 {
		t.Error("missing unmatched right k=2")
	}
	if !seen300 {
		t.Error("missing unmatched right k=3")
	}
}

func TestHashJoin_FullOuter(t *testing.T) {
	left := newMemOp([]pl.Row{kvr(1, 10), kvr(2, 20)})
	right := newMemOp([]pl.Row{kvr(1, 100), kvr(3, 300)})
	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0).WithKind(JoinKindFull)
	defer hj.Close()
	rows := collectHashJoin(t, hj)

	// Match (1,10)+(1,100), unmatched left (2,20), unmatched right (3,300) = 3.
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	hasUnmatchedLeft := false
	hasUnmatchedRight := false
	for _, r := range rows {
		lk, rk := r.Data[0].I64, r.Data[2].I64
		if lk == 2 && rk == 0 {
			hasUnmatchedLeft = true
		}
		if lk == 0 && rk == 3 {
			hasUnmatchedRight = true
		}
	}
	if !hasUnmatchedLeft {
		t.Error("missing unmatched-left row (k=2)")
	}
	if !hasUnmatchedRight {
		t.Error("missing unmatched-right row (k=3)")
	}
}

func TestHashJoin_InnerUnchanged(t *testing.T) {
	left := newMemOp([]pl.Row{kvr(1, 10), kvr(2, 20), kvr(3, 30)})
	right := newMemOp([]pl.Row{kvr(1, 100), kvr(3, 300)})
	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0)
	defer hj.Close()
	rows := collectHashJoin(t, hj)
	if len(rows) != 2 {
		t.Fatalf("expected 2 matched rows, got %d", len(rows))
	}
}

func TestHashJoin_LeftEmptyRight(t *testing.T) {
	left := newMemOp([]pl.Row{kvr(1, 10), kvr(2, 20)})
	right := newMemOp(nil)
	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0).WithKind(JoinKindLeft)
	defer hj.Close()
	rows := collectHashJoin(t, hj)
	// Both left rows emitted as unmatched-left.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Data[0].I64 != 1 || rows[1].Data[0].I64 != 2 {
		t.Errorf("left keys = [%d %d], want [1 2]", rows[0].Data[0].I64, rows[1].Data[0].I64)
	}
}

func TestHashJoin_RightEmptyLeft(t *testing.T) {
	left := newMemOp(nil)
	right := newMemOp([]pl.Row{kvr(1, 100), kvr(2, 200)})
	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0).WithKind(JoinKindRight)
	defer hj.Close()
	rows := collectHashJoin(t, hj)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (all unmatched-right), got %d", len(rows))
	}
	// Without left rows, the emitBuf width equals the right side only.
	// Just verify the rows have data.
	if rows[0].Data[0].IsNull() {
		t.Errorf("row 0 first cell NULL, expected a right-side value")
	}
}
