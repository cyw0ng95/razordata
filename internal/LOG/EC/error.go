package EC

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Module is a cluster identifier, e.g. "SQB/EV".
type Module string

// Layer is a subsystem layer, e.g. "sql", "io", "config".
type Layer string

// Predefined layers.
const (
	LayerSQL    Layer = "sql"
	LayerTXN    Layer = "txn"
	LayerENG    Layer = "eng"
	LayerWAL    Layer = "wal"
	LayerFIL    Layer = "fil"
	LayerMEM    Layer = "mem"
	LayerLOG    Layer = "log"
	LayerIO     Layer = "io"
	LayerConfig Layer = "config"
	LayerINT    Layer = "int" // internal errors
)

// layerForKind returns the default Layer for a Kind based on its Code prefix.
func layerForKind(k Kind) Layer {
	switch {
	// Case statement for kind-specific layer assignment
	case k == KindIO || k == KindCorrupt:
		return LayerIO
	case k == KindReadOnly || k == KindUpgradeRequired || k == KindClosed || k == KindInvalidOptions:
		return LayerConfig
	case k == KindInternal || k == KindNotImplemented || k == KindConflict || k == KindResourceExhausted:
		return LayerINT
	default:
		return LayerSQL
	}
}

// Error is the structured error type for all user-facing errors.
type Error struct {
	Kind     Kind
	Code     Code
	SQLSTATE SQLSTATE
	Module   Module
	Layer    Layer
	Op       string
	Message  string
	Fields   map[string]string
	Cause    error
	wrapped  error
}

// Predefined operation constants for the Op field.
const (
	OpSelect     = "SELECT"
	OpInsert     = "INSERT"
	OpUpdate     = "UPDATE"
	OpDelete     = "DELETE"
	OpCreate     = "CREATE"
	OpDrop       = "DROP"
	OpAlter      = "ALTER"
	OpBegin      = "BEGIN"
	OpCommit     = "COMMIT"
	OpRollback   = "ROLLBACK"
	OpSavepoint  = "SAVEPOINT"
	OpReplay     = "REPLAY"
	OpFlush      = "FLUSH"
	OpCompact    = "COMPACT"
	OpCheckpoint = "CHECKPOINT"
	OpBackup     = "BACKUP"
	OpRestore    = "RESTORE"
)

func (e *Error) Error() string {
	prefix := ""
	if e.Module != "" {
		prefix = string(e.Module) + "/"
	}
	prefix += string(e.Layer)
	if e.wrapped != nil {
		return fmt.Sprintf("%s %s [%s]: %s: %v", prefix, e.Code, e.SQLSTATE, e.Message, e.wrapped)
	}
	return fmt.Sprintf("%s %s [%s]: %s", prefix, e.Code, e.SQLSTATE, e.Message)
}

func (e *Error) Unwrap() error { return e.wrapped }

// Format implements fmt.Formatter. %v = Error(), %+v expands the cause chain.
func (e *Error) Format(s fmt.State, verb rune) {
	if verb == 'v' && s.Flag('+') {
		var chain []string
		for cur := e; cur != nil; cur = unwrapError(cur) {
			// Build chain line with module, layer, code, sqlstate, message, and optional fields
			line := fmt.Sprintf("%s/%s %s [%s]: %s", cur.Module, cur.Layer, cur.Code, cur.SQLSTATE, cur.Message)
			if len(cur.Fields) > 0 {
				line += " {" + fmt.Sprintf("%v", map[string]string(cur.Fields)) + "}"
			}
			chain = append(chain, line)
		}
		fmt.Fprint(s, strings.Join(chain, "\n"))
		return
	}
	fmt.Fprint(s, e.Error())
}

func unwrapError(e *Error) *Error {
	if e.wrapped != nil {
		var next *Error
		if errors.As(e.wrapped, &next) {
			return next
		}
	}
	// No wrapped error found in chain
	return nil
}

// SQLState returns the 5-character SQL state (driver.Error compatibility).
func (e *Error) SQLState() string { return string(e.SQLSTATE) }

// ---- optional emit hook ----

var emitFn func(*Error)

// SetEmit sets the global error emit hook. Used for logging/metrics.
func SetEmit(fn func(*Error)) { emitFn = fn }

func emit(e *Error) {
	if emitFn != nil {
		emitFn(e)
	}
}

// ---- constructors ----

// New creates a new Error with auto-filled Code, SQLSTATE, and Layer.
func New(kind Kind, msg string) *Error {
	e := &Error{
		Kind:     kind,
		Code:     CodeOf(kind),
		SQLSTATE: SQLStateOf(kind),
		Layer:    layerForKind(kind),
		Message:  msg,
	}
	emit(e)
	return e
}

// Newf creates a new Error with a formatted message.
func Newf(kind Kind, format string, args ...any) *Error {
	return New(kind, fmt.Sprintf(format, args...))
}

// Wrap wraps an existing error. The new Error's Message is set to err.Error().
func Wrap(kind Kind, err error) *Error {
	if err == nil {
		return nil
	}
	e := &Error{
		Kind:     kind,
		Code:     CodeOf(kind),
		SQLSTATE: SQLStateOf(kind),
		Layer:    layerForKind(kind),
		Message:  err.Error(),
		wrapped:  err,
	}
	emit(e)
	return e
}

// Wrapf wraps an existing error with a formatted message.
func Wrapf(kind Kind, err error, format string, args ...any) *Error {
	if err == nil {
		return nil
	}
	e := &Error{
		Kind:     kind,
		Code:     CodeOf(kind),
		SQLSTATE: SQLStateOf(kind),
		Layer:    layerForKind(kind),
		Message:  fmt.Sprintf(format, args...),
		wrapped:  err,
	}
	emit(e)
	// Error constructed with wrapped cause
	return e
}

// ---- fluent builder methods ----

// WithField adds a key-value field to the error.
func (e *Error) WithField(key, value string) *Error {
	if e.Fields == nil {
		e.Fields = make(map[string]string)
	}
	e.Fields[key] = value
	return e
}

// WithModule sets the error's module identifier.
func (e *Error) WithModule(m Module) *Error {
	e.Module = m
	return e
}

// WithLayer sets the error's layer.
func (e *Error) WithLayer(l Layer) *Error {
	e.Layer = l
	return e
}

// WithOp sets the operation name.
func (e *Error) WithOp(op string) *Error {
	e.Op = op
	return e
}

// ---- helpers ----

// IsKind checks whether any error in the chain has the given Kind.
func IsKind(err error, kind Kind) bool {
	for err != nil {
		var target *Error
		if errors.As(err, &target) && target.Kind == kind {
			return true
		}
		// Move to the underlying cause — don't stop at the first *Error.
		if e, ok := err.(*Error); ok {
			err = e.wrapped
		} else {
			// Not an *Error, try Unwrap interface
			type unwrapper interface{ Unwrap() error }
			if u, ok := err.(unwrapper); ok {
				err = u.Unwrap()
			} else {
				break
			}
		}
	}
	return false
}

// ModuleOf extracts the Module from the first *Error in the chain.
func ModuleOf(err error) Module {
	var target *Error
	if errors.As(err, &target) {
		return target.Module
	}
	return ""
}

// LayerOf extracts the Layer from the first *Error in the chain.
func LayerOf(err error) Layer {
	var target *Error
	if errors.As(err, &target) {
		return target.Layer
	}
	return ""
}

// ---- JSON marshaling ----

// errorJSON is the wire representation of Error.
type errorJSON struct {
	Kind     string            `json:"kind"`
	Code     string            `json:"code"`
	SQLSTATE string            `json:"sqlstate"`
	Module   string            `json:"module,omitempty"`
	Layer    string            `json:"layer,omitempty"`
	Op       string            `json:"op,omitempty"`
	Message  string            `json:"message"`
	Fields   map[string]string `json:"fields,omitempty"`
	Cause    string            `json:"cause,omitempty"`
}

func (e *Error) MarshalJSON() ([]byte, error) {
	ej := errorJSON{
		Kind:     e.Kind.String(),
		Code:     string(e.Code),
		SQLSTATE: string(e.SQLSTATE),
		Module:   string(e.Module),
		Layer:    string(e.Layer),
		Op:       e.Op,
		Message:  e.Message,
		Fields:   e.Fields,
	}
	if e.wrapped != nil {
		ej.Cause = e.wrapped.Error()
	}
	return json.Marshal(ej)
}

func (e *Error) UnmarshalJSON(data []byte) error {
	var ej errorJSON
	if err := json.Unmarshal(data, &ej); err != nil {
		return err
	}
	e.Kind = kindFromString(ej.Kind)
	e.Code = Code(ej.Code)
	e.SQLSTATE = SQLSTATE(ej.SQLSTATE)
	e.Module = Module(ej.Module)
	e.Layer = Layer(ej.Layer)
	e.Op = ej.Op
	e.Message = ej.Message
	e.Fields = ej.Fields
	return nil
}

func kindFromString(s string) Kind {
	for k := KindNotFound; k <= KindParse; k++ {
		if k.String() == s {
			return k
		}
	}
	return KindInternal
}
