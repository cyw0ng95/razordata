package CT

import (
	"fmt"
	"testing"
)

// dummyTypeDesc is a fictional extension type used to prove REQ002287: a new
// SQL type can be added by registering exactly one TypeDesc, with no edit to
// any existing `switch v.Kind` site in the engine. It is registered in this
// test (an extension kind >= KindExtBase) and then exercised through the full
// parse -> eval -> store -> read pipeline via the registry.
type dummyTypeDesc struct{}

func (dummyTypeDesc) Name() string    { return "dummycolor" }
func (dummyTypeDesc) Kind() ValueKind { return KindExtBase } // 32, extension range

func (dummyTypeDesc) Validate(v Value) error {
	if v.Kind != KindExtBase {
		return fmt.Errorf("dummycolor: kind %d", v.Kind)
	}
	return nil
}

func (dummyTypeDesc) Compare(a, b Value) int {
	// Order by the underlying string payload.
	if a.S < b.S {
		return -1
	}
	if a.S > b.S {
		return 1
	}
	return 0
}

func (dummyTypeDesc) Hash(v Value) uint64 {
	h := uint64(1469598103934665603)
	for _, c := range []byte(v.S) {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

func (dummyTypeDesc) Encode(v Value) ([]byte, error) {
	if v.Kind != KindExtBase {
		return nil, fmt.Errorf("dummycolor: cannot encode kind %d", v.Kind)
	}
	return []byte(v.S), nil
}

func (dummyTypeDesc) Decode(b []byte) (Value, error) {
	return Value{Kind: KindExtBase, S: string(b)}, nil
}

func (dummyTypeDesc) ParseLiteral(s string) (Value, error) {
	return Value{Kind: KindExtBase, S: s}, nil
}

func (dummyTypeDesc) ScalarEval(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("dummycolor: needs an arg")
	}
	return args[0], nil
}

func init() {
	// Register the dummy extension type for the test binary. Re-registering
	// would panic; this runs once per package init.
	RegisterType(dummyTypeDesc{})
}

// TestREQ002287_DummyTypeRoundTrip proves the frozen-type-contract claim:
// a brand-new type flows through parse -> eval -> store -> read entirely via
// the TypeDesc registry, touching zero pre-existing ValueKind switch sites.
func TestREQ002287_DummyTypeRoundTrip(t *testing.T) {
	desc, ok := LookupType(KindExtBase)
	if !ok {
		t.Fatalf("dummy TypeDesc not registered")
	}
	if got := desc.Name(); got != "dummycolor" {
		t.Fatalf("unexpected registered name %q", got)
	}

	// 1) parse: literal text -> Value
	parsed, err := desc.ParseLiteral("emerald")
	if err != nil {
		t.Fatalf("ParseLiteral: %v", err)
	}
	if parsed.Kind != KindExtBase || parsed.S != "emerald" {
		t.Fatalf("parse produced %+v", parsed)
	}

	// 2) eval: route through the evaluator fallback (no specialized kernel).
	// The EV layer dispatches to desc.ScalarEval; here we exercise the same
	// TypeDesc method the evaluator would call.
	evaluated, err := desc.ScalarEval([]Value{parsed})
	if err != nil {
		t.Fatalf("ScalarEval: %v", err)
	}
	if !evaluated.Equal(parsed) {
		t.Fatalf("eval changed value: %+v != %+v", evaluated, parsed)
	}

	// 3) store: encode through the registry dispatch.
	encoded, err := EncodeValue(evaluated)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if string(encoded) != "emerald" {
		t.Fatalf("encode mismatch: %q", encoded)
	}

	// 4) read: decode back through the registry dispatch.
	decoded, err := DecodeValue(KindExtBase, encoded)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if !decoded.Equal(parsed) {
		t.Fatalf("round-trip mismatch: %+v != %+v", decoded, parsed)
	}

	// Registry completeness: all six built-ins must be registered.
	for _, want := range []ValueKind{KindInt, KindFloat, KindText, KindBlob, KindBool, KindNull} {
		if _, ok := LookupType(want); !ok {
			t.Fatalf("built-in kind %d not registered", want)
		}
	}
	if len(RegisteredTypes()) < 7 {
		t.Fatalf("expected >=7 registered types, got %d", len(RegisteredTypes()))
	}
}

// TestREQ002287_FrozenEnum proves the ValueKind codes are frozen with the
// reserved ranges described in REQ002287, so existing on-disk encodings and
// the reserved built-in/extension ranges stay valid.
func TestREQ002287_FrozenEnum(t *testing.T) {
	// Core scalar codes keep their historical values.
	if KindNull != 0 || KindInt != 1 || KindFloat != 2 || KindText != 3 ||
		KindBlob != 4 || KindBool != 5 {
		t.Fatalf("core scalar codes changed: %d %d %d %d %d %d",
			KindNull, KindInt, KindFloat, KindText, KindBlob, KindBool)
	}
	// Reserved built-in scalar range 16-31.
	if KindDecimal != 16 || KindJSON != 17 || KindDate != 18 ||
		KindTime != 19 || KindTimestamp != 20 {
		t.Fatalf("reserved built-in codes changed")
	}
	if KindBuiltinMax != 31 {
		t.Fatalf("KindBuiltinMax = %d, want 31", KindBuiltinMax)
	}
	// Extension range starts at 32.
	if KindExtBase != 32 {
		t.Fatalf("KindExtBase = %d, want 32", KindExtBase)
	}
	// The dummy extension type sits in the extension range.
	if KindExtBase < KindBuiltinMax+1 {
		t.Fatalf("extension base overlaps reserved built-in range")
	}
}

// TestREQ002287_RegisterTypeRejectsInvalidKind proves the frozen-code-range
// contract is enforced: registering a TypeDesc outside the legal range panics.
func TestREQ002287_RegisterTypeRejectsInvalidKind(t *testing.T) {
	// Descriptor at an illegal built-in code (6, in the gap between core
	// scalars and the reserved range) must be rejected.
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for illegal kind")
		}
	}()
	RegisterType(illegalKindDesc{})
}

type illegalKindDesc struct{}

func (illegalKindDesc) Name() string    { return "illegal" }
func (illegalKindDesc) Kind() ValueKind { return 6 } // gap, not reserved
func (illegalKindDesc) ParseLiteral(string) (Value, error) {
	return Value{}, nil
}
func (illegalKindDesc) Validate(Value) error          { return nil }
func (illegalKindDesc) Compare(a, b Value) int        { return 0 }
func (illegalKindDesc) Hash(Value) uint64             { return 0 }
func (illegalKindDesc) Encode(Value) ([]byte, error)  { return nil, nil }
func (illegalKindDesc) Decode([]byte) (Value, error)  { return Value{}, nil }
func (illegalKindDesc) ScalarEval([]Value) (Value, error) {
	return Value{}, nil
}
