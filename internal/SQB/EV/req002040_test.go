package EV

import (
	"math"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// makeIntArithBatch creates a batch with two int64 columns for arithmetic tests.
func makeIntArithBatch(a, b []int64) *UT.Batch {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	batch := UT.GetBatch(2)
	batch.SetColumnName(0, "a")
	batch.SetColumnName(1, "b")
	batch.Cols[0].Type = LX.T_INT_KW
	batch.Cols[0].Data.Ints = make([]int64, n)
	copy(batch.Cols[0].Data.Ints, a)
	batch.Cols[1].Type = LX.T_INT_KW
	batch.Cols[1].Data.Ints = make([]int64, n)
	copy(batch.Cols[1].Data.Ints, b)
	batch.Size = n
	return batch
}

func TestREQ002040_AddOverflow(t *testing.T) {
	// MaxInt64 + 1 should produce NULL, not wrap to MinInt64
	batch := makeIntArithBatch(
		[]int64{math.MaxInt64, 1, 100},
		[]int64{1, math.MaxInt64, 200},
	)
	defer batch.Put()

	expr := &PS.BinaryExpr{Op: LX.T_PLUS, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
	col := EvalBatchExpr(expr, batch, nil)

	// Row 0: MaxInt64 + 1 → overflow → NULL
	if col.Nulls == nil || !col.Nulls[0] {
		t.Errorf("row 0: expected NULL for MaxInt64+1, got %d", col.Data.Ints[0])
	}
	// Row 1: 1 + MaxInt64 → overflow → NULL
	if col.Nulls == nil || !col.Nulls[1] {
		t.Errorf("row 1: expected NULL for 1+MaxInt64, got %d", col.Data.Ints[1])
	}
	// Row 2: 100 + 200 → 300
	if col.Nulls != nil && col.Nulls[2] {
		t.Error("row 2: unexpected NULL for 100+200")
	}
	if col.Data.Ints[2] != 300 {
		t.Errorf("row 2: got %d, want 300", col.Data.Ints[2])
	}
}

func TestREQ002040_SubOverflow(t *testing.T) {
	// MinInt64 - 1 should produce NULL, not wrap to MaxInt64
	batch := makeIntArithBatch(
		[]int64{math.MinInt64, 0},
		[]int64{1, -1},
	)
	defer batch.Put()

	expr := &PS.BinaryExpr{Op: LX.T_MINUS, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
	col := EvalBatchExpr(expr, batch, nil)

	// Row 0: MinInt64 - 1 → overflow → NULL
	if col.Nulls == nil || !col.Nulls[0] {
		t.Errorf("row 0: expected NULL for MinInt64-1, got %d", col.Data.Ints[0])
	}
	// Row 1: 0 - (-1) = 1
	if col.Nulls != nil && col.Nulls[1] {
		t.Error("row 1: unexpected NULL for 0-(-1)")
	}
	if col.Data.Ints[1] != 1 {
		t.Errorf("row 1: got %d, want 1", col.Data.Ints[1])
	}
}

func TestREQ002040_MulOverflow(t *testing.T) {
	// MaxInt64 * 2 should produce NULL
	batch := makeIntArithBatch(
		[]int64{math.MaxInt64, math.MinInt64, 5, -1},
		[]int64{2, -1, 6, math.MinInt64},
	)
	defer batch.Put()

	expr := &PS.BinaryExpr{Op: LX.T_STAR, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
	col := EvalBatchExpr(expr, batch, nil)

	// Row 0: MaxInt64 * 2 → overflow → NULL
	if col.Nulls == nil || !col.Nulls[0] {
		t.Errorf("row 0: expected NULL for MaxInt64*2, got %d", col.Data.Ints[0])
	}
	// Row 1: MinInt64 * -1 → overflow → NULL
	if col.Nulls == nil || !col.Nulls[1] {
		t.Errorf("row 1: expected NULL for MinInt64*-1, got %d", col.Data.Ints[1])
	}
	// Row 2: 5 * 6 = 30
	if col.Nulls != nil && col.Nulls[2] {
		t.Error("row 2: unexpected NULL for 5*6")
	}
	if col.Data.Ints[2] != 30 {
		t.Errorf("row 2: got %d, want 30", col.Data.Ints[2])
	}
	// Row 3: -1 * MinInt64 → overflow → NULL
	if col.Nulls == nil || !col.Nulls[3] {
		t.Errorf("row 3: expected NULL for -1*MinInt64, got %d", col.Data.Ints[3])
	}
}

func TestREQ002040_MulZero(t *testing.T) {
	// Anything * 0 = 0 (no overflow)
	batch := makeIntArithBatch(
		[]int64{math.MaxInt64, math.MinInt64, 0},
		[]int64{0, 0, math.MaxInt64},
	)
	defer batch.Put()

	expr := &PS.BinaryExpr{Op: LX.T_STAR, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
	col := EvalBatchExpr(expr, batch, nil)

	for i, want := range []int64{0, 0, 0} {
		if col.Nulls != nil && col.Nulls[i] {
			t.Errorf("row %d: unexpected NULL", i)
			continue
		}
		if col.Data.Ints[i] != want {
			t.Errorf("row %d: got %d, want %d", i, col.Data.Ints[i], want)
		}
	}
}

func TestREQ002040_DivNoOverflow(t *testing.T) {
	// Division by zero → NULL (already handled), not overflow
	batch := makeIntArithBatch(
		[]int64{10, 10},
		[]int64{2, 0},
	)
	defer batch.Put()

	expr := &PS.BinaryExpr{Op: LX.T_SLASH, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
	col := EvalBatchExpr(expr, batch, nil)

	// Row 0: 10 / 2 = 5
	if col.Data.Ints[0] != 5 {
		t.Errorf("row 0: got %d, want 5", col.Data.Ints[0])
	}
	// Row 1: 10 / 0 → NULL
	if col.Nulls == nil || !col.Nulls[1] {
		t.Errorf("row 1: expected NULL for division by zero, got %d", col.Data.Ints[1])
	}
}

func TestREQ002040_AddMinInt64(t *testing.T) {
	// MinInt64 + (-1) should overflow
	batch := makeIntArithBatch(
		[]int64{math.MinInt64},
		[]int64{-1},
	)
	defer batch.Put()

	expr := &PS.BinaryExpr{Op: LX.T_PLUS, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
	col := EvalBatchExpr(expr, batch, nil)

	if col.Nulls == nil || !col.Nulls[0] {
		t.Errorf("expected NULL for MinInt64+(-1), got %d", col.Data.Ints[0])
	}
}

func TestREQ002040_MulTwoNegatives(t *testing.T) {
	// Large negative * large negative can overflow
	batch := makeIntArithBatch(
		[]int64{math.MinInt64 + 1, -2},
		[]int64{-2, 3},
	)
	defer batch.Put()

	expr := &PS.BinaryExpr{Op: LX.T_STAR, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
	col := EvalBatchExpr(expr, batch, nil)

	// Row 0: (MinInt64+1) * -2 = overflow (positive result > MaxInt64)
	if col.Nulls == nil || !col.Nulls[0] {
		t.Errorf("row 0: expected NULL for (MinInt64+1)*-2, got %d", col.Data.Ints[0])
	}
	// Row 1: -2 * 3 = -6
	if col.Data.Ints[1] != -6 {
		t.Errorf("row 1: got %d, want -6", col.Data.Ints[1])
	}
}

func TestREQ002040_MatchesNumericArithValue(t *testing.T) {
	// Cross-validate: batch result should match NumericArithValue for same inputs
	cases := []struct {
		a, b int64
		op   rune
	}{
		{math.MaxInt64, 1, '+'},
		{1, math.MaxInt64, '+'},
		{math.MinInt64, -1, '+'},
		{math.MinInt64, 1, '-'},
		{math.MaxInt64, -1, '-'},
		{math.MaxInt64, 2, '*'},
		{math.MinInt64, -1, '*'},
		{-1, math.MinInt64, '*'},
		{100, 200, '+'},
		{100, 50, '-'},
		{7, 6, '*'},
	}

	for _, tc := range cases {
		batch := makeIntArithBatch([]int64{tc.a}, []int64{tc.b})
		var tok LX.TokenType
		switch tc.op {
		case '+':
			tok = LX.T_PLUS
		case '-':
			tok = LX.T_MINUS
		case '*':
			tok = LX.T_STAR
		}

		expr := &PS.BinaryExpr{Op: tok, Left: &PS.Ident{Name: "a"}, Right: &PS.Ident{Name: "b"}}
		batchCol := EvalBatchExpr(expr, batch, nil)

		av := DT.NewIntValue(tc.a)
		bv := DT.NewIntValue(tc.b)
		rowVal, _ := NumericArithValue(av, bv, tc.op)

		batchIsNull := batchCol.Nulls != nil && batchCol.Nulls[0]
		rowIsNull := rowVal.Kind == DT.KindNull

		if batchIsNull != rowIsNull {
			t.Errorf("mismatch for %d %c %d: batch null=%v, row null=%v", tc.a, tc.op, tc.b, batchIsNull, rowIsNull)
		}
		if !batchIsNull && !rowIsNull {
			if batchCol.Data.Ints[0] != rowVal.I64 {
				t.Errorf("mismatch for %d %c %d: batch=%d, row=%d", tc.a, tc.op, tc.b, batchCol.Data.Ints[0], rowVal.I64)
			}
		}

		batch.Put()
	}
}
