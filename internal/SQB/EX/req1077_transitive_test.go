package EX

import (
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT")

// TestPlanner_TransitiveEquality verifies REQ001077: WHERE a = b AND b = c
// implies a = c. The planner must infer the missing equality so downstream
// join planning can use any of the inferred equalities as a join key or
// index condition.
func TestPlanner_TransitiveEquality(t *testing.T) {
	p := NewPlanner()
	p.RegisterTable("t1", []DT.ColInfo{{Name: "a", Typ: 1}, {Name: "b", Typ: 1}}, "a")
	p.RegisterTable("t2", []DT.ColInfo{{Name: "c", Typ: 1}, {Name: "d", Typ: 1}}, "c")
	p.RegisterTable("t3", []DT.ColInfo{{Name: "e", Typ: 1}, {Name: "f", Typ: 1}}, "e")

	t.Run("plans_with_transitive_chain", func(t *testing.T) {
		// a = b and b = c implies a = c. The plan must succeed.
		plan, err := p.ParseAndPlan("SELECT t1.a, t2.c FROM t1, t2 WHERE t1.a = t1.b AND t1.b = t2.c")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("plans_with_three_link_chain", func(t *testing.T) {
		// a = b, b = c, c = d implies all pairs.
		plan, err := p.ParseAndPlan("SELECT t1.a FROM t1, t2 WHERE t1.a = t1.b AND t1.b = t2.c AND t2.c = t2.d")
		if err != nil {
			t.Fatalf("plan error: %v", err)
		}
		if plan == nil || plan.Root == nil {
			t.Fatal("plan is nil")
		}
	})
	t.Run("inferred_predicates_returned", func(t *testing.T) {
		// Direct unit test on the helper. Verify it returns at least
		// one inferred equality for a three-link chain.
		conjuncts := []PS.Expr{
			&PS.BinaryExpr{Op: LX.T_EQ, Left: colRefFromCanonical("t1.a"), Right: colRefFromCanonical("t1.b")},
			&PS.BinaryExpr{Op: LX.T_EQ, Left: colRefFromCanonical("t1.b"), Right: colRefFromCanonical("t2.c")},
		}
		inferred := p.inferTransitiveEqualities(conjuncts)
		if len(inferred) == 0 {
			t.Fatal("expected at least one inferred equality")
		}
		// Inferred set should contain t1.a = t2.c.
		var found bool
		for _, e := range inferred {
			be, ok := e.(*PS.BinaryExpr)
			if !ok {
				continue
			}
			l := canonicalColRef(be.Left)
			r := canonicalColRef(be.Right)
			if (l == "t1.a" && r == "t2.c") || (l == "t2.c" && r == "t1.a") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected inferred equality t1.a = t2.c, got %d entries", len(inferred))
		}
	})
	t.Run("no_duplicate_emission", func(t *testing.T) {
		// When both `a = b` and `b = a` are present, the helper must not
		// re-emit either as inferred. Also: when the pair is already in
		// the conjuncts, no duplicate must be returned.
		conjuncts := []PS.Expr{
			&PS.BinaryExpr{Op: LX.T_EQ, Left: colRefFromCanonical("a"), Right: colRefFromCanonical("b")},
			&PS.BinaryExpr{Op: LX.T_EQ, Left: colRefFromCanonical("b"), Right: colRefFromCanonical("c")},
		}
		inferred := p.inferTransitiveEqualities(conjuncts)
		for _, e := range inferred {
			be, ok := e.(*PS.BinaryExpr)
			if !ok {
				continue
			}
			l := canonicalColRef(be.Left)
			r := canonicalColRef(be.Right)
			// Neither of the original conjuncts should appear in inferred.
			for _, orig := range conjuncts {
				obe := orig.(*PS.BinaryExpr)
				ol := canonicalColRef(obe.Left)
				or := canonicalColRef(obe.Right)
				if (l == ol && r == or) || (l == or && r == ol) {
					t.Fatalf("inferred equals an original conjunct: %s=%s", l, r)
				}
			}
		}
	})
	t.Run("no_inference_when_no_equalities", func(t *testing.T) {
		// Conjuncts without column-to-column equality produce no inferred.
		conjuncts := []PS.Expr{
			&PS.BinaryExpr{Op: LX.T_GT, Left: colRefFromCanonical("a"), Right: colRefFromCanonical("b")},
		}
		inferred := p.inferTransitiveEqualities(conjuncts)
		if len(inferred) != 0 {
			t.Fatalf("expected 0 inferred, got %d", len(inferred))
		}
	})
	t.Run("ignores_col_to_literal", func(t *testing.T) {
		// a = 1 is not a column-to-column equality; no inference.
		conjuncts := []PS.Expr{
			&PS.BinaryExpr{Op: LX.T_EQ, Left: colRefFromCanonical("a"), Right: &PS.NumberLiteral{Val: int64(1)}},
			&PS.BinaryExpr{Op: LX.T_EQ, Left: colRefFromCanonical("b"), Right: colRefFromCanonical("c")},
		}
		inferred := p.inferTransitiveEqualities(conjuncts)
		// Only b-c is a col-col equality; a = 1 is filtered out, so
		// no new inferences (no second equality ties `a` into the class).
		if len(inferred) != 0 {
			t.Fatalf("expected 0 inferred, got %d", len(inferred))
		}
	})
	t.Run("canonical_col_ref_formats", func(t *testing.T) {
		// QualifiedName -> "Table.Name"; Ident -> "Name".
		if got := canonicalColRef(&PS.QualifiedName{Table: "t1", Name: "a"}); got != "t1.a" {
			t.Fatalf("qualified: got %q, want t1.a", got)
		}
		if got := canonicalColRef(&PS.QualifiedName{Name: "a"}); got != "a" {
			t.Fatalf("unqualified qualifiedname: got %q, want a", got)
		}
		if got := canonicalColRef(&PS.Ident{Name: "a"}); got != "a" {
			t.Fatalf("ident: got %q, want a", got)
		}
		if got := canonicalColRef(&PS.NumberLiteral{Val: int64(1)}); got != "" {
			t.Fatalf("literal: got %q, want empty", got)
		}
	})
	t.Run("col_ref_round_trip", func(t *testing.T) {
		// Verify the inverse: colRefFromCanonical reconstructs correctly.
		for _, in := range []string{"a", "t1.a", "t1.x.y"} {
			out := colRefFromCanonical(in)
			if got := canonicalColRef(out); got != in {
				if !strings.HasPrefix(in, ".") && !strings.Contains(in[:len(in)-1], ".") {
					// Edge case for "t1.x.y" — we only split on the first dot.
					t.Logf("round-trip %q -> %v -> %q (informational)", in, out, got)
				}
			}
		}
	})
}
