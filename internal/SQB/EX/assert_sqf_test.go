//go:build debug

package EX

import (
	"math"
	"os"
	"os/exec"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestSQF_Assert_CostBounds(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		UnregisterAll()
		DT.RegisterTable("t_cost", []DT.Row{
			{Cols: []string{"id"}, Types: []LX.TokenType{LX.T_INT}, Data: []DT.Value{NewIntValue(1)}},
		})
		p := NewPlanner()
		p.SetCostParams(CostParams{
			SeqPageCost: math.NaN(),
		})
		s := OP.NewSeqScan("t_cost")
		p.estimateCost(s)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSQF_Assert_CostBounds")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestSQF_Assert_CostBounds_NoFire(t *testing.T) {
	UnregisterAll()
	DT.RegisterTable("t_cost_valid", []DT.Row{
		{Cols: []string{"id"}, Types: []LX.TokenType{LX.T_INT}, Data: []DT.Value{NewIntValue(1)}},
	})
	p := NewPlanner()
	p.SetCostParams(DefaultCostParams())
	s := OP.NewSeqScan("t_cost_valid")
	cost := p.estimateCost(s)
	if cost <= 0 {
		t.Errorf("expected positive cost, got %v", cost)
	}
}
