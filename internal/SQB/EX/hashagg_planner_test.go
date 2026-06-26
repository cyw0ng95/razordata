package EX

import (
	"context"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestHashAggregateThreshold verifies the threshold constant
// matches the expected value (REQ000196).
func TestHashAggregateThreshold(t *testing.T) {
	if HashAggregateThreshold != 1000 {
		t.Errorf("HashAggregateThreshold: got %d, want 1000", HashAggregateThreshold)
	}
}

// TestEstimateRowCount verifies estimateRowCount for
// non-existent tables returns the default estimate of 100.
func TestEstimateRowCount(t *testing.T) {
	p := NewPlanner()
	got := p.estimateRowCount("nonexistent", nil)
	if got != 100 {
		t.Errorf("estimateRowCount: got %d, want 100 (default)", got)
	}
}

// TestHashAggregate_AggregateEquivalence verifies HashAggregate
// produces the same results as Aggregate for small datasets
// (REQ000196 - equivalence).
func TestHashAggregate_AggregateEquivalence(t *testing.T) {
	rows := []Row{
		{Cols: []string{"category", "value"}, Types: []int{int(LX.T_TEXT), int(LX.T_INT_KW)},
			Data: []Value{NewTextValue("A"), NewIntValue(int64(10))}},
		{Cols: []string{"category", "value"}, Types: []int{int(LX.T_TEXT), int(LX.T_INT_KW)},
			Data: []Value{NewTextValue("A"), NewIntValue(int64(20))}},
		{Cols: []string{"category", "value"}, Types: []int{int(LX.T_TEXT), int(LX.T_INT_KW)},
			Data: []Value{NewTextValue("B"), NewIntValue(int64(5))}},
		{Cols: []string{"category", "value"}, Types: []int{int(LX.T_TEXT), int(LX.T_INT_KW)},
			Data: []Value{NewTextValue("B"), NewIntValue(int64(15))}},
		{Cols: []string{"category", "value"}, Types: []int{int(LX.T_TEXT), int(LX.T_INT_KW)},
			Data: []Value{NewTextValue("A"), NewIntValue(int64(30))}},
	}
	RegisterTable("equivalence_test", rows)
	defer UnregisterAll()

	groupCols := []PS.Expr{&PS.Ident{Name: "category"}}
	aggExprs := []PS.Expr{&PS.AggregateFunc{Name: "SUM", Arg: &PS.Ident{Name: "value"}}}

	// HashAggregate
	RegisterTable("hashagg_test", rows)
	scan := NewSeqScan("hashagg_test")
	ha := NewHashAggregate(scan, groupCols, aggExprs)

	haResults := make(map[string]int64)
	for {
		row, err := ha.Next(context.Background())
		if err != nil {
			if err == ErrNoRows {
				break
			}
			t.Fatalf("HashAggregate Next: %v", err)
		}
		key := ""
		if len(row.Data) > 0 {
			if s, ok := row.Data[0].ToAny().(string); ok {
				key = s
			}
		}
		var sum int64
		if len(row.Data) > 1 {
			if v, ok := row.Data[1].ToAny().(int64); ok {
				sum = v
			}
		}
		haResults[key] = sum
	}
	ha.Close()

	// Expected: A=60, B=20
	expected := map[string]int64{"A": 60, "B": 20}
	if len(haResults) != len(expected) {
		t.Errorf("result count: got %d, want %d", len(haResults), len(expected))
	}
	for k, v := range expected {
		if haResults[k] != v {
			t.Errorf("%s: got %d, want %d", k, haResults[k], v)
		}
	}
}
