package EC

import (
	"errors"
	"fmt"
	"testing"
)

// TestEC_WrapChain verifies error wrapping preserves chain across all subsystems.
// This is the cross-subsystem chain verification required by REQ001199.
func TestEC_WrapChain(t *testing.T) {
	// Simulate a realistic chain: FIL → WAL → ENG → SQB → SYS
	// Step 1: FIL layer creates a raw I/O error
	filErr := New(KindIO, "read block 7")
	filErr.Module = "FIL/DF"
	filErr.Layer = LayerIO

	// Step 2: WAL layer wraps it
	walErr := WrapAt(KindCorrupt, "WAL/RP", LayerWAL, filErr)

	// Step 3: ENG layer wraps again
	engErr := WrapAt(KindCorrupt, "ENG/LS", LayerENG, walErr)

	// Step 4: SQB layer wraps with user context
	sqbErr := WrapAt(KindCorrupt, "SQB/EX", LayerSQL, engErr)
	sqbErr.Message = "query execution failed"

	// Step 5: SYS layer final wrap
	sysErr := WrapAt(KindCorrupt, "SYS/SY", LayerSQL, sqbErr)

	// Verify chain traversal works at each layer
	tests := []struct {
		name string
		err  error
		kind Kind
	}{
		{"FIL", filErr, KindIO},
		{"WAL", walErr, KindCorrupt},
		{"ENG", engErr, KindCorrupt},
		{"SQB", sqbErr, KindCorrupt},
		{"SYS", sysErr, KindCorrupt},
	}

	for _, tc := range tests {
		if !IsKind(tc.err, tc.kind) {
			t.Errorf("IsKind(%s layer, %v) = false", tc.name, tc.kind)
		}
	}

	// Top-level SYS error should traverse through ALL layers
	// This is the critical test: the top error must find the FIL error deep in the chain
	if !errors.Is(sysErr, filErr) {
		// Error chain broken between SYS and FIL layers
		// Impact: deep chain traversal fails for cross-subsystem errors
		// Fix: ensure Wrap/WrapAt always sets wrapped field for chain traversal
		t.Error("errors.Is(sysErr, filErr) should traverse full chain FIL → WAL → ENG → SQB → SYS")
	}

	// Verify LayerOf returns the first *Error in the chain (top-level)
	if got := LayerOf(sysErr); got != LayerSQL {
		// Layer extraction returned unexpected layer
		t.Errorf("LayerOf(sysErr) = %v, want LayerSQL", got)
	}

	// Verify ModuleOf returns the first *Error in the chain
	if got := ModuleOf(sysErr); got != "SYS/SY" {
		// Module extraction returned unexpected module
		t.Errorf("ModuleOf(sysErr) = %q, want %q", got, "SYS/SY")
	}

	// Verify IsCode works on the chain
	if !IsCode(sysErr, "RZR-IO-001") {
		t.Error("IsCode should find FIL Code through full chain")
	}

	// Verify IsSQLState works on the chain
	if !IsSQLState(sysErr, "08006") {
		t.Error("IsSQLState should find FIL SQLSTATE through full chain")
	}
}

// TestEC_CrossSubsystemWrapping tests that errors can be wrapped across
// subsystem boundaries without losing chain integrity.
func TestEC_CrossSubsystemWrapping(t *testing.T) {
	// Simulate: TXN/VL → ENG/CT → SYS/AP chain
	// TXN layer error
	txnErr := New(KindLocked, "lock timeout")
	txnErr.Module = "TXN/VL"
	txnErr.Layer = LayerTXN

	// ENG wraps with catalog context
	engErr := Wrapf(KindLocked, txnErr, "catalog operation locked")
	engErr.Module = "ENG/CT"
	engErr.Layer = LayerENG

	// SYS wraps for API return
	sysErr := Wrapf(KindLocked, engErr, "transaction conflict")
	sysErr.Module = "SYS/AP"
	sysErr.Layer = LayerSQL

	// Chain must be intact
	if !errors.Is(sysErr, txnErr) {
		// Chain broken: TXN error not found from SYS level
		t.Error("Chain broken: SYS error should contain TXN error")
	}

	// Kind must be traversable
	if !IsKind(sysErr, KindLocked) {
		t.Error("IsKind should find KindLocked in chain")
	}

	// Classification must work on top-level error
	c := Classify(sysErr)
	if c.Retryable != true {
		// Retryable classification incorrect for locked error
		t.Errorf("Classify(sysErr).Retryable = %v, want true", c.Retryable)
	}
	if c.Fatal != false {
		// Fatal classification incorrect for locked error
		t.Errorf("Classify(sysErr).Fatal = %v, want false", c.Fatal)
	}
}

// TestEC_WrapChainWithFmtError tests that fmt.Errorf wrappers are handled
// correctly in the chain (common when callers wrap our errors with fmt.Errorf).
func TestEC_WrapChainWithFmtError(t *testing.T) {
	ecErr := New(KindNotFound, "key not found")
	ecErr.Module = "WAL/RP"
	ecErr.Layer = LayerWAL

	// Caller wraps with fmt.Errorf (common pattern)
	wrapped := fmt.Errorf("replay failed: %w", ecErr)

	// fmt.Errorf wrapper should not break our chain traversal
	if !IsKind(wrapped, KindNotFound) {
		// IsKind failed through fmt.Errorf wrapper
		t.Error("IsKind should traverse through fmt.Errorf wrapper")
	}

	if !errors.Is(wrapped, ecErr) {
		// Standard errors.Is should also work
		t.Error("errors.Is should find EC error through fmt.Errorf")
	}

	// Classification should work
	c := Classify(wrapped)
	if c.Kind != KindNotFound {
		// Classify failed through fmt.Errorf wrapper
		t.Errorf("Classify.Kind = %v, want KindNotFound", c.Kind)
	}
}

// TestEC_NilChainBehavior tests behavior when wrapping nil errors.
func TestEC_NilChainBehavior(t *testing.T) {
	// Wrap(nil) should return nil (no panic)
	if e := Wrap(KindIO, nil); e != nil {
		t.Errorf("Wrap(nil) should return nil, got %v", e)
	}

	// Wrapf(nil) should return nil
	if e := Wrapf(KindIO, nil, "msg"); e != nil {
		t.Errorf("Wrapf(nil) should return nil, got %v", e)
	}

	// WrapAt(nil) should return nil
	if e := WrapAt(KindIO, "MOD", LayerSQL, nil); e != nil {
		t.Errorf("WrapAt(nil) should return nil, got %v", e)
	}

	// IsKind(nil) should be false
	if IsKind(nil, KindIO) {
		t.Error("IsKind(nil) should be false")
	}

	// Classify(nil) should return zero Classification
	c := Classify(nil)
	if c.Kind != 0 || c.Retryable || c.Fatal {
		t.Errorf("Classify(nil) should return zero, got %+v", c)
	}
}
