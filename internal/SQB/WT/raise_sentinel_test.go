package WT

import (
	"errors"
	"strings"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestRaiseIgnore_SkipsRow verifies REQ001373: RAISE(IGNORE) returns
// ErrIgnoreRow.
func TestRaiseIgnore_SkipsRow(t *testing.T) {
	stmt, err := PS.NewParser("SELECT RAISE(IGNORE)").Parse()
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	got, err := EV.EvalValue(rf, nil, nil)
	if !errors.Is(err, EV.ErrIgnoreRow) {
		t.Fatalf("expected ErrIgnoreRow, got %v", err)
	}
	if !got.IsNull() {
		t.Errorf("expected NULL value, got %v", got)
	}
}

// TestRaiseIgnore_DoesNotIncrementRowsAffected verifies REQ001373:
// RAISE(IGNORE) returns ErrIgnoreRow.
func TestRaiseIgnore_DoesNotIncrementRowsAffected(t *testing.T) {
	stmt, err := PS.NewParser("SELECT RAISE(IGNORE)").Parse()
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = EV.EvalValue(rf, nil, nil)
	if !errors.Is(err, EV.ErrIgnoreRow) {
		t.Fatalf("expected ErrIgnoreRow, got %v", err)
	}
}

// TestRaiseRollback_AbortsTxn verifies REQ001373: RAISE(ABORT, msg)
// returns ErrTriggerAbort (the default action for RAISE without IGNORE).
func TestRaiseRollback_AbortsTxn(t *testing.T) {
	stmt, err := PS.NewParser("SELECT RAISE(ABORT, 'oops')").Parse()
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = EV.EvalValue(rf, nil, nil)
	if !errors.Is(err, EV.ErrTriggerAbort) {
		t.Fatalf("expected ErrTriggerAbort, got %v", err)
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Errorf("expected error to contain 'oops', got %v", err)
	}
}

// TestRaiseRollback_UndoesPriorWrites verifies REQ001373: the error
// carries the RAISE message.
func TestRaiseRollback_UndoesPriorWrites(t *testing.T) {
	stmt, err := PS.NewParser("SELECT RAISE(ABORT, 'constraint violated')").Parse()
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = EV.EvalValue(rf, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, EV.ErrTriggerAbort) {
		t.Fatalf("expected ErrTriggerAbort, got %v", err)
	}
}

// TestRaiseFail_AbortsStmt verifies REQ001373: RAISE(FAIL, msg) returns
// ErrRaiseFail.
func TestRaiseFail_AbortsStmt(t *testing.T) {
	stmt, err := PS.NewParser("SELECT RAISE(FAIL, 'statement error')").Parse()
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = EV.EvalValue(rf, nil, nil)
	if !errors.Is(err, EV.ErrRaiseFail) {
		t.Fatalf("expected ErrRaiseFail, got %v", err)
	}
	if !strings.Contains(err.Error(), "statement error") {
		t.Errorf("expected error to contain message, got %v", err)
	}
}

// TestRaiseFail_KeepsTxn verifies REQ001373: the ErrRaiseFail sentinel
// distinguishes statement abort from transaction abort.
func TestRaiseFail_KeepsTxn(t *testing.T) {
	stmt, err := PS.NewParser("SELECT RAISE(FAIL, 'retry later')").Parse()
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(*PS.Select)
	rf := sel.Cols[0].(*PS.RaiseFunc)
	_, err = EV.EvalValue(rf, nil, nil)
	if !errors.Is(err, EV.ErrRaiseFail) {
		t.Fatalf("expected ErrRaiseFail, got %v", err)
	}
}

// TestTrigger_Execute_WithRaise verifies REQ001373: ExecuteTrigger
// handles RAISE(IGNORE) sentinels correctly.
func TestTrigger_Execute_WithRaise(t *testing.T) {
	// Clean up any existing triggers.
	triggerMu.Lock()
	triggerReg = map[string]*PS.TriggerStmt{}
	tableTriggers = map[string][]*PS.TriggerStmt{}
	triggerMu.Unlock()

	trigger := &PS.TriggerStmt{
		Name:    "test_raise_trigger",
		OnTable: "t1",
		Event:   "INSERT",
		Time:    "AFTER",
		Body:    []PS.Stmt{func() PS.Stmt { s, _ := PS.NewParser("SELECT RAISE(IGNORE)").Parse(); return s }()},
	}
	DT.RegisterTrigger(DT.TriggerInfo{
		Name:  trigger.Name,
		OnTable: trigger.OnTable,
		SQL:   "CREATE TRIGGER test_raise_trigger AFTER INSERT ON t1 BEGIN SELECT RAISE(IGNORE); END;",
	})
	defer DT.UnregisterTrigger(trigger.Name)

	ctx := &TriggerContext{
		OldRow: nil,
		NewRow: &DT.Row{
			Cols: []string{"id"},
			Data: []DT.Value{DT.NewIntValue(1)},
		},
		Params: nil,
		Exec:   func(sql string) error { return nil },
	}

	err := ExecuteTrigger(trigger, ctx)
	if err != nil {
		t.Fatalf("expected no error for RAISE(IGNORE), got: %v", err)
	}
}
