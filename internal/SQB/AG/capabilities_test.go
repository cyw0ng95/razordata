package AG

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestAggregate_SetChild verifies the SetChild adapter.
func TestAggregate_SetChild(t *testing.T) {
	a := NewAggregate(nil, nil, nil)
	if a.Child() != nil {
		t.Error("fresh Aggregate.Child() should be nil")
	}
	// SetChild with a DT.Operator.
	a.SetChild(nil)
	if a.Child() != nil {
		t.Error("SetChild(nil) should set child to nil")
	}
}

// TestAggregate_GroupCols verifies the group-cols accessor.
func TestAggregate_GroupCols(t *testing.T) {
	a := NewAggregate(nil, []PS.Expr{
		&PS.QualifiedName{Name: "a"},
		&PS.QualifiedName{Name: "b"},
	}, nil)
	got := a.GroupCols()
	if len(got) != 2 {
		t.Fatalf("GroupCols len = %d, want 2", len(got))
	}
	if qn, ok := got[0].(*PS.QualifiedName); !ok || qn.Name != "a" {
		t.Errorf("GroupCols[0] = %T, want *PS.QualifiedName{Name:a}", got[0])
	}
}

// TestAggregate_GetAggregates verifies the aggregate-spec adapter.
func TestAggregate_GetAggregates(t *testing.T) {
	a := NewAggregate(nil, nil, []PS.Expr{
		&PS.FunctionCall{Name: "count", Args: []PS.Expr{&PS.StarExpr{}}},
		&PS.FunctionCall{Name: "sum", Args: []PS.Expr{&PS.QualifiedName{Name: "x"}}},
	})
	got := a.Aggregates()
	if len(got) != 2 {
		t.Fatalf("Aggregates len = %d, want 2", len(got))
	}
	if got[0].FuncName != "count" {
		t.Errorf("Aggregates[0].FuncName = %q, want count", got[0].FuncName)
	}
	if got[1].FuncName != "sum" || got[1].Arg != "x" {
		t.Errorf("Aggregates[1] = %+v, want sum(x)", got[1])
	}
}

// TestHashAggregate_SetChild verifies the SetChild adapter.
func TestHashAggregate_SetChild(t *testing.T) {
	a := NewAggregate(nil, nil, nil)
	if a.Child() != nil {
		t.Error("fresh HashAggregate.Child() should be nil")
	}
	// Verify it satisfies DT.Operator.
	var _ DT.Operator = a
}

// TestHashAggregate_GroupCols verifies the group-cols interface.
func TestHashAggregate_GroupCols(t *testing.T) {
	a := NewAggregate(nil, []PS.Expr{
		&PS.QualifiedName{Name: "k"},
	}, nil)
	got := a.GroupCols()
	if len(got) != 1 {
		t.Fatalf("GroupCols len = %d, want 1", len(got))
	}
	if qn, ok := got[0].(*PS.QualifiedName); !ok || qn.Name != "k" {
		t.Errorf("GroupCols[0] = %T, want *PS.QualifiedName{Name:k}", got[0])
	}
}
