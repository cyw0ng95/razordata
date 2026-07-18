package DT

import (
	"testing"
)

// REQ001319: DDLVersion starts at 0.
func TestDDLVersion_Zero(t *testing.T) {
	v := DDLVersion()
	if v != 0 {
		t.Errorf("DDLVersion: got %d, want 0", v)
	}
}

// REQ001319: IncrDDLVersion increments the version.
func TestDDLVersion_Increment(t *testing.T) {
	// Reset to 0 for isolation.
	TablesMu.Lock()
	ddlVersion = 0
	TablesMu.Unlock()

	v := DDLVersion()
	if v != 0 {
		t.Fatalf("DDLVersion: got %d, want 0", v)
	}

	IncrDDLVersion()
	v = DDLVersion()
	if v != 1 {
		t.Errorf("DDLVersion after IncrDDLVersion: got %d, want 1", v)
	}

	IncrDDLVersion()
	IncrDDLVersion()
	v = DDLVersion()
	if v != 3 {
		t.Errorf("DDLVersion after 2 more IncrDDLVersion: got %d, want 3", v)
	}
}

// REQ001319: PushDDLVersion saves current version.
func TestDDLVersion_PushPop(t *testing.T) {
	// Reset for isolation.
	TablesMu.Lock()
	ddlVersion = 0
	TablesMu.Unlock()
	ddlVersionStackMu.Lock()
	ddlVersionStack = nil
	ddlTableSnapshots = nil
	ddlVersionStackMu.Unlock()

	// Version is 0.
	if v := DDLVersion(); v != 0 {
		t.Fatalf("DDLVersion: got %d, want 0", v)
	}

	// Push at version 0.
	PushDDLVersion()

	// Increment to 5.
	IncrDDLVersion()
	IncrDDLVersion()
	IncrDDLVersion()
	IncrDDLVersion()
	IncrDDLVersion()
	if v := DDLVersion(); v != 5 {
		t.Fatalf("DDLVersion after 5 Incr: got %d, want 5", v)
	}

	// Pop restores to 0.
	v := PopDDLVersion()
	if v != 0 {
		t.Errorf("PopDDLVersion: got %d, want 0", v)
	}
	if v := DDLVersion(); v != 0 {
		t.Errorf("DDLVersion after Pop: got %d, want 0", v)
	}

	// Stack is now empty.
	if l := DDLVersionStackLen(); l != 0 {
		t.Errorf("StackLen after pop: got %d, want 0", l)
	}
}

// REQ001319: PopDDLVersion on empty stack returns 0 and does not panic.
func TestDDLVersion_PopEmpty(t *testing.T) {
	TablesMu.Lock()
	ddlVersion = 42
	TablesMu.Unlock()
	ddlVersionStackMu.Lock()
	ddlVersionStack = nil
	ddlTableSnapshots = nil
	ddlVersionStackMu.Unlock()

	v := PopDDLVersion()
	if v != 0 {
		t.Errorf("PopDDLVersion on empty stack: got %d, want 0", v)
	}
	if v := DDLVersion(); v != 42 {
		t.Errorf("DDLVersion after pop empty: got %d, want 42", v)
	}
}

// REQ001319: Multiple push/pop.
func TestDDLVersion_MultiplePushPop(t *testing.T) {
	TablesMu.Lock()
	ddlVersion = 0
	TablesMu.Unlock()
	ddlVersionStackMu.Lock()
	ddlVersionStack = nil
	ddlTableSnapshots = nil
	ddlVersionStackMu.Unlock()

	PushDDLVersion() // save 0
	IncrDDLVersion() // v=1
	PushDDLVersion() // save 1
	IncrDDLVersion() // v=2
	IncrDDLVersion() // v=3

	v := PopDDLVersion()
	if v != 1 {
		t.Errorf("Pop: got %d, want 1", v)
	}
	if v := DDLVersion(); v != 1 {
		t.Errorf("DDLVersion after pop: got %d, want 1", v)
	}

	v = PopDDLVersion()
	if v != 0 {
		t.Errorf("Pop: got %d, want 0", v)
	}
	if v := DDLVersion(); v != 0 {
		t.Errorf("DDLVersion after pop: got %d, want 0", v)
	}
}

// REQ001320: DiscardDDLVersion pops without restoring the version.
func TestDDLVersion_Discard(t *testing.T) {
	TablesMu.Lock()
	ddlVersion = 10
	TablesMu.Unlock()
	ddlVersionStackMu.Lock()
	ddlVersionStack = nil
	ddlTableSnapshots = nil
	ddlVersionStackMu.Unlock()

	PushDDLVersion() // save 10
	IncrDDLVersion() // v=11
	IncrDDLVersion() // v=12

	if v := DDLVersion(); v != 12 {
		t.Fatalf("DDLVersion before discard: got %d, want 12", v)
	}
	if l := DDLVersionStackLen(); l != 1 {
		t.Fatalf("StackLen before discard: got %d, want 1", l)
	}

	DiscardDDLVersion()
	if l := DDLVersionStackLen(); l != 0 {
		t.Fatalf("StackLen after discard: got %d, want 0", l)
	}
	if v := DDLVersion(); v != 12 {
		t.Errorf("DDLVersion after discard: got %d, want 12 (unchanged)", v)
	}
}

// REQ001320: DiscardDDLVersion on empty stack is a no-op.
func TestDDLVersion_DiscardEmpty(t *testing.T) {
	ddlVersionStackMu.Lock()
	ddlVersionStack = nil
	ddlTableSnapshots = nil
	ddlVersionStackMu.Unlock()
	// Should not panic.
	DiscardDDLVersion()
}

// REQ001320: PopDDLVersion drops tables added after savepoint.
func TestDDLVersion_PopRemovesNewTables(t *testing.T) {
	TablesMu.Lock()
	ddlVersion = 0
	Tables = map[string][]Row{"t1": {}}
	Schemas = map[string][]string{"t1": {"a"}}
	TablePKs = map[string]string{"t1": "a"}
	TablesMu.Unlock()
	ddlVersionStackMu.Lock()
	ddlVersionStack = nil
	ddlTableSnapshots = nil
	ddlVersionStackMu.Unlock()

	PushDDLVersion() // snapshot: {t1}
	IncrDDLVersion()

	TablesMu.Lock()
	Tables["t2"] = []Row{}
	Schemas["t2"] = []string{"x"}
	TablePKs["t2"] = "x"
	TablesMu.Unlock()

	// t2 should be removed on rollback.
	v := PopDDLVersion()
	if v != 0 {
		t.Errorf("PopDDLVersion: got %d, want 0", v)
	}

	TablesMu.RLock()
	_, t1ok := Tables["t1"]
	_, t2ok := Tables["t2"]
	TablesMu.RUnlock()
	if !t1ok {
		t.Error("t1 should still exist after pop")
	}
	if t2ok {
		t.Error("t2 should be removed after pop")
	}
}

// REQ001320: PushDDLVersion snapshots tables even when none exist.
func TestDDLVersion_PushEmptyTables(t *testing.T) {
	TablesMu.Lock()
	ddlVersion = 0
	Tables = map[string][]Row{}
	Schemas = map[string][]string{}
	TablePKs = map[string]string{}
	TablesMu.Unlock()
	ddlVersionStackMu.Lock()
	ddlVersionStack = nil
	ddlTableSnapshots = nil
	ddlVersionStackMu.Unlock()

	PushDDLVersion()
	if l := DDLVersionStackLen(); l != 1 {
		t.Fatalf("StackLen: got %d, want 1", l)
	}

	TablesMu.Lock()
	Tables["new_tbl"] = []Row{}
	Schemas["new_tbl"] = []string{"col"}
	TablePKs["new_tbl"] = "col"
	TablesMu.Unlock()

	PopDDLVersion()
	TablesMu.RLock()
	_, exists := Tables["new_tbl"]
	TablesMu.RUnlock()
	if exists {
		t.Error("new_tbl should be removed after pop from empty snapshot")
	}
}
