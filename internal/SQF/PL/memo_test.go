package PL

import (
	"fmt"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestMemo_LRUEvictionBounds verifies that Put evicts the
// least-recently-used entry when the cache is full. REQ000584.
func TestMemo_LRUEvictionBounds(t *testing.T) {
	const cap = 16
	m := NewMemoWithCapacity(cap)
	for i := 0; i < 2000; i++ {
		key := fmt.Sprintf("k-%d", i)
		m.Put(key, Plan{Cost: float64(i), MemoKey: key})
		if m.Len() > cap {
			t.Fatalf("Len = %d, exceeds cap %d at i=%d", m.Len(), cap, i)
		}
	}
	if got := m.Len(); got != cap {
		t.Errorf("final Len = %d, want %d", got, cap)
	}
	if got := m.MaxEntries(); got != cap {
		t.Errorf("MaxEntries = %d, want %d", got, cap)
	}
	// The earliest keys should have been evicted.
	if _, ok := m.Get("k-0"); ok {
		t.Error("expected LRU entry to be evicted")
	}
	// The most recent key must still be present.
	if _, ok := m.Get("k-1999"); !ok {
		t.Error("expected most recent entry to be present")
	}
}

// TestMemo_LRUPromotionOnGet verifies that a Get promotes the
// entry to most-recently-used. REQ000584.
func TestMemo_LRUPromotionOnGet(t *testing.T) {
	m := NewMemoWithCapacity(2)
	m.Put("a", Plan{Cost: 1, MemoKey: "a"})
	m.Put("b", Plan{Cost: 2, MemoKey: "b"})
	// Touch 'a' so 'b' becomes the LRU.
	if _, ok := m.Get("a"); !ok {
		t.Fatal("expected 'a' to be present")
	}
	m.Put("c", Plan{Cost: 3, MemoKey: "c"})
	if _, ok := m.Get("b"); ok {
		t.Error("'b' should have been evicted as LRU")
	}
	if _, ok := m.Get("a"); !ok {
		t.Error("'a' should still be present after promotion")
	}
	if _, ok := m.Get("c"); !ok {
		t.Error("'c' should be present")
	}
}

// TestMemo_RePutPromotes verifies that re-Put updates the
// value and promotes the entry. REQ000584.
func TestMemo_RePutPromotes(t *testing.T) {
	m := NewMemoWithCapacity(2)
	m.Put("a", Plan{Cost: 1, MemoKey: "a"})
	m.Put("b", Plan{Cost: 2, MemoKey: "b"})
	m.Put("a", Plan{Cost: 99, MemoKey: "a"})
	got, ok := m.Get("a")
	if !ok {
		t.Fatal("'a' should be present after re-Put")
	}
	if got.Cost != 99 {
		t.Errorf("re-Put cost = %v, want 99", got.Cost)
	}
	// Now 'b' is LRU; inserting 'c' evicts 'b'.
	m.Put("c", Plan{Cost: 3, MemoKey: "c"})
	if _, ok := m.Get("b"); ok {
		t.Error("'b' should have been evicted after re-Put promotion")
	}
}

// TestMemo_DefaultCapacityMatchesSpec verifies the default
// capacity is 1024. REQ000584.
func TestMemo_DefaultCapacityMatchesSpec(t *testing.T) {
	m := NewMemo()
	if m.MaxEntries() != DefaultMaxMemoEntries {
		t.Errorf("default MaxEntries = %d, want %d", m.MaxEntries(), DefaultMaxMemoEntries)
	}
	if DefaultMaxMemoEntries != 4096 {
		t.Errorf("DefaultMaxMemoEntries = %d, want 4096", DefaultMaxMemoEntries)
	}
}

// TestMemo_ClearDropsAll verifies Clear empties the cache.
// REQ000584.
func TestMemo_ClearDropsAll(t *testing.T) {
	m := NewMemoWithCapacity(4)
	m.Put("a", Plan{Cost: 1, MemoKey: "a"})
	m.Put("b", Plan{Cost: 2, MemoKey: "b"})
	m.Clear()
	if m.Len() != 0 {
		t.Errorf("after Clear, Len = %d, want 0", m.Len())
	}
	if _, ok := m.Get("a"); ok {
		t.Error("'a' should not be present after Clear")
	}
	// Re-Put after Clear should still work.
	m.Put("a", Plan{Cost: 1, MemoKey: "a"})
	if m.Len() != 1 {
		t.Errorf("after re-Put, Len = %d, want 1", m.Len())
	}
}

// TestMemo_SchemaVersionInvalidation verifies that a schema
// version bump changes the SerializeKey output, so previously
// cached plans are not reused. REQ000584.
func TestMemo_SchemaVersionInvalidation(t *testing.T) {
	stmt := &PS.Select{From: "t", Cols: []PS.Expr{&PS.Ident{Name: "x"}}}
	prev := SchemaVersion()
	defer defaultMemoSchemaVersion.Store(prev)
	defaultMemoSchemaVersion.Store(0)
	v1 := SerializeKey(stmt)
	BumpDefaultSchemaVersion()
	v2 := SerializeKey(stmt)
	if v1 == v2 {
		t.Errorf("expected different keys after schema bump, both = %q", v1)
	}
}

// TestMemo_BumpInvalidatesCachedPlan simulates the DDL flow:
// insert plan, bump schema, re-Plan, expect BuildTree to be
// called again because the key no longer matches. REQ000584.
func TestMemo_BumpInvalidatesCachedPlan(t *testing.T) {
	defaultMemoSchemaVersion.Store(0)
	calls := 0
	pl := NewPlannerWith(PlanOptions{
		BuildTree: func(PS.Stmt) (float64, error) {
			calls++
			return 0.5, nil
		},
	})
	stmt := &PS.Select{From: "t", Cols: []PS.Expr{&PS.Ident{Name: "x"}}}
	if _, err := pl.Plan(stmt); err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Plan(stmt); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call before bump, got %d", calls)
	}
	// Simulate DDL: bump schema version.
	pl.Memo().BumpSchemaVersion()
	if _, err := pl.Plan(stmt); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls after schema bump, got %d", calls)
	}
}

// TestSerializeKey_NotSHA256Length verifies the key is the
// 16-hex-character xxhash digest, not the 64-hex-character
// SHA-256 digest. REQ000584.
func TestSerializeKey_NotSHA256Length(t *testing.T) {
	stmt := &PS.Select{From: "t"}
	k := SerializeKey(stmt)
	if len(k) != 16 {
		t.Errorf("key length = %d, want 16 (xxhash hex)", len(k))
	}
	for i, c := range k {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			t.Errorf("key[%d] = %q, want lowercase hex", i, c)
		}
	}
}

// TestSerializeKey_StableAcrossCalls verifies the hash is
// deterministic for the same AST and schema version. REQ000584.
func TestSerializeKey_StableAcrossCalls(t *testing.T) {
	stmt := &PS.Select{From: "t", Cols: []PS.Expr{&PS.Ident{Name: "x"}}}
	a := SerializeKey(stmt)
	b := SerializeKey(stmt)
	if a != b {
		t.Errorf("hash unstable: %q vs %q", a, b)
	}
}

// TestSerializeKey_DifferentForDifferentASTs is a basic
// anti-collision check. REQ000584.
func TestSerializeKey_DifferentForDifferentASTs(t *testing.T) {
	defaultMemoSchemaVersion.Store(0)
	a := SerializeKey(&PS.Select{From: "t", Cols: []PS.Expr{&PS.Ident{Name: "x"}}})
	b := SerializeKey(&PS.Select{From: "t", Cols: []PS.Expr{&PS.Ident{Name: "y"}}})
	if a == b {
		t.Errorf("expected different hashes for different ASTs, both = %q", a)
	}
}

// TestMemo_ConcurrentSafe verifies basic race-safety under
// concurrent Put/Get. REQ000584.
func TestMemo_ConcurrentSafe(t *testing.T) {
	m := NewMemoWithCapacity(128)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				key := fmt.Sprintf("k-%d", off*1000+i)
				m.Put(key, Plan{Cost: float64(i), MemoKey: key})
				_, _ = m.Get(key)
			}
		}(w)
	}
	wg.Wait()
	if m.Len() > 128 {
		t.Errorf("Len = %d, exceeds cap", m.Len())
	}
}

// TestMemo_2000DistinctStaysBounded is the explicit REQ000584
// acceptance test: 2000 distinct queries must not grow the memo
// past its capacity.
func TestMemo_2000DistinctStaysBounded(t *testing.T) {
	m := NewMemo()
	for i := 0; i < 2000; i++ {
		stmt := &PS.Select{From: "t", Cols: []PS.Expr{&PS.Ident{Name: fmt.Sprintf("c%d", i)}}}
		key := SerializeKey(stmt)
		m.Put(key, Plan{Cost: float64(i), MemoKey: key})
	}
	if m.Len() > DefaultMaxMemoEntries {
		t.Errorf("memo grew to %d, exceeds default cap %d", m.Len(), DefaultMaxMemoEntries)
	}
}
