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

// REQ000636: NewMemoWithCapacity(-1) uses default.
func TestNewMemoWithCapacity_Negative(t *testing.T) {
	m := NewMemoWithCapacity(-1)
	if m.MaxEntries() != DefaultMaxMemoEntries {
		t.Errorf("MaxEntries: got %d, want %d", m.MaxEntries(), DefaultMaxMemoEntries)
	}
}

// REQ000636: Clear removes all entries.
func TestMemoClear(t *testing.T) {
	m := NewMemo()
	m.Put("k", Plan{Cost: 1})
	if m.Len() != 1 {
		t.Fatal("expected 1 entry after Put")
	}
	m.Clear()
	if m.Len() != 0 {
		t.Errorf("Len after Clear: got %d, want 0", m.Len())
	}
	if _, ok := m.Get("k"); ok {
		t.Error("Get after Clear returned ok=true")
	}
}

// REQ000636: BumpSchemaVersion increments and Get sees new version.
func TestMemoBumpSchemaVersion(t *testing.T) {
	m := NewMemo()
	v1 := m.SchemaVersion()
	v2 := m.BumpSchemaVersion()
	if v2 != v1+1 {
		t.Errorf("BumpSchemaVersion: got %d, want %d", v2, v1+1)
	}
	if got := m.SchemaVersion(); got != v1+1 {
		t.Errorf("SchemaVersion after bump: got %d, want %d", got, v1+1)
	}
}

// REQ000636: LearnedModel.Predict untrained returns fallback (features[1]).
func TestLearnedModelPredictUntrained(t *testing.T) {
	lm := &LearnedModel{correlations: make(map[pairKey]float64)}
	features := [6]float64{0, 0.42, 0, 0, 0, 0}
	got := lm.Predict(features)
	if got != 0.42 {
		t.Errorf("untrained Predict: want 0.42, got %f", got)
	}
}

// REQ000636: PredicateCache LRU eviction.
func TestPredicateCacheLRUEviction(t *testing.T) {
	c := NewPredicateCache(2)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3) // should evict "a"

	if _, ok := c.Get("a"); ok {
		t.Error("expected 'a' to be evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("expected 'b' to be present")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("expected 'c' to be present")
	}
}

// REQ000636: BuildTree is memoized — repeated Plan calls don't re-execute.
func TestPlanner_BuildTreeMemoized(t *testing.T) {
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
