//go:build debug

package fs

import (
	"testing"
)

func TestFIL_Assert_StubNoop(t *testing.T) {
	// Verify that WARN_ON and BUG_ON don't fire when the condition is false.
	// This tests the ValidatePath path traversal detection.
	pv, err := newPathValidator("/tmp")
	if err != nil {
		t.Fatal(err)
	}

	// Valid paths should not trigger any assertion.
	if err := pv.Validate("valid/path"); err != nil {
		t.Fatalf("unexpected error for valid path: %v", err)
	}
	if err := pv.Validate("another/normal/path.txt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Path traversal should still return an error (the WARN_ON is just a debug aid).
	if err := pv.Validate("../escape"); err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if err := pv.Validate("foo/../../bar"); err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
}