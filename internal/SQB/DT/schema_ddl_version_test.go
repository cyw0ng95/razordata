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
	ddlVersionStackMu.Unlock()

	v := PopDDLVersion()
	if v != 0 {
		t.Errorf("PopDDLVersion on empty stack: got %d, want 0", v)
	}
	// Version should be unchanged.
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
