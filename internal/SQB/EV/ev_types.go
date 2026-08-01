package EV

import (
	"fmt"

	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

type Value = CT.Value
type ValueKind = CT.ValueKind
type Row = pl.Row
type Operator = DT.Operator
type ExecContext = pl.ExecContext
type ColInfo = pl.ColInfo

const (
	KindNull  = CT.KindNull
	KindInt   = CT.KindInt
	KindFloat = CT.KindFloat
	KindText  = CT.KindText
	KindBlob  = CT.KindBlob
	KindBool  = CT.KindBool
)

var ErrNoRows = pl.ErrNoRows

// TypeDesc is the type-model contract a SQL value type implements. REQ002287:
// it is re-exported from SYS/CT so EV (and its callers) consult the registry
// as the single dispatch layer for type metadata and extension-aware storage,
// rather than switching on ValueKind directly.
type TypeDesc = CT.TypeDesc

// LookupType returns the TypeDesc registered for a ValueKind code, routing
// through the SYS/CT registry (the single dispatch layer). REQ002287.
func LookupType(kind ValueKind) (TypeDesc, bool) {
	return CT.LookupType(kind)
}

// LookupTypeByName returns the TypeDesc registered under a canonical name.
func LookupTypeByName(name string) (TypeDesc, bool) {
	return CT.LookupTypeByName(name)
}

// KindName returns the canonical type name for a ValueKind, resolved through
// the registry so extension types print correctly without editing this path.
func KindName(kind ValueKind) string {
	return CT.ValueKind(kind).String()
}

// RegisteredTypes returns a sorted snapshot of all registered TypeDescs.
func RegisteredTypes() []TypeDesc {
	return CT.RegisteredTypes()
}

// ScalarEvalFallback evaluates an expression over operands of a
// registry-registered type using that type's ScalarEval fallback. REQ002287:
// the evaluator routes types it has no specialized kernel for through here,
// so adding a type requires registering exactly one TypeDesc — no edit to the
// evaluator's kind switches.
func ScalarEvalFallback(kind ValueKind, args []Value) (Value, error) {
	d, ok := CT.LookupType(kind)
	if !ok {
		return Value{}, fmt.Errorf("EV: no TypeDesc registered for kind %d", kind)
	}
	return d.ScalarEval(args)
}
