package PL

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestSerializeKey_Deterministic(t *testing.T) {
	stmt := &PS.Select{
		Cols:  []PS.Expr{&PS.Ident{Name: "a"}},
		From:  "t",
		Where: &PS.NumberLiteral{Val: 1},
	}
	k1 := SerializeKey(stmt)
	k2 := SerializeKey(stmt)
	if k1 != k2 {
		t.Errorf("expected same key for same AST, got %q vs %q", k1, k2)
	}
	if len(k1) == 0 {
		t.Error("expected non-empty key")
	}
}

func TestSerializeKey_DistinguishesAST(t *testing.T) {
	a := &PS.Select{Cols: []PS.Expr{&PS.Ident{Name: "a"}}, From: "t"}
	b := &PS.Select{Cols: []PS.Expr{&PS.Ident{Name: "b"}}, From: "t"}
	if SerializeKey(a) == SerializeKey(b) {
		t.Error("expected different keys for different ASTs")
	}
}

func TestMemo_PutGet(t *testing.T) {
	m := NewMemo()
	if _, ok := m.Get("k"); ok {
		t.Error("empty memo should not have k")
	}
	m.Put("k", Plan{Cost: 1.5, MemoKey: "k"})
	got, ok := m.Get("k")
	if !ok {
		t.Fatal("expected to find k after Put")
	}
	if got.Cost != 1.5 {
		t.Errorf("Cost = %v, want 1.5", got.Cost)
	}
	if got.MemoKey != "k" {
		t.Errorf("MemoKey = %q, want k", got.MemoKey)
	}
}

func TestPlanner_Memoizes(t *testing.T) {
	calls := 0
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(PS.Stmt) (float64, error) {
			calls++
			return 0.42, nil
		},
	})
	stmt := &PS.Select{From: "t"}
	if _, err := pl.Plan(stmt); err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Plan(stmt); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected BuildTree called once (memoized), got %d", calls)
	}
	if pl.Memo().Len() != 1 {
		t.Errorf("expected memo to hold 1 plan, got %d", pl.Memo().Len())
	}
}
