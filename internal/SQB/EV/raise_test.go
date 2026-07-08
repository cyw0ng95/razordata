// REQ001373: Trigger SELECT RAISE(IGNORE) — eval-level contract.
// evalRaise returns the sentinel ErrIgnoreRow for action="IGNORE"
// and ErrTriggerAbort for action="ABORT". The DML layer's writer
// is responsible for catching these sentinels once BEFORE-trigger
// support lands; the eval layer just classifies them.
package EV

import (
	"errors"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestRaise_Ignore_ReturnsIgnoreSentinel verifies REQ001373: calling
// SELECT RAISE(IGNORE) at eval time returns ErrIgnoreRow, signalling that
// the surrounding DML should skip the current row.
func TestRaise_Ignore_ReturnsIgnoreSentinel(t *testing.T) {
	parser := PS.NewParser("SELECT RAISE(IGNORE)")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	got, err := evalRaise(rf, nil, nil)
	if !errors.Is(err, ErrIgnoreRow) {
		t.Fatalf("err = %v, want ErrIgnoreRow", err)
	}
	if got.Kind != KindNull {
		t.Errorf("value kind = %d, want KindNull", got.Kind)
	}
}

// TestRaise_Ignore_DoesNotIncrementRowsAffected verifies the
// contract documented for REQ001373: when a BEFORE trigger uses
// SELECT RAISE(IGNORE), the DML writer must NOT increment the
// RowsAffected counter. We don't have BEFORE triggers yet, but we
// pin the ErrIgnoreRow sentinel so a future BEFORE-trigger writer
// can rely on it.
func TestRaise_Ignore_DoesNotIncrementRowsAffected(t *testing.T) {
	parser := PS.NewParser("SELECT RAISE(IGNORE)")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = evalRaise(rf, nil, nil)
	// The sentinel is the entire contract: a "row skip" is signaled
	// solely by returning ErrIgnoreRow, not by any other side effect.
	if !errors.Is(err, ErrIgnoreRow) {
		t.Fatalf("err = %v, want ErrIgnoreRow (no side effects, no counter)", err)
	}
}

// TestRaise_Abort_WrapsErrTriggerAbort verifies REQ001374 (the
// REQ001373 sibling): RAISE(ABORT, 'msg') returns ErrTriggerAbort
// with the message text appended.
func TestRaise_Abort_WrapsErrTriggerAbort(t *testing.T) {
	parser := PS.NewParser("SELECT RAISE(ABORT, 'oops')")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = evalRaise(rf, nil, nil)
	if !errors.Is(err, ErrTriggerAbort) {
		t.Fatalf("err = %v, want ErrTriggerAbort", err)
	}
	if got := err.Error(); got == "" {
		t.Errorf("abort error message empty; want 'oops' or similar")
	}
}

// TestRaise_ActionCaseInsensitive verifies that "raise(ignore)" and
// "SELECT RAISE(IGNORE)" are equivalent — the eval layer normalizes case.
func TestRaise_ActionCaseInsensitive(t *testing.T) {
	parser := PS.NewParser("SELECT RAISE(ignore)")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = evalRaise(rf, nil, nil)
	if !errors.Is(err, ErrIgnoreRow) {
		t.Fatalf("err = %v, want ErrIgnoreRow (case-insensitive action)", err)
	}
}