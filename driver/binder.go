package driver

import (
	"database/sql/driver"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// ParamBinder is the single point that converts database/sql driver
// arguments into the engine's []any parameter slice. REQ002291: the
// driver previously had three divergent conversions — ExecContext did a
// raw `anyArgs[i] = a.Value`, while Stmt.Exec/Query duplicated the
// toDriverValue loop, and the deprecated Conn.Exec double-wrapped values
// into driver.NamedValue before calling ExecContext. All three paths now
// route through ParamBinder so positional parameter binding is
// consistent across the prepared-statement and ad-hoc execution paths and
// across the session Exec / Stmt Exec / Stmt Query call sites.
//
// The engine only supports positional parameters (`?`); named parameters
// are flattened by ordinal (1-based), preserving order.
type ParamBinder struct{}

// Bind converts a slice of driver.NamedValue into the engine's []any
// parameter slice. Each value passes through toDriverValue so AP.Value
// and raw driver values share one coercion rule.
func (ParamBinder) Bind(args []driver.NamedValue) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = toDriverValue(a.Value)
	}
	return out
}

// BindValues converts the deprecated []driver.Value slice (no names,
// positional only) into the engine's []any parameter slice.
func (ParamBinder) BindValues(args []driver.Value) []any {
	out := make([]any, len(args))
	for i, v := range args {
		out[i] = toDriverValue(v)
	}
	return out
}

// ToNamedValues wraps a positional []driver.Value slice into
// []driver.NamedValue with 1-based ordinals.
func ToNamedValues(args []driver.Value) []driver.NamedValue {
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return named
}

// BindPrepared converts a positional []driver.Value slice into the
// engine's []any parameter slice and validates the placeholder count
// against NumInput (the statement's parameter count). REQ002291: this is
// the shared validation formerly only enforced deep in the engine
// (ST.validateArgTypes); the driver now fails fast with a clear error
// instead of forwarding a mismatched arg count to the executor. A count
// of zero on both sides is allowed (no parameters).
func (ParamBinder) BindPrepared(args []driver.Value, numInput int) ([]any, error) {
	if len(args) == 0 && numInput <= 0 {
		return nil, nil
	}
	if numInput >= 0 && len(args) != numInput {
		return nil, AP.New(AP.KindTypeMismatch, fmt.Sprintf("driver: %d args given, want %d", len(args), numInput))
	}
	return ParamBinder{}.BindValues(args), nil
}
