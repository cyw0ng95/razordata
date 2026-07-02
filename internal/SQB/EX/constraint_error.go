package EX

import (
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// ConstraintError captures a constraint violation with context.
type ConstraintError struct {
	Table      string
	Constraint string
	Columns    []string
	Values     []string
	Op         string
	Message    string
	Cause      error
}

func (e *ConstraintError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Constraint != "" {
		return fmt.Sprintf("constraint %q violated on table %s", e.Constraint, e.Table)
	}
	return fmt.Sprintf("constraint violated on table %s", e.Table)
}

func (e *ConstraintError) Unwrap() error { return e.Cause }

// ToAPError converts to a structured AP.Error.
func (e *ConstraintError) ToAPError() *AP.Error {
	ae := AP.Wrap(AP.KindConstraint, e.Cause)
	ae.Module = "SQB/EX"
	ae.Layer = AP.LayerSQL
	ae.Op = e.Op
	ae.Fields = map[string]string{
		"table":      e.Table,
		"constraint": e.Constraint,
		"op":         e.Op,
	}
	return ae
}
