package CO

import (
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQO/CP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestNew(t *testing.T) {
	o := New(nil)
	if o == nil {
		t.Fatal("expected non-nil optimizer")
	}
}

func TestNewWithOptions(t *testing.T) {
	o := NewWithOptions(Options{CacheSize: 100})
	if o == nil {
		t.Fatal("expected non-nil optimizer")
	}
}

func TestPlan_NoBuilder(t *testing.T) {
	o := New(nil)
	_, err := o.Plan(&PS.Select{From: "t1"})
	if err == nil {
		t.Fatal("expected error without PlanBuilder")
	}
}

func TestPlan_WithBuilder(t *testing.T) {
	o := New(nil)
	o.SetBuilder(func(stmt PS.Stmt) (DT.Operator, error) {
		return nil, nil
	})
	op, err := o.Plan(&PS.Select{From: "t1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op != nil {
		t.Fatal("expected nil operator")
	}
}

func TestPlan_WithBuilderError(t *testing.T) {
	o := New(nil)
	o.SetBuilder(func(stmt PS.Stmt) (DT.Operator, error) {
		return nil, nil
	})
	op, err := o.Plan(&PS.Select{From: "t1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op != nil {
		t.Fatal("expected nil operator")
	}
}

func TestSetCostParams(t *testing.T) {
	o := New(nil)
	cp := CP.Default()
	cp.SeqPageCost = 2.0
	o.SetCostParams(cp)
}

func TestSetStatsCatalog(t *testing.T) {
	o := New(nil)
	o.SetStatsCatalog(nil)
}

func TestInvalidateCache(t *testing.T) {
	o := New(nil)
	o.InvalidateCache()
}

func TestRegisterTable(t *testing.T) {
	o := New(nil)
	o.RegisterTable("t1", []DT.ColInfo{{Name: "id", Typ: 1}}, "id")
}

func TestRegisterIndex(t *testing.T) {
	o := New(nil)
	o.RegisterTable("t1", []DT.ColInfo{{Name: "id", Typ: 1}}, "id")
	o.RegisterIndex("t1", "idx_id", []string{"id"})
	o.RegisterIndex("nonexistent", "idx", nil)
}

func TestSetBuilder(t *testing.T) {
	o := New(nil)
	o.SetBuilder(func(stmt PS.Stmt) (DT.Operator, error) {
		return nil, nil
	})
}

func TestPlan_RewriteError(t *testing.T) {
	o := New(nil)
	o.SetBuilder(func(stmt PS.Stmt) (DT.Operator, error) {
		return nil, nil
	})
	_, err := o.Plan(nil)
	if err == nil {
		t.Fatal("expected error for nil statement")
	}
}