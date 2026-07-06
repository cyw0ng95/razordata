//go:build debug

package EX

import (
	"context"
	"os"
	"os/exec"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestSQB_Assert_OperatorUseAfterClose(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		UnregisterAll()
		DT.RegisterTable("t_assert", []DT.Row{
			{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(1)}},
		})
		s := OP.NewSeqScan("t_assert")
		s.Close()
		s.Next(context.Background()) // should BUG_ON → os.Exit(1)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSQB_Assert_OperatorUseAfterClose")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestSQB_Assert_ColOffsetBounds(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		row := DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(1)}}
		expr := &PS.Ident{Name: "id", SlotIdx: 5} // out of bounds
		EV.EvalValue(expr, &row, nil) // should BUG_ON
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSQB_Assert_ColOffsetBounds")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestSQB_Assert_RowIntegrity(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		r := DT.Row{Data: []DT.Value{NewIntValue(1)}, Types: nil} // Data len=1 != Types len=0
		DT.CloneRow(r) // should BUG_ON
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSQB_Assert_RowIntegrity")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestSQB_Assert_WarnOnEvalValueNilRow(t *testing.T) {
	// WARN_ON does not crash — test directly.
	_, err := EV.EvalValue(&PS.NumberLiteral{Val: 42}, nil, nil)
	if err != nil {
		t.Errorf("EvalValue with nil row and literal: unexpected error: %v", err)
	}
}
