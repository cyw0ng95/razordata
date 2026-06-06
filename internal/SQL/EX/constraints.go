package EX

import (
	"fmt"

	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// ErrConstraint is the package alias for AP.ErrConstraint. Constraint
// violations (NOT NULL, DEFAULT evaluation) wrap this sentinel.
var ErrConstraint = ap.ErrConstraint

// fillDefaults replaces nil entries in row.Data with the evaluated
// DEFAULT expression for that column. Columns without a DEFAULT keep
// their nil. The row is returned with the same Data slice length.
// Returns a wrapped ErrConstraint on DEFAULT evaluation failure.
func fillDefaults(schema *storeSchema, row Row) (Row, error) {
	if schema.defaults == nil {
		return row, nil
	}
	for i, def := range schema.defaults {
		if def == nil {
			continue
		}
		if row.Data[i] == nil {
			v, err := Eval(def, nil, nil)
			if err != nil {
				return row, fmt.Errorf("%w: default for column %q: %v",
					ErrConstraint, schema.cols[i], err)
			}
			row.Data[i] = v
		}
	}
	return row, nil
}

// validateRow checks that every non-nullable column has a non-nil value
// in row.Data. Columns with a DEFAULT are allowed to be nil at this
// stage (fillDefaults runs first). Returns a wrapped ErrConstraint on
// violation.
func validateRow(schema *storeSchema, row Row) error {
	for i, col := range schema.cols {
		if row.Data[i] == nil && !schema.nullable[i] {
			// Note: PK implies NOT NULL; primary-key columns always have
			// schema.nullable[i] == false from CREATE TABLE parsing.
			return fmt.Errorf("%w: column %q is NOT NULL", ErrConstraint, col)
		}
	}
	return nil
}
