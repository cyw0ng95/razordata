package ST

import "testing"

func TestStmtCache_PutGet(t *testing.T) {
	c := NewStmtCache(16)
	stmt := &Stmt{sql: "SELECT 1"}
	c.Put("SELECT 1", stmt)
	got, ok := c.Get("SELECT 1")
	if !ok || got != stmt {
		t.Fatalf("Get: got %v, ok=%v; want %v, true", got, ok, stmt)
	}
	c.Release("SELECT 1")
	if _, ok := c.Get("missing"); ok {
		t.Error("expected miss for missing sql")
	}
}

func TestStmtCache_LRUEviction(t *testing.T) {
	c := NewStmtCache(2)
	c.Put("A", &Stmt{sql: "A"})
	c.Release("A")
	c.Put("B", &Stmt{sql: "B"})
	c.Release("B")
	c.Put("C", &Stmt{sql: "C"})
	c.Release("C")
	if c.Size() != 2 {
		t.Errorf("expected size 2, got %d", c.Size())
	}
	if _, ok := c.Get("A"); ok {
		t.Error("expected A evicted")
	}
}

func TestStmtCache_RefCount(t *testing.T) {
	c := NewStmtCache(2)
	c.Put("X", &Stmt{sql: "X"})
	// Don't release — entry stays pinned.
	if _, ok := c.Get("X"); !ok {
		t.Fatal("expected hit")
	}
	c.Release("X") // first release (from Get)
	c.Release("X") // second release (from Put)
	if c.Size() != 1 {
		t.Errorf("expected size 1, got %d", c.Size())
	}
}

func TestStmtCache_Clear(t *testing.T) {
	c := NewStmtCache(4)
	c.Put("A", &Stmt{sql: "A"})
	c.Release("A")
	c.Put("B", &Stmt{sql: "B"})
	c.Release("B")
	c.Clear()
	if c.Size() != 0 {
		t.Errorf("after Clear: size %d, want 0", c.Size())
	}
}