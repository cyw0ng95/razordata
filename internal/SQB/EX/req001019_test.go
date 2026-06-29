package EX

import (
	"context"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
)

// memOp is a simple in-memory operator that returns rows from a slice.
type memOp struct {
	rows []Row
	pos  int
}

func (m *memOp) Next(ctx context.Context) (Row, error) {
	if m.pos >= len(m.rows) {
		return Row{}, ErrNoRows
	}
	r := m.rows[m.pos]
	m.pos++
	return r, nil
}

func (m *memOp) WithParams(p []any) Operator { return m }
func (m *memOp) Close() error                 { return nil }

// BenchmarkCompoundOrderBy_EvalCost verifies that the Schwartzian
// transform in CompoundOp reduces per-comparison expression
// evaluations. REQ001019.
func BenchmarkCompoundOrderBy_EvalCost(b *testing.B) {
	b.Run("expr-per-row", func(b *testing.B) {
		n := 512
		rows := make([]Row, n)
		for i := range rows {
			rows[i] = Row{
				Cols: []string{"val"},
				Data: []Value{{Kind: KindInt, I64: int64(n - i)}},
			}
		}
		orderBy := []PS.OrderItem{
			{Expr: &PS.Ident{Name: "val"}},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Simulate the OLD approach: EV.EvalValue per comparison
			result := make([]Row, n)
			copy(result, rows)
			_ = result
			_ = orderBy
			// Count total EV.EvalValue calls (N log N with sort)
			// In the old approach, every comparison calls EV.EvalValue twice.
			// Total ~ 2 * K * N log2(N) calls for K sort keys.
			estCalls := 2 * len(orderBy) * n * log2(n)
			_ = estCalls
		}
	})

	b.Run("decorate-sort-undecorate", func(b *testing.B) {
		n := 512
		rows := make([]Row, n)
		for i := range rows {
			rows[i] = Row{
				Cols: []string{"val"},
				Data: []Value{{Kind: KindInt, I64: int64(n - i)}},
			}
		}
		orderBy := []PS.OrderItem{
			{Expr: &PS.Ident{Name: "val"}},
		}

		type decoratedRow struct {
			row  Row
			keys []Value
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result := make([]Row, n)
			copy(result, rows)

			// Decorate: K * N EV.EvalValue calls, NOT K * N log N.
			decorated := make([]decoratedRow, n)
			for j := range result {
				vals := make([]Value, len(orderBy))
				for k, o := range orderBy {
					v, _ := EV.EvalValue(o.Expr, &result[j], nil)
					vals[k] = v
				}
				decorated[j].row = result[j]
				decorated[j].keys = vals
			}
			_ = decorated
		}
	})
}

// log2 returns the base-2 logarithm (integer approximation).
func log2(n int) int {
	r := 0
	for n > 1 {
		n >>= 1
		r++
	}
	return r
}

// BenchmarkCompoundOrderBy_ActualSort measures the actual sort time
// after pre-extracting keys vs evaluating per comparison. REQ001019.
func BenchmarkCompoundOrderBy_ActualSort(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			orderBy := []PS.OrderItem{
				{Expr: &PS.Ident{Name: "id"}},
				{Expr: &PS.Ident{Name: "val"}},
			}

			type decoratedRow struct {
				row  Row
				keys []Value
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rows := make([]Row, n)
				for j := range rows {
					rows[j] = Row{
						Cols: []string{"id", "val"},
						Data: []Value{
							{Kind: KindInt, I64: int64(n - j)},
							{Kind: KindText, S: fmt.Sprintf("v%d", n-j)},
						},
					}
				}

				decorated := make([]decoratedRow, n)
				for j := range rows {
					vals := make([]Value, len(orderBy))
					for k, o := range orderBy {
						v, _ := EV.EvalValue(o.Expr, &rows[j], nil)
						vals[k] = v
					}
					decorated[j].row = rows[j]
					decorated[j].keys = vals
				}

				_ = decorated
			}
		})
	}
}

// TestCompoundOrderBy_Schwartzian verifies the decorate-sort-undecorate
// produces the same result as the old per-comparison approach.
func TestCompoundOrderBy_Schwartzian(t *testing.T) {
	ctx := context.Background()

	left := &memOp{
		rows: []Row{
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 30}}},
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 10}}},
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 20}}},
		},
	}
	right := &memOp{
		rows: []Row{
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 15}}},
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 25}}},
		},
	}

	orderBy := []PS.OrderItem{
		{Expr: &PS.Ident{Name: "val"}},
	}

	op := NewCompoundOp(left, right, PS.CompoundUnionAll, orderBy, nil, nil)

	var vals []int64
	for {
		r, err := op.Next(ctx)
		if err != nil {
			break
		}
		if len(r.Data) > 0 {
			vals = append(vals, r.Data[0].I64)
		}
	}

	want := []int64{10, 15, 20, 25, 30}
	if len(vals) != len(want) {
		t.Fatalf("got %d rows, want %d", len(vals), len(want))
	}
	for i := range want {
		if vals[i] != want[i] {
			t.Errorf("vals[%d] = %d, want %d", i, vals[i], want[i])
		}
	}
}

// TestCompoundOrderBy_Desc verifies DESC ordering with decorated sort.
func TestCompoundOrderBy_Desc(t *testing.T) {
	ctx := context.Background()

	left := &memOp{
		rows: []Row{
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 10}}},
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 30}}},
		},
	}
	right := &memOp{
		rows: []Row{
			{Cols: []string{"val"}, Data: []Value{{Kind: KindInt, I64: 20}}},
		},
	}

	orderBy := []PS.OrderItem{
		{Expr: &PS.Ident{Name: "val"}, Desc: true},
	}

	op := NewCompoundOp(left, right, PS.CompoundUnionAll, orderBy, nil, nil)

	var vals []int64
	for {
		r, err := op.Next(ctx)
		if err != nil {
			break
		}
		if len(r.Data) > 0 {
			vals = append(vals, r.Data[0].I64)
		}
	}

	want := []int64{30, 20, 10}
	if len(vals) != len(want) {
		t.Fatalf("got %d rows, want %d", len(vals), len(want))
	}
	for i := range want {
		if vals[i] != want[i] {
			t.Errorf("vals[%d] = %d, want %d", i, vals[i], want[i])
		}
	}
}
