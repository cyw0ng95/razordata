package CO

import (
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TestGroupBushyJoins_TransitiveClosure_StarSchema verifies REQ001452:
// a star schema (hub + 3 leaves, only hub-leaf predicates) groups
// all tables together via transitive closure.
func TestGroupBushyJoins_TransitiveClosure_StarSchema(t *testing.T) {
	// Star: hub=t1, leaves=t2,t3,t4. Predicates: t1.a=t2.a, t1.a=t3.a, t1.a=t4.a.
	// No direct t2-t3, t2-t4, t3-t4 predicates. Without transitive closure,
	// N3 would see t2,t3,t4 as independent and create separate groups.
	preds := []PS.Expr{
		&PS.BinaryExpr{
			Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
			Op:    LX.T_EQ,
			Right: &PS.QualifiedName{Table: "t2", Name: "a"},
		},
		&PS.BinaryExpr{
			Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
			Op:    LX.T_EQ,
			Right: &PS.QualifiedName{Table: "t3", Name: "a"},
		},
		&PS.BinaryExpr{
			Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
			Op:    LX.T_EQ,
			Right: &PS.QualifiedName{Table: "t4", Name: "a"},
		},
	}
	extract := func(e PS.Expr) (string, string) {
		if qn, ok := e.(*PS.QualifiedName); ok {
			return qn.Table, qn.Name
		}
		return "", ""
	}
	groups := GroupBushyJoins("t1", []string{"t1", "t2", "t3", "t4"}, preds, extract)
	// All tables should be in one group.
	if len(groups) != 1 {
		t.Fatalf("expected 1 group for star schema, got %d: %v", len(groups), groups)
	}
	if len(groups[0]) != 4 {
		t.Errorf("expected 4 tables in group, got %d: %v", len(groups[0]), groups[0])
	}
}

// TestGroupBushyJoins_TransitiveClosure_Chain verifies REQ001452:
// A-B-C chain (A-B, B-C predicates) produces one group, not two.
func TestGroupBushyJoins_TransitiveClosure_Chain(t *testing.T) {
	preds := []PS.Expr{
		&PS.BinaryExpr{
			Left:  &PS.QualifiedName{Table: "t1", Name: "a"},
			Op:    LX.T_EQ,
			Right: &PS.QualifiedName{Table: "t2", Name: "a"},
		},
		&PS.BinaryExpr{
			Left:  &PS.QualifiedName{Table: "t2", Name: "a"},
			Op:    LX.T_EQ,
			Right: &PS.QualifiedName{Table: "t3", Name: "a"},
		},
	}
	extract := func(e PS.Expr) (string, string) {
		if qn, ok := e.(*PS.QualifiedName); ok {
			return qn.Table, qn.Name
		}
		return "", ""
	}
	groups := GroupBushyJoins("t1", []string{"t1", "t2", "t3"}, preds, extract)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group for chain, got %d: %v", len(groups), groups)
	}
	if len(groups[0]) != 3 {
		t.Errorf("expected 3 tables in group, got %d: %v", len(groups[0]), groups[0])
	}
}
