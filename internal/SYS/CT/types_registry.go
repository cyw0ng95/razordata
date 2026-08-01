// Package CT (Contract Types) — type-model registry. REQ002287.
//
// The type model is frozen: ValueKind codes are stable and new SQL types are
// added as additive extensions through the TypeDesc registry, never as a
// cross-cutting refactor of the ~52 ValueKind switch sites across the engine.
//
// A TypeDesc describes everything the engine needs to know about a value type
// so it can be parsed, validated, compared, hashed, stored, and evaluated
// without the storage (ENG/LS, SQB/DT), planner (SQF/QP), or evaluator
// (SQB/EV) switch statements having to know about the type:
//
//   - parse literals into Values
//   - validate a Value is well-formed for the type
//   - compare two Values of the type (for ORDER BY / predicates)
//   - hash a Value (for hash joins / grouping)
//   - encode / decode a Value to / from storage bytes
//   - a scalar-eval fallback used by the evaluator when no specialized kernel
//     exists for the type
//
// Built-in types register a TypeDesc at package init (see builtin_types.go).
// Extension types register their own TypeDesc; adding one requires editing
// zero existing switch sites because the extension-aware paths (e.g. DT row
// encode/decode) dispatch through LookupType.
package CT

import (
	"fmt"
	"sort"
	"sync"
)

// TypeDesc is the contract every SQL value type implements. REQ002287.
//
// Implementations must be safe for concurrent use (the engine reads the
// registry from multiple goroutines). Encode/Decode must round-trip:
// Decode(Encode(v)) must equal v for non-null v.
type TypeDesc interface {
	// Name returns the canonical, lowercase type name (e.g. "int", "decimal").
	// Must be unique across all registered types.
	Name() string

	// Kind returns the ValueKind code this descriptor owns. Must be within
	// the reserved/extension ranges appropriate to the type:
	//   - built-in scalars use 1..KindBuiltinMax's declared codes
	//   - extension types use codes >= KindExtBase
	Kind() ValueKind

	// ParseLiteral converts a SQL literal string (already stripped of
	// quoting) into a Value of this type. Returns an error if the text is
	// not a valid literal for the type.
	ParseLiteral(s string) (Value, error)

	// Validate reports whether v is a well-formed Value for this type.
	// A null Value always validates.
	Validate(v Value) error

	// Compare returns -1/0/1 for a<b / a==b / a>b. NULLs are handled by the
	// caller's collation policy; Compare is only invoked for non-null values
	// of this type.
	Compare(a, b Value) int

	// Hash returns a stable 64-bit hash of v for non-null values.
	Hash(v Value) uint64

	// Encode serializes a non-null Value to storage bytes.
	Encode(v Value) ([]byte, error)

	// Decode reconstructs a Value from bytes produced by Encode.
	Decode(b []byte) (Value, error)

	// ScalarEval is the evaluator fallback for expressions over this type.
	// args are the already-evaluated operand Values. The default identity
	// implementation returns args[0] unchanged; types with real evaluation
	// semantics override it. It must not be called with nil args unless the
	// type explicitly supports zero-argument evaluation.
	ScalarEval(args []Value) (Value, error)
}

// typeRegistry is the process-wide registry of TypeDescs, keyed both by
// ValueKind code and by canonical name.
var (
	typeRegistryMu sync.RWMutex
	typeRegistry   = struct {
		byKind map[ValueKind]TypeDesc
		byName map[string]TypeDesc
	}{
		byKind: make(map[ValueKind]TypeDesc),
		byName: make(map[string]TypeDesc),
	}
)

// RegisterType registers a TypeDesc. It must be called for every built-in and
// extension type exactly once, typically from an init function. It panics on:
//   - a nil descriptor
//   - a kind code outside the legal range for the registering type
//   - a duplicate kind or name (a type is being redefined)
//
// Registering from init (as the built-ins do) guarantees the registry is
// populated before any query runs.
func RegisterType(d TypeDesc) {
	if d == nil {
		panic("CT: RegisterType called with nil TypeDesc")
	}
	k := d.Kind()
	name := d.Name()
	if name == "" {
		panic("CT: RegisterType: TypeDesc.Name must not be empty")
	}
	// REQ002287: enforce the frozen code-range contract.
	if k == KindNull {
		// The null type is a legitimate built-in type (sentinel); its code
		// is 0.
	} else if k < KindExtBase {
		// Built-in scalar range (0..KindBuiltinMax). Only the explicitly
		// declared core codes (1..5) and the reserved built-in codes
		// (16..KindBuiltinMax) are legal; the gap 6..15 is frozen unused.
		core := k >= 1 && k <= 5
		reserved := k >= 16 && k <= KindBuiltinMax
		if !core && !reserved {
			panic(fmt.Sprintf("CT: TypeDesc %q kind %d is not a declared core or reserved built-in code", name, k))
		}
	}

	typeRegistryMu.Lock()
	defer typeRegistryMu.Unlock()
	if _, dup := typeRegistry.byKind[k]; dup {
		panic(fmt.Sprintf("CT: TypeDesc kind %d already registered", k))
	}
	if _, dup := typeRegistry.byName[name]; dup {
		panic(fmt.Sprintf("CT: TypeDesc name %q already registered", name))
	}
	typeRegistry.byKind[k] = d
	typeRegistry.byName[name] = d
}

// LookupType returns the TypeDesc registered for a ValueKind code.
func LookupType(k ValueKind) (TypeDesc, bool) {
	typeRegistryMu.RLock()
	d, ok := typeRegistry.byKind[k]
	typeRegistryMu.RUnlock()
	return d, ok
}

// LookupTypeByName returns the TypeDesc registered under a canonical name.
func LookupTypeByName(name string) (TypeDesc, bool) {
	typeRegistryMu.RLock()
	d, ok := typeRegistry.byName[name]
	typeRegistryMu.RUnlock()
	return d, ok
}

// RegisteredTypes returns a sorted snapshot of all registered TypeDescs
// (ordered by Kind) for diagnostics and introspection.
func RegisteredTypes() []TypeDesc {
	typeRegistryMu.RLock()
	defer typeRegistryMu.RUnlock()
	out := make([]TypeDesc, 0, len(typeRegistry.byKind))
	for _, d := range typeRegistry.byKind {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}

// EncodeValue dispatches value encoding to the registry. It is used by the
// storage layer for extension kinds that are not handled by the fast inline
// built-in path. Returns an error if no TypeDesc is registered for v.Kind.
func EncodeValue(v Value) ([]byte, error) {
	d, ok := LookupType(v.Kind)
	if !ok {
		return nil, fmt.Errorf("CT: no TypeDesc registered for kind %d", v.Kind)
	}
	return d.Encode(v)
}

// DecodeValue dispatches value decoding to the registry using the given kind.
func DecodeValue(k ValueKind, b []byte) (Value, error) {
	d, ok := LookupType(k)
	if !ok {
		return Value{}, fmt.Errorf("CT: no TypeDesc registered for kind %d", k)
	}
	return d.Decode(b)
}

// CompareValues dispatches comparison to the registry. Returns 0 if either
// value is null (callers apply NULL collation policy before calling).
func CompareValues(a, b Value) int {
	if a.Kind != b.Kind {
		// Cross-type comparison is not defined by the registry; fall back to
		// the legacy kind ordering so callers get a deterministic result.
		if a.Kind < b.Kind {
			return -1
		}
		if a.Kind > b.Kind {
			return 1
		}
		return 0
	}
	d, ok := LookupType(a.Kind)
	if !ok {
		return 0
	}
	return d.Compare(a, b)
}
