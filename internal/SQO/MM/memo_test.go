package MM

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestNewMemo(t *testing.T) {
	m := NewMemo(0)
	if m == nil {
		t.Fatal("expected non-nil memo")
	}
	if m.MaxEntries() != DefaultMaxMemoEntries {
		t.Fatalf("expected %d, got %d", DefaultMaxMemoEntries, m.MaxEntries())
	}
}

func TestNewMemo_CustomCapacity(t *testing.T) {
	m := NewMemo(100)
	if m.MaxEntries() != 100 {
		t.Fatalf("expected 100, got %d", m.MaxEntries())
	}
}

func TestMemo_PutGet(t *testing.T) {
	m := NewMemo(10)
	m.Put("k1", &Plan{Cost: 1.5})
	p, ok := m.Get("k1")
	if !ok {
		t.Fatal("expected hit")
	}
	if p.Cost != 1.5 {
		t.Fatalf("expected cost 1.5, got %v", p.Cost)
	}
}

func TestMemo_GetMiss(t *testing.T) {
	m := NewMemo(10)
	_, ok := m.Get("nonexistent")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestMemo_UpdateExisting(t *testing.T) {
	m := NewMemo(10)
	m.Put("k1", &Plan{Cost: 1.0})
	m.Put("k1", &Plan{Cost: 2.0})
	p, ok := m.Get("k1")
	if !ok {
		t.Fatal("expected hit")
	}
	if p.Cost != 2.0 {
		t.Fatalf("expected cost 2.0, got %v", p.Cost)
	}
}

func TestMemo_LRUEviction(t *testing.T) {
	m := NewMemo(2)
	m.Put("a", &Plan{Cost: 1})
	m.Put("b", &Plan{Cost: 2})
	m.Get("a") // promote a
	m.Put("c", &Plan{Cost: 3})
	_, ok := m.Get("b")
	if ok {
		t.Fatal("expected b to be evicted (LRU)")
	}
	_, ok = m.Get("a")
	if !ok {
		t.Fatal("expected a to remain (recently used)")
	}
	_, ok = m.Get("c")
	if !ok {
		t.Fatal("expected c to remain")
	}
}

func TestMemo_Len(t *testing.T) {
	m := NewMemo(10)
	if m.Len() != 0 {
		t.Fatalf("expected 0, got %d", m.Len())
	}
	m.Put("a", &Plan{})
	if m.Len() != 1 {
		t.Fatalf("expected 1, got %d", m.Len())
	}
}

func TestMemo_Clear(t *testing.T) {
	m := NewMemo(10)
	m.Put("a", &Plan{})
	m.Put("b", &Plan{})
	m.Clear()
	if m.Len() != 0 {
		t.Fatalf("expected 0 after clear, got %d", m.Len())
	}
}

func TestMemo_SchemaVersion(t *testing.T) {
	m := NewMemo(10)
	v0 := m.SchemaVersion()
	v1 := m.BumpSchemaVersion()
	if v1 != v0+1 {
		t.Fatalf("expected %d, got %d", v0+1, v1)
	}
}

func TestMemo_Invalidate(t *testing.T) {
	m := NewMemo(10)
	m.Put("a", &Plan{Cost: 1, SchemaVer: 1})
	m.Put("b", &Plan{Cost: 2, SchemaVer: 2})
	m.Invalidate(2)
	_, ok := m.Get("a")
	if ok {
		t.Fatal("expected a to be invalidated (ver 1 < 2)")
	}
	_, ok = m.Get("b")
	if !ok {
		t.Fatal("expected b to remain (ver 2 >= 2)")
	}
}

func TestDefaultSchemaVersion(t *testing.T) {
	v0 := DefaultSchemaVersion()
	v1 := BumpDefaultSchemaVersion()
	if v1 != v0+1 {
		t.Fatalf("expected %d, got %d", v0+1, v1)
	}
}

func TestSerializeKey_Deterministic(t *testing.T) {
	stmt := &PS.Select{From: "t1", Where: &PS.BinaryExpr{
		Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 1}, Op: LX.T_EQ,
	}}
	k1 := SerializeKey(stmt, 1)
	k2 := SerializeKey(stmt, 1)
	if k1 != k2 {
		t.Fatalf("expected same key for same input, got %q vs %q", k1, k2)
	}
}

func TestSerializeKey_SchemaVersionChanges(t *testing.T) {
	stmt := &PS.Select{From: "t1"}
	k1 := SerializeKey(stmt, 1)
	k2 := SerializeKey(stmt, 2)
	if k1 == k2 {
		t.Fatal("expected different keys for different schema versions")
	}
}

func TestSerializeKey_DifferentStatements(t *testing.T) {
	s1 := &PS.Select{From: "t1"}
	s2 := &PS.Select{From: "t2"}
	k1 := SerializeKey(s1, 1)
	k2 := SerializeKey(s2, 1)
	if k1 == k2 {
		t.Fatal("expected different keys for different statements")
	}
}

func TestNormalizeForMemo_Select(t *testing.T) {
	stmt := &PS.Select{
		From: "t1",
		Where: &PS.BinaryExpr{
			Left: &PS.Ident{Name: "a"}, Right: &PS.NumberLiteral{Val: 42}, Op: LX.T_EQ,
		},
	}
	normalized, params := NormalizeForMemo(stmt)
	if normalized == nil {
		t.Fatal("expected non-nil normalized stmt")
	}
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}
	if params[0] != int64(42) {
		t.Fatalf("expected param value 42, got %v", params[0])
	}
}

func TestNormalizeForMemo_Nil(t *testing.T) {
	normalized, params := NormalizeForMemo(nil)
	if normalized != nil {
		t.Fatal("expected nil for nil input")
	}
	if len(params) != 0 {
		t.Fatalf("expected 0 params, got %d", len(params))
	}
}

func TestNewLearnedModel(t *testing.T) {
	lm := NewLearnedModel()
	if lm == nil {
		t.Fatal("expected non-nil learned model")
	}
}

func TestLearnedModel_Apply(t *testing.T) {
	lm := NewLearnedModel()
	cf := lm.Apply("some_predicate")
	if cf != 1.0 {
		t.Fatalf("expected 1.0 for unknown predicate, got %v", cf)
	}
}

func TestLearnedModel_Record(t *testing.T) {
	lm := NewLearnedModel()
	lm.Record("pred", 100, 50)
	cf := lm.Apply("pred")
	if cf != 1.0 {
		t.Fatalf("expected 1.0 (stub), got %v", cf)
	}
}

func TestPlan_Default(t *testing.T) {
	p := &Plan{}
	if p.Cost != 0 || p.MemoKey != "" || p.SchemaVer != 0 {
		t.Fatal("expected zero-value Plan")
	}
}

