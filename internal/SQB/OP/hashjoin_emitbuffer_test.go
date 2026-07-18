// REQ001594 — regression test for select4 L39784 (SLT select4.test join277).
//
// Bug: HashJoin.nextMatched returned every match with Data pointing to the
// same shared emitBuf slice. When a consumer (e.g. NLJ block mode)
// drained all matches into a stored slice before reading them, every
// retained row aliased the backing array and ended up holding the last
// match's values. The 8-table select4 L39784 query collapses 21 rows
// into 7 triplets, all reading identical (t6, t4) values instead of the
// 3 distinct equi-join pairs.
//
// The test below exercises the exact failing pattern at the unit level:
// an inner HashJoin with multiple matches for a single left key, where
// the consumer retains matches across Next() calls without copying.
//
// Run with:
//
//	go test -run TestREQ001594_HashJoinNextMatchedDistinct ./internal/SQB/OP/...
package OP

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// TestREQ001594_HashJoinNextMatchedDistinct emits three matches
// against a single left key (k=1) and asserts that each retained
// match has its own backing array. Prior to the fix every match
// aliased j.emitBuf, so retaining two matches gave two copies of
// the second match's values.
func TestREQ001594_HashJoinNextMatchedDistinct(t *testing.T) {
	left := newMemOp([]pl.Row{
		kvr2(1, "L"),
	})

	right := newMemOp([]pl.Row{
		kvr2(1, "A"),
		kvr2(1, "B"),
		kvr2(1, "C"),
	})

	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0).
		WithKind(JoinKindInner)
	defer hj.Close()

	// Drain all matches WITHOUT per-call deep copies.
	// REQ001594: HashJoin must give each Next() call its own Data
	// slice so the consumer can retain multiple matches.
	var matches []pl.Row
	for i := 0; i < 8; i++ { // over-fetch to ensure we see real matches only
		row, err := hj.Next(context.Background())
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next#%d: %v", i, err)
		}
		matches = append(matches, row)
	}

	if len(matches) != 3 {
		t.Fatalf("want 3 matches, got %d", len(matches))
	}

	want := []string{"A", "B", "C"}
	for i, m := range matches {
		if len(m.Data) != 4 {
			t.Fatalf("match %d: data length = %d, want 4", i, len(m.Data))
		}
		// left.v at index 1, right.v at index 3.
		if got := m.Data[3].S; got != want[i] {
			t.Errorf("match %d right.v = %q, want %q (data aliased across matches)", i, got, want[i])
		}
	}
}

// TestREQ001594_HashJoinNextMatchedDistinctBackingArrays checks
// that retained matches reference DIFFERENT backing arrays. Two
// matches must not share the same underlying slice — a slice-header
// comparison (e.g. &matches[0].Data[0]) catches aliasing.
func TestREQ001594_HashJoinNextMatchedDistinctBackingArrays(t *testing.T) {
	left := newMemOp([]pl.Row{kvr2(1, "L")})
	right := newMemOp([]pl.Row{kvr2(1, "A"), kvr2(1, "B")})

	hj := NewHashJoin(left, right, "l", "r", []string{"k"}, []string{"k"}, 0).
		WithKind(JoinKindInner)
	defer hj.Close()

	var matches []pl.Row
	for i := 0; i < 4; i++ {
		row, err := hj.Next(context.Background())
		if err == pl.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next#%d: %v", i, err)
		}
		matches = append(matches, row)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 matches, got %d", len(matches))
	}
	if len(matches[0].Data) == 0 || len(matches[1].Data) == 0 {
		t.Fatalf("matches have empty data")
	}
	addr0 := &matches[0].Data[0]
	addr1 := &matches[1].Data[0]
	if addr0 == addr1 {
		t.Errorf("matches alias the same backing array: &d[0]==%p for both rows", addr0)
	}
}

// kvr2 is a 2-column key/value row helper.
func kvr2(k int64, v string) pl.Row {
	return pl.Row{
		Cols:  []string{"k", "v"},
		Types: []LX.TokenType{LX.T_INT, LX.T_TEXT},
		Data: []pl.Value{
			{Kind: pl.KindInt, I64: k},
			{Kind: pl.KindText, S: v},
		},
	}
}
