package EX

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestEvalBatch_8Wide compares the 8-wide path against the
// 4-wide path to ensure they produce identical selection
// vectors. REQ000310.
func TestEvalBatch_8Wide(t *testing.T) {
	b := makeIntBatch(nil)
	defer b.Put()
	for i := int64(0); i < 100; i++ {
		b.AppendRow(0, LX.T_INT_KW, i, false)
		b.AdvanceSize()
	}
	b.SetColMap(map[string]int{"x": 0})

	expr := &PS.BinaryExpr{
		Op:    LX.T_LT,
		Left:  &PS.Ident{Name: "x"},
		Right: &PS.NumberLiteral{Val: 50},
	}
	sel := EvalBatch(expr, b, nil)
	if len(sel) != 50 {
		t.Errorf("expected 50 rows, got %d", len(sel))
	}
	for i, idx := range sel {
		if int(idx) != i {
			t.Errorf("sel[%d]=%d, want %d", i, idx, i)
		}
	}
}
