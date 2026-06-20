package AP

import (
	"errors"
	"fmt"
	"testing"
)

func TestError_KindString(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{KindNotFound, "NotFound"},
		{KindDuplicateKey, "DuplicateKey"},
		{KindLocked, "Locked"},
		{KindCorrupt, "Corrupt"},
		{KindSyntax, "Syntax"},
		{KindTypeMismatch, "TypeMismatch"},
		{KindTxAborted, "TxAborted"},
		{KindIO, "IO"},
		{KindUpgradeRequired, "UpgradeRequired"},
		{KindReadOnly, "ReadOnly"},
		{KindDeadlineExceeded, "DeadlineExceeded"},
		{KindConstraint, "Constraint"},
		{KindClosed, "Closed"},
		{KindInvalidOptions, "InvalidOptions"},
		{Kind(999), "Kind(999)"},
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(tc.kind), got, tc.want)
		}
	}
}

func TestError_New(t *testing.T) {
	e := New(KindNotFound, "key missing")
	if e.Kind != KindNotFound {
		t.Errorf("Kind = %v, want KindNotFound", e.Kind)
	}
	if e.Message != "key missing" {
		t.Errorf("Message = %q, want %q", e.Message, "key missing")
	}
	if e.wrapped != nil {
		t.Error("wrapped should be nil for New")
	}
	want := "NotFound: key missing"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestError_Wrap(t *testing.T) {
	inner := fmt.Errorf("disk broken")
	e := Wrap(KindIO, inner)
	if e.Kind != KindIO {
		t.Errorf("Kind = %v, want KindIO", e.Kind)
	}
	if e.Unwrap() != inner {
		t.Error("Unwrap should return the inner error")
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should match the inner error")
	}
}

func TestError_WrapNil(t *testing.T) {
	if e := Wrap(KindIO, nil); e != nil {
		t.Errorf("Wrap(nil) should return nil, got %v", e)
	}
}

func TestIsKind(t *testing.T) {
	e := New(KindCorrupt, "bad data")
	if !IsKind(e, KindCorrupt) {
		t.Error("IsKind should match direct error")
	}
	if IsKind(e, KindIO) {
		t.Error("IsKind should not match different kind")
	}

	wrapped := fmt.Errorf("context: %w", e)
	if !IsKind(wrapped, KindCorrupt) {
		t.Error("IsKind should traverse wrapped chain")
	}

	if IsKind(nil, KindCorrupt) {
		t.Error("IsKind(nil) should be false")
	}
}

func TestIsKind_BackwardCompat(t *testing.T) {
	// Sentinels created via New should match with errors.Is
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Error("errors.Is(ErrNotFound, ErrNotFound) should be true")
	}
	// Wrapped sentinel should still match
	wrapped := fmt.Errorf("wrap: %w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is(wrapped, ErrNotFound) should be true")
	}
	if !IsKind(wrapped, KindNotFound) {
		t.Error("IsKind(wrapped, KindNotFound) should be true")
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		err     error
		retry   bool
	}{
		{ErrIO, true},
		{ErrLocked, true},
		{ErrNotFound, false},
		{ErrCorrupt, false},
		{ErrClosed, false},
		{fmt.Errorf("wrap: %w", ErrIO), true},
		{fmt.Errorf("wrap: %w", ErrLocked), true},
		{nil, false},
		{fmt.Errorf("unknown"), false},
	}
	for _, tc := range cases {
		if got := IsRetryable(tc.err); got != tc.retry {
			t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.retry)
		}
	}
}

func TestIsFatal(t *testing.T) {
	cases := []struct {
		err  error
		fatal bool
	}{
		{ErrIO, false},
		{ErrLocked, false},
		{ErrNotFound, true},
		{ErrCorrupt, true},
		{ErrClosed, true},
		{fmt.Errorf("wrap: %w", ErrCorrupt), true},
		{nil, false},
		{fmt.Errorf("unknown"), true},
	}
	for _, tc := range cases {
		if got := IsFatal(tc.err); got != tc.fatal {
			t.Errorf("IsFatal(%v) = %v, want %v", tc.err, got, tc.fatal)
		}
	}
}

func TestSentinel_KindMatch(t *testing.T) {
	// Every sentinel should have the correct Kind
	cases := []struct {
		err  *Error
		kind Kind
	}{
		{ErrNotFound, KindNotFound},
		{ErrDuplicateKey, KindDuplicateKey},
		{ErrLocked, KindLocked},
		{ErrCorrupt, KindCorrupt},
		{ErrSyntax, KindSyntax},
		{ErrTypeMismatch, KindTypeMismatch},
		{ErrTxAborted, KindTxAborted},
		{ErrIO, KindIO},
		{ErrUpgradeRequired, KindUpgradeRequired},
		{ErrReadOnly, KindReadOnly},
		{ErrDeadlineExceeded, KindDeadlineExceeded},
		{ErrAlreadyOpen, KindInvalidOptions},
		{ErrNotOpen, KindClosed},
		{ErrClosed, KindClosed},
		{ErrInvalidOptions, KindInvalidOptions},
		{ErrNoActiveTxn, KindConstraint},
		{ErrUnknownSavepoint, KindConstraint},
		{ErrConstraint, KindConstraint},
	}
	for _, tc := range cases {
		if tc.err.Kind != tc.kind {
			t.Errorf("%v.Kind = %v, want %v", tc.err, tc.err.Kind, tc.kind)
		}
		if !IsKind(tc.err, tc.kind) {
			t.Errorf("IsKind(%v, %v) = false", tc.err, tc.kind)
		}
	}
}
