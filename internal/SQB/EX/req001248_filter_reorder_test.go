package EX

import (
	CO "github.com/cyw0ng95/razordata/internal/SQO/CO"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestFilterReorder verifies that predicateCost returns correct heuristic
// costs for various expression types. REQ001248.
func TestFilterReorder(t *testing.T) {
	tests := []struct {
		name    string
		expr    PS.Expr
		expCost int
	}{
		{name: "ident", expr: &PS.Ident{Name: "x"}, expCost: 1},
		{name: "qualified name", expr: &PS.QualifiedName{Table: "t", Name: "x"}, expCost: 1},
		{name: "number literal", expr: &PS.NumberLiteral{Val: 5}, expCost: 1},
		{name: "string literal", expr: &PS.StringLiteral{Val: "hello"}, expCost: 1},
		{name: "bool literal", expr: &PS.BoolLiteral{Val: true}, expCost: 1},
		{name: "null literal", expr: &PS.NullLiteral{}, expCost: 1},
		{name: "param", expr: &PS.Param{Index: 0}, expCost: 1},
		{name: "star", expr: &PS.StarExpr{}, expCost: 1},
		{name: "interval", expr: &PS.IntervalLiteral{Value: "1", Unit: "day"}, expCost: 2},
		{
			name: "simple eq",
			expr: &PS.BinaryExpr{
				Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 5},
			},
			expCost: 4, // comparison 2 + left 1 + right 1
		},
		{
			name: "not-equal",
			expr: &PS.BinaryExpr{
				Op: LX.T_NE, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 0},
			},
			expCost: 4,
		},
		{
			name: "less-than",
			expr: &PS.BinaryExpr{
				Op: LX.T_LT, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 10},
			},
			expCost: 4,
		},
		{
			name: "like pattern",
			expr: &PS.BinaryExpr{
				Op: LX.T_LIKE, Left: &PS.Ident{Name: "x"}, Right: &PS.StringLiteral{Val: "%pat%"},
			},
			expCost: 12, // LIKE 10 + left 1 + right 1
		},
		{
			name: "glob pattern",
			expr: &PS.BinaryExpr{
				Op: LX.T_GLOB, Left: &PS.Ident{Name: "x"}, Right: &PS.StringLiteral{Val: "*pat*"},
			},
			expCost: 12,
		},
		{
			name: "in list",
			expr: &PS.InExpr{
				Expr: &PS.Ident{Name: "x"},
				List: []PS.Expr{
					&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}, &PS.NumberLiteral{Val: 3},
				},
			},
			expCost: 9, // InExpr 5 + expr 1 + items(1+1+1)
		},
		{
			name: "between",
			expr: &PS.BetweenExpr{
				Expr: &PS.Ident{Name: "x"},
				Low:  &PS.NumberLiteral{Val: 1},
				High: &PS.NumberLiteral{Val: 100},
			},
			expCost: 7, // Between 4 + expr + low + high
		},
		{
			name: "function call in predicate",
			expr: &PS.BinaryExpr{
				Op:    LX.T_EQ,
				Left:  &PS.FunctionCall{Name: "abs", Args: []PS.Expr{&PS.Ident{Name: "x"}}},
				Right: &PS.NumberLiteral{Val: 5},
			},
			expCost: 9, // 2 + (5 + 1) + 1
		},
		{
			name:    "exists subquery",
			expr:    &PS.ExistsExpr{Subquery: &PS.Select{From: "t"}},
			expCost: 100,
		},
		{
			name:    "scalar subquery",
			expr:    &PS.SubqueryExpr{Subquery: &PS.Select{From: "t"}},
			expCost: 100,
		},
		{
			name: "cast expression",
			expr: &PS.CastExpr{
				Expr: &PS.Ident{Name: "x"},
				Type: &PS.TypeInfo{Type: LX.T_INT_KW},
			},
			expCost: 4, // Cast 3 + child 1
		},
		{
			name: "unary not",
			expr: &PS.UnaryExpr{
				Op: LX.T_NOT, Operand: &PS.Ident{Name: "x"},
			},
			expCost: 2, // NOT 1 + operand 1
		},
		{
			name: "unary minus",
			expr: &PS.UnaryExpr{
				Op: LX.T_MINUS, Operand: &PS.Ident{Name: "x"},
			},
			expCost: 3, // arithmetic unary 2 + operand 1
		},
		{
			name: "case expression",
			expr: &PS.CaseExpr{
				WhenList: []PS.WhenClause{{
					Cond: &PS.BinaryExpr{
						Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 1},
					},
					Then: &PS.StringLiteral{Val: "one"},
				}},
				Else: &PS.StringLiteral{Val: "other"},
			},
			expCost: 9, // Case 3 + when(4) + then(1) + else(1)
		},
		{
			name: "alias expression",
			expr: &PS.AliasedExpr{
				Expr:  &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 1}},
				Alias: "eq",
			},
			expCost: 4, // comparison cost
		},
		{
			name: "and chain",
			expr: &PS.BinaryExpr{
				Op:   LX.T_AND,
				Left: &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 1}},
				Right: &PS.BinaryExpr{
					Op: LX.T_LIKE, Left: &PS.Ident{Name: "y"}, Right: &PS.StringLiteral{Val: "%test%"},
				},
			},
			expCost: 16, // 4 + 12
		},
		{
			name: "arithmetic binary",
			expr: &PS.BinaryExpr{
				Op: LX.T_PLUS, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 1},
			},
			expCost: 5, // arithmetic 3 + children(1+1)
		},
		{
			name: "concat binary",
			expr: &PS.BinaryExpr{
				Op: LX.T_CONCAT, Left: &PS.Ident{Name: "x"}, Right: &PS.StringLiteral{Val: "_suffix"},
			},
			expCost: 6, // concat 4 + children(1+1)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := CO.Cost(tt.expr)
			if cost != tt.expCost {
				t.Errorf("CO.Cost(%T) = %d, want %d", tt.expr, cost, tt.expCost)
			}
		})
	}
}

// TestFilterReorderIndices verifies that reorderIndices sorts predicates
// by ascending cost, with stable sort for equal-cost items. REQ001248.
func TestFilterReorderIndices(t *testing.T) {
	cheap := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 1}}
	medium := &PS.InExpr{
		Expr: &PS.Ident{Name: "x"},
		List: []PS.Expr{&PS.NumberLiteral{Val: 1}, &PS.NumberLiteral{Val: 2}},
	}
	expensive := &PS.BinaryExpr{
		Op: LX.T_LIKE, Left: &PS.Ident{Name: "y"}, Right: &PS.StringLiteral{Val: "%test%"},
	}

	t.Run("cost ordering", func(t *testing.T) {
		// Reverse order, worst case for AST ordering.
		preds := []PS.Expr{expensive, cheap, medium}
		indices := CO.ReorderIndices(preds)
		if indices == nil {
			t.Fatal("reorderIndices returned nil for 3 predicates")
		}
		ordered := CO.OrderSlice(preds, indices)

		got := []int{
			CO.Cost(ordered[0]),
			CO.Cost(ordered[1]),
			CO.Cost(ordered[2]),
		}
		want := []int{4, 8, 12}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("position %d: cost = %d, want %d", i, got[i], want[i])
			}
		}
	})

	t.Run("stable sort", func(t *testing.T) {
		// All cost 4: comparison 2 + ident 1 + literal 1.
		p1 := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}}
		p2 := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "b"}, Right: &PS.NumberLiteral{Val: 2}}
		p3 := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "c"}, Right: &PS.NumberLiteral{Val: 3}}

		preds := []PS.Expr{p3, p1, p2}
		indices := CO.ReorderIndices(preds)
		if indices == nil {
			t.Fatal("returned nil")
		}
		ordered := CO.OrderSlice(preds, indices)

		if ordered[0] != p3 || ordered[1] != p1 || ordered[2] != p2 {
			t.Error("stable sort should preserve original order for equal cost")
		}
	})

	t.Run("single element", func(t *testing.T) {
		if indices := CO.ReorderIndices([]PS.Expr{cheap}); indices != nil {
			t.Error("expected nil for single element")
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		if indices := CO.ReorderIndices([]PS.Expr{}); indices != nil {
			t.Error("expected nil for empty slice")
		}
	})
}

// TestFilterReorder_Logic verifies reordering does not change meaning.
// REQ001248.
func TestFilterReorder_Logic(t *testing.T) {
	t.Run("all predicates pass", func(t *testing.T) {
		p1 := &PS.BinaryExpr{
			Op: LX.T_GE, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 0},
		}
		p2 := &PS.BinaryExpr{
			Op: LX.T_LT, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 100},
		}
		p3 := &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 50},
		}

		preds := []PS.Expr{p1, p2, p3}
		indices := CO.ReorderIndices(preds)
		if indices == nil {
			t.Fatal("reorderIndices returned nil")
		}
		ordered := CO.OrderSlice(preds, indices)

		// All 3 must survive reordering.
		if len(ordered) != 3 {
			t.Fatalf("expected 3, got %d", len(ordered))
		}
		seen := map[string]bool{}
		for _, p := range ordered {
			if be, ok := p.(*PS.BinaryExpr); ok {
				if nl, ok := be.Right.(*PS.NumberLiteral); ok {
					_ = nl
					seen[string(rune(be.Op))] = true
				}
			}
		}
		if !seen[string(rune(LX.T_GE))] || !seen[string(rune(LX.T_LT))] || !seen[string(rune(LX.T_EQ))] {
			t.Errorf("reorder lost predicates: %v", seen)
		}
	})

	t.Run("cheapest first", func(t *testing.T) {
		cheap := &PS.BinaryExpr{
			Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 999},
		}
		expensive := &PS.BinaryExpr{
			Op: LX.T_LIKE, Left: &PS.Ident{Name: "y"}, Right: &PS.StringLiteral{Val: "%pattern%"},
		}

		preds := []PS.Expr{expensive, cheap}
		indices := CO.ReorderIndices(preds)
		if indices == nil {
			t.Fatal("reorderIndices returned nil")
		}
		ordered := CO.OrderSlice(preds, indices)

		if ordered[0] != cheap {
			t.Error("cheap predicate should be first")
		}
		if ordered[1] != expensive {
			t.Error("expensive predicate should be last")
		}
	})
}

// BenchmarkFilterReorder measures reorderIndices + orderSlice throughput.
func BenchmarkFilterReorder(b *testing.B) {
	cheap := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 5}}
	m1 := &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 0}}
	m2 := &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 100}}
	exp := &PS.BinaryExpr{
		Op: LX.T_LIKE, Left: &PS.Ident{Name: "y"}, Right: &PS.StringLiteral{Val: "%pattern%"},
	}
	vexp := &PS.BinaryExpr{
		Op: LX.T_GLOB, Left: &PS.Ident{Name: "z"}, Right: &PS.StringLiteral{Val: "*pattern*"},
	}

	preds := []PS.Expr{vexp, exp, m2, m1, cheap}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		indices := CO.ReorderIndices(preds)
		if indices == nil {
			b.Fatal("reorderIndices returned nil")
		}
		_ = CO.OrderSlice(preds, indices)
	}
}

// BenchmarkFilterReorder_Mixed measures full reorder + filter chain creation.
func BenchmarkFilterReorder_Mixed(b *testing.B) {
	cheap := &PS.BinaryExpr{Op: LX.T_EQ, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 5}}
	m1 := &PS.BinaryExpr{Op: LX.T_GT, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 0}}
	m2 := &PS.BinaryExpr{Op: LX.T_LT, Left: &PS.Ident{Name: "x"}, Right: &PS.NumberLiteral{Val: 100}}
	exp := &PS.BinaryExpr{
		Op: LX.T_LIKE, Left: &PS.Ident{Name: "y"}, Right: &PS.StringLiteral{Val: "%pattern%"},
	}
	vexp := &PS.BinaryExpr{
		Op: LX.T_GLOB, Left: &PS.Ident{Name: "z"}, Right: &PS.StringLiteral{Val: "*pattern*"},
	}

	// Reverse cost order: most expensive first (worst AST order).
	expr := &PS.BinaryExpr{
		Op: LX.T_AND, Left: vexp,
		Right: &PS.BinaryExpr{
			Op: LX.T_AND, Left: exp,
			Right: &PS.BinaryExpr{
				Op: LX.T_AND, Left: m2,
				Right: &PS.BinaryExpr{Op: LX.T_AND, Left: m1, Right: cheap},
			},
		},
	}

	pl := NewPlanner()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conjuncts := pl.splitAnd(expr)
		if order := CO.ReorderIndices(conjuncts); order != nil {
			conjuncts = CO.OrderSlice(conjuncts, order)
		}
		var op DT.Operator = OP.NewSeqScan("bench")
		for _, c := range conjuncts {
			op = OP.NewFilter(op, c, nil)
		}
		_ = op
	}
}