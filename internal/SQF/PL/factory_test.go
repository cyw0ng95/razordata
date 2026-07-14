package PL

import (
	"context"
	"testing"
)

// noopOp is a no-op Operator for compile-time interface checks.
type noopOp struct{}

func (noopOp) Next(_ context.Context) (Row, error) { return Row{}, ErrNoRows }
func (noopOp) Close() error                       { return nil }

func TestOperatorFactory_InterfaceCompiles(t *testing.T) {
	// Compile-time assertion: nil OperatorFactory satisfies the
	// interface (no implementation required at this layer).
	var _ OperatorFactory = (OperatorFactory)(nil)
}

func TestJoinType_Constants(t *testing.T) {
	// Verify the enum values are unique and non-negative.
	seen := map[JoinType]bool{}
	for _, v := range []JoinType{InnerJoin, LeftJoin, RightJoin, FullJoin, SemiJoin, AntiJoin} {
		if v < 0 {
			t.Errorf("JoinType %d is negative", v)
		}
		if seen[v] {
			t.Errorf("JoinType %d duplicated", v)
		}
		seen[v] = true
	}
}

func TestSetOpType_Constants(t *testing.T) {
	seen := map[SetOpType]bool{}
	for _, v := range []SetOpType{UnionAllOp, UnionOp, IntersectOp, ExceptOp} {
		if v < 0 {
			t.Errorf("SetOpType %d is negative", v)
		}
		if seen[v] {
			t.Errorf("SetOpType %d duplicated", v)
		}
		seen[v] = true
	}
}

func TestOptionalInterfaces_NilSatisfies(t *testing.T) {
	// Each optional interface must accept a nil dynamic value so
	// type-assertion sites can use the (op, ok) idiom without
	// importing a concrete type.
	var (
		p  Parent           = (Parent)(nil)
		c2 Children2        = (Children2)(nil)
		cp ColPrunable      = (ColPrunable)(nil)
		pc PredicateCarrier = (PredicateCarrier)(nil)
		rs RelationSource   = (RelationSource)(nil)
		ii IndexInfo        = (IndexInfo)(nil)
		ai AggregateInfo    = (AggregateInfo)(nil)
		si SortInfo         = (SortInfo)(nil)
		li LimitInfo        = (LimitInfo)(nil)
		cs ColumnSchema     = (ColumnSchema)(nil)
	)
	_ = p
	_ = c2
	_ = cp
	_ = pc
	_ = rs
	_ = ii
	_ = ai
	_ = si
	_ = li
	_ = cs
}

func TestAggregateSpec_ZeroValue(t *testing.T) {
	var s AggregateSpec
	if s.FuncName != "" || s.Arg != "" || s.Distinct || s.Alias != "" {
		t.Errorf("zero-value AggregateSpec not empty: %+v", s)
	}
}

func TestOrderSpec_ZeroValue(t *testing.T) {
	var s OrderSpec
	if s.Col != "" || s.Desc {
		t.Errorf("zero-value OrderSpec not empty: %+v", s)
	}
}
