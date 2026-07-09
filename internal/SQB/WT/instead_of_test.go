package WT

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestFindInsteadOfTrigger_Insert(t *testing.T) {
	trig := &PS.TriggerStmt{
		Name:    "tr_view_insert",
		OnTable: "v_test",
		Time:    "INSTEAD OF",
		Event:   "INSERT",
		Body:    []PS.Stmt{},
	}
	RegisterTrigger(trig)
	defer DT.UnregisterAllTriggers()

	got := FindInsteadOfTrigger("v_test", "INSERT")
	if got == nil {
		t.Fatal("FindInsteadOfTrigger returned nil, want trigger")
	}
	if got.Name != "tr_view_insert" {
		t.Errorf("got trigger %q, want %q", got.Name, "tr_view_insert")
	}
}

func TestFindInsteadOfTrigger_NotFound(t *testing.T) {
	got := FindInsteadOfTrigger("nonexistent", "INSERT")
	if got != nil {
		t.Errorf("FindInsteadOfTrigger = %v, want nil", got)
	}
}

func TestFindInsteadOfTrigger_WrongEvent(t *testing.T) {
	trig := &PS.TriggerStmt{
		Name:    "tr_view_insert",
		OnTable: "v_test",
		Time:    "INSTEAD OF",
		Event:   "INSERT",
		Body:    []PS.Stmt{},
	}
	RegisterTrigger(trig)
	defer DT.UnregisterAllTriggers()

	got := FindInsteadOfTrigger("v_test", "UPDATE")
	if got != nil {
		t.Errorf("FindInsteadOfTrigger for UPDATE = %v, want nil", got)
	}
}

func TestFindInsteadOfTrigger_AfterTriggerIgnored(t *testing.T) {
	trig := &PS.TriggerStmt{
		Name:    "tr_after",
		OnTable: "t",
		Time:    "AFTER",
		Event:   "INSERT",
		Body:    []PS.Stmt{},
	}
	RegisterTrigger(trig)
	defer DT.UnregisterAllTriggers()

	got := FindInsteadOfTrigger("t", "INSERT")
	if got != nil {
		t.Errorf("FindInsteadOfTrigger for AFTER trigger = %v, want nil", got)
	}
}

func TestInsteadOfInsert_Operator(t *testing.T) {
	trig := &PS.TriggerStmt{
		Name:    "tr_view_ins",
		OnTable: "v_test",
		Time:    "INSTEAD OF",
		Event:   "INSERT",
		Body:    []PS.Stmt{},
	}
	RegisterTrigger(trig)
	defer DT.UnregisterAllTriggers()

	op := NewInsteadOfInsert("v_test", trig)
	if op == nil {
		t.Fatal("NewInsteadOfInsert returned nil")
	}
	if op.RowsAffected() != 0 {
		t.Errorf("RowsAffected before Next = %d, want 0", op.RowsAffected())
	}
}

func TestInsteadOfUpdate_Operator(t *testing.T) {
	trig := &PS.TriggerStmt{
		Name:    "tr_view_upd",
		OnTable: "v_test",
		Time:    "INSTEAD OF",
		Event:   "UPDATE",
		Body:    []PS.Stmt{},
	}
	RegisterTrigger(trig)
	defer DT.UnregisterAllTriggers()

	op := NewInsteadOfUpdate("v_test", trig)
	if op == nil {
		t.Fatal("NewInsteadOfUpdate returned nil")
	}
}

func TestInsteadOfDelete_Operator(t *testing.T) {
	trig := &PS.TriggerStmt{
		Name:    "tr_view_del",
		OnTable: "v_test",
		Time:    "INSTEAD OF",
		Event:   "DELETE",
		Body:    []PS.Stmt{},
	}
	RegisterTrigger(trig)
	defer DT.UnregisterAllTriggers()

	op := NewInsteadOfDelete("v_test", trig)
	if op == nil {
		t.Fatal("NewInsteadOfDelete returned nil")
	}
}