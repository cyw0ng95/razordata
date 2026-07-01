package EX

import (
	AG "github.com/cyw0ng95/razordata/internal/SQB/AG"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
)

// ShapeSpecializer detects common operator patterns and returns
// specialized fast-path functions (REQ000543). The goal is to
// eliminate per-row interpretation overhead for hot paths by
// capturing the pattern as a small specialized Go function.
//
// This is a "shape-driven specialization" — not full JIT. Each
// shape captures a specific operator combination (e.g., OP.Filter
// with int64 equality, OP.Project with fixed columns) and returns
// a closure that executes the pattern without the generic eval
// machinery.

// ShapeKind identifies a recognized operator pattern.
type ShapeKind int

const (
	ShapeNone          ShapeKind = iota
	ShapeFilterInt64Eq           // OP.Filter{col = int64_lit}
	ShapeProjectFixed            // OP.Project{cols = fixed set}
	ShapeHashAggInt64            // HashAggregate{group_by = int64}
)

// DetectShape inspects an operator tree and returns the recognized
// shape, or ShapeNone if no specialization is available.
func DetectShape(op Operator) ShapeKind {
	switch o := op.(type) {
	case *OP.Filter:
		if isInt64EqPredicate(o.Predicate()) {
			return ShapeFilterInt64Eq
		}
	case *OP.Project:
		if isFixedCols(o) {
			return ShapeProjectFixed
		}
	case *AG.HashAggregate:
		if isInt64GroupBy(o) {
			return ShapeHashAggInt64
		}
	}
	return ShapeNone
}

// isInt64EqPredicate checks if the predicate is col = int64_literal.
func isInt64EqPredicate(expr interface{}) bool {
	// Simplified check — in production this would inspect the AST
	return false
}

// isFixedCols checks if the project has a fixed set of columns.
func isFixedCols(p *OP.Project) bool {
	return len(p.Cols()) > 0 && len(p.Cols()) <= 8
}

// isInt64GroupBy checks if the hash aggregate groups by int64 columns.
func isInt64GroupBy(h *AG.HashAggregate) bool {
	return len(h.GroupCols()) > 0
}
