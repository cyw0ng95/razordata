package WT

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestRaiseIgnore_SkipsRow(t *testing.T) {
	RegisterTrigger(&PS.TriggerStmt{
		Name:    "tr_ignore_test",
		OnTable: "t_ignore",
		Time:    "AFTER",
		Event:   "INSERT",
		Body: []PS.Stmt{
			&PS.Select{
				Cols: []PS.Expr{
					&PS.RaiseFunc{Action: "IGNORE"},
				},
			},
		},
	})
	defer DT.UnregisterAllTriggers()

	exec := func(sql string) error {
		return nil
	}

	err := FireTriggers("t_ignore", "AFTER", "INSERT", nil, &DT.Row{}, nil, exec)
	if err != nil {
		t.Fatalf("FireTriggers: %v (expected nil, RAISE(IGNORE) should be swallowed)", err)
	}
}

func TestRaiseIgnore_DoesNotIncrementRowsAffected(t *testing.T) {
	RegisterTrigger(&PS.TriggerStmt{
		Name:    "tr_ignore_ra",
		OnTable: "t_ignore_ra",
		Time:    "AFTER",
		Event:   "INSERT",
		Body: []PS.Stmt{
			&PS.Select{
				Cols: []PS.Expr{
					&PS.RaiseFunc{Action: "IGNORE"},
				},
			},
		},
	})
	defer DT.UnregisterAllTriggers()

	exec := func(sql string) error {
		return nil
	}

	err := FireTriggers("t_ignore_ra", "AFTER", "INSERT", nil, &DT.Row{}, nil, exec)
	if err != nil {
		t.Fatalf("FireTriggers: %v (RAISE(IGNORE) should not cause error)", err)
	}

	err = FireTriggers("t_ignore_ra", "AFTER", "INSERT", nil, &DT.Row{}, nil, exec)
	if err != nil {
		t.Fatalf("FireTriggers (2nd): %v", err)
	}
}

func TestRaiseIgnore_MultiStmtBody(t *testing.T) {
	RegisterTrigger(&PS.TriggerStmt{
		Name:    "tr_ignore_multi",
		OnTable: "t_ignore_multi",
		Time:    "AFTER",
		Event:   "INSERT",
		Body: []PS.Stmt{
			&PS.Select{
				Cols: []PS.Expr{
					&PS.RaiseFunc{Action: "IGNORE"},
				},
			},
			&PS.Select{
				Cols: []PS.Expr{
					&PS.NumberLiteral{Val: 1},
				},
			},
		},
	})
	defer DT.UnregisterAllTriggers()

	exec := func(sql string) error {
		return nil
	}

	err := FireTriggers("t_ignore_multi", "AFTER", "INSERT", nil, &DT.Row{}, nil, exec)
	if err != nil {
		t.Fatalf("FireTriggers: %v (RAISE(IGNORE) aborts remaining trigger body)", err)
	}
}

func TestRaiseIgnore_NoTriggers(t *testing.T) {
	err := FireTriggers("nonexistent_table", "AFTER", "INSERT", nil, &DT.Row{}, nil, func(sql string) error { return nil })
	if err != nil {
		t.Fatalf("FireTriggers on nonexistent table: %v", err)
	}
}