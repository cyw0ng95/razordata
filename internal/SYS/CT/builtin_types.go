package CT

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// builtinTypeDesc is a self-contained TypeDesc implementation for the six
// built-in scalar kinds. REQ002287: every built-in type registers a TypeDesc
// so the registry is the single source of type metadata; the engine's hot
// paths (e.g. DT row encode/decode, DT.CompareValue) keep their specialized
// inline switches for performance and SLT stability, but the metadata and the
// extension-aware dispatch path both flow through the registry.

func hashBytes(b []byte) uint64 {
	// FNV-1a 64-bit, allocation-free.
	var h uint64 = 1469598103934665603
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// ---------------------------------------------------------------------------
// int
// ---------------------------------------------------------------------------

type intTypeDesc struct{}

func (intTypeDesc) Name() string         { return "int" }
func (intTypeDesc) Kind() ValueKind      { return KindInt }
func (intTypeDesc) Validate(v Value) error {
	if v.Kind != KindInt {
		return fmt.Errorf("int: value has kind %d", v.Kind)
	}
	return nil
}
func (intTypeDesc) Compare(a, b Value) int {
	switch {
	case a.I64 < b.I64:
		return -1
	case a.I64 > b.I64:
		return 1
	default:
		return 0
	}
}
func (intTypeDesc) Hash(v Value) uint64 { return uint64(v.I64) }
func (intTypeDesc) Encode(v Value) ([]byte, error) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v.I64))
	return b[:], nil
}
func (intTypeDesc) Decode(b []byte) (Value, error) {
	if len(b) < 8 {
		return Value{}, fmt.Errorf("int: truncated payload")
	}
	return NewIntValue(int64(binary.BigEndian.Uint64(b[:8]))), nil
}
func (intTypeDesc) ParseLiteral(s string) (Value, error) {
	var i int64
	if _, err := fmt.Sscanf(s, "%d", &i); err != nil {
		return Value{}, fmt.Errorf("int: invalid literal %q", s)
	}
	return NewIntValue(i), nil
}
func (intTypeDesc) ScalarEval(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("int: ScalarEval requires an argument")
	}
	return args[0], nil
}

// ---------------------------------------------------------------------------
// float
// ---------------------------------------------------------------------------

type floatTypeDesc struct{}

func (floatTypeDesc) Name() string    { return "float" }
func (floatTypeDesc) Kind() ValueKind { return KindFloat }
func (floatTypeDesc) Validate(v Value) error {
	if v.Kind != KindFloat {
		return fmt.Errorf("float: value has kind %d", v.Kind)
	}
	return nil
}
func (floatTypeDesc) Compare(a, b Value) int {
	switch {
	case a.F64 < b.F64:
		return -1
	case a.F64 > b.F64:
		return 1
	default:
		return 0
	}
}
func (floatTypeDesc) Hash(v Value) uint64 { return hashBytes([]byte{byte(v.F64)}) }
func (floatTypeDesc) Encode(v Value) ([]byte, error) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(v.F64))
	return b[:], nil
}
func (floatTypeDesc) Decode(b []byte) (Value, error) {
	if len(b) < 8 {
		return Value{}, fmt.Errorf("float: truncated payload")
	}
	return NewFloatValue(math.Float64frombits(binary.BigEndian.Uint64(b[:8]))), nil
}
func (floatTypeDesc) ParseLiteral(s string) (Value, error) {
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return Value{}, fmt.Errorf("float: invalid literal %q", s)
	}
	return NewFloatValue(f), nil
}
func (floatTypeDesc) ScalarEval(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("float: ScalarEval requires an argument")
	}
	return args[0], nil
}

// ---------------------------------------------------------------------------
// text
// ---------------------------------------------------------------------------

type textTypeDesc struct{}

func (textTypeDesc) Name() string    { return "text" }
func (textTypeDesc) Kind() ValueKind { return KindText }
func (textTypeDesc) Validate(v Value) error {
	if v.Kind != KindText {
		return fmt.Errorf("text: value has kind %d", v.Kind)
	}
	return nil
}
func (textTypeDesc) Compare(a, b Value) int { return strings.Compare(a.S, b.S) }
func (textTypeDesc) Hash(v Value) uint64    { return hashBytes([]byte(v.S)) }
func (textTypeDesc) Encode(v Value) ([]byte, error) {
	out := make([]byte, 0, len(v.S)+binary.MaxVarintLen64)
	out = binary.AppendUvarint(out, uint64(len(v.S)))
	out = append(out, v.S...)
	return out, nil
}
func (textTypeDesc) Decode(b []byte) (Value, error) {
	l, n := binary.Uvarint(b)
	if n <= 0 {
		return Value{}, fmt.Errorf("text: bad length")
	}
	if uint64(len(b)-n) < l {
		return Value{}, fmt.Errorf("text: truncated payload")
	}
	return NewTextValue(string(b[n : n+int(l)])), nil
}
func (textTypeDesc) ParseLiteral(s string) (Value, error) { return NewTextValue(s), nil }
func (textTypeDesc) ScalarEval(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("text: ScalarEval requires an argument")
	}
	return args[0], nil
}

// ---------------------------------------------------------------------------
// blob
// ---------------------------------------------------------------------------

type blobTypeDesc struct{}

func (blobTypeDesc) Name() string    { return "blob" }
func (blobTypeDesc) Kind() ValueKind { return KindBlob }
func (blobTypeDesc) Validate(v Value) error {
	if v.Kind != KindBlob {
		return fmt.Errorf("blob: value has kind %d", v.Kind)
	}
	return nil
}
func (blobTypeDesc) Compare(a, b Value) int {
	// Byte-wise comparison; nil slices compare equal when both empty.
	return bytes.Compare(a.B, b.B)
}
func (blobTypeDesc) Hash(v Value) uint64    { return hashBytes(v.B) }
func (blobTypeDesc) Encode(v Value) ([]byte, error) {
	out := make([]byte, 0, len(v.B)+binary.MaxVarintLen64)
	out = binary.AppendUvarint(out, uint64(len(v.B)))
	out = append(out, v.B...)
	return out, nil
}
func (blobTypeDesc) Decode(b []byte) (Value, error) {
	l, n := binary.Uvarint(b)
	if n <= 0 {
		return Value{}, fmt.Errorf("blob: bad length")
	}
	if uint64(len(b)-n) < l {
		return Value{}, fmt.Errorf("blob: truncated payload")
	}
	cp := make([]byte, l)
	copy(cp, b[n:n+int(l)])
	return NewBlobValue(cp), nil
}
func (blobTypeDesc) ParseLiteral(s string) (Value, error) {
	return NewBlobValue([]byte(s)), nil
}
func (blobTypeDesc) ScalarEval(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("blob: ScalarEval requires an argument")
	}
	return args[0], nil
}

// ---------------------------------------------------------------------------
// bool
// ---------------------------------------------------------------------------

type boolTypeDesc struct{}

func (boolTypeDesc) Name() string    { return "bool" }
func (boolTypeDesc) Kind() ValueKind { return KindBool }
func (boolTypeDesc) Validate(v Value) error {
	if v.Kind != KindBool {
		return fmt.Errorf("bool: value has kind %d", v.Kind)
	}
	return nil
}
func (boolTypeDesc) Compare(a, b Value) int {
	if !a.Bo && b.Bo {
		return -1
	}
	if a.Bo && !b.Bo {
		return 1
	}
	return 0
}
func (boolTypeDesc) Hash(v Value) uint64 {
	if v.Bo {
		return 1
	}
	return 0
}
func (boolTypeDesc) Encode(v Value) ([]byte, error) {
	if v.Bo {
		return []byte{1}, nil
	}
	return []byte{0}, nil
}
func (boolTypeDesc) Decode(b []byte) (Value, error) {
	if len(b) < 1 {
		return Value{}, fmt.Errorf("bool: truncated payload")
	}
	return NewBoolValue(b[0] != 0), nil
}
func (boolTypeDesc) ParseLiteral(s string) (Value, error) {
	switch s {
	case "true", "TRUE", "1":
		return NewBoolValue(true), nil
	case "false", "FALSE", "0":
		return NewBoolValue(false), nil
	default:
		return Value{}, fmt.Errorf("bool: invalid literal %q", s)
	}
}
func (boolTypeDesc) ScalarEval(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("bool: ScalarEval requires an argument")
	}
	return args[0], nil
}

// ---------------------------------------------------------------------------
// null (sentinel type, primarily for registry completeness)
// ---------------------------------------------------------------------------

type nullTypeDesc struct{}

func (nullTypeDesc) Name() string    { return "null" }
func (nullTypeDesc) Kind() ValueKind { return KindNull }
func (nullTypeDesc) Validate(v Value) error {
	if v.Kind != KindNull {
		return fmt.Errorf("null: value has kind %d", v.Kind)
	}
	return nil
}
func (nullTypeDesc) Compare(a, b Value) int { return 0 }
func (nullTypeDesc) Hash(v Value) uint64    { return 0 }
func (nullTypeDesc) Encode(v Value) ([]byte, error) {
	return nil, nil
}
func (nullTypeDesc) Decode(b []byte) (Value, error) { return NullValue(), nil }
func (nullTypeDesc) ParseLiteral(s string) (Value, error) {
	if s == "" || s == "NULL" || s == "null" {
		return NullValue(), nil
	}
	return Value{}, fmt.Errorf("null: invalid literal %q", s)
}
func (nullTypeDesc) ScalarEval(args []Value) (Value, error) { return NullValue(), nil }

func init() {
	RegisterType(intTypeDesc{})
	RegisterType(floatTypeDesc{})
	RegisterType(textTypeDesc{})
	RegisterType(blobTypeDesc{})
	RegisterType(boolTypeDesc{})
	RegisterType(nullTypeDesc{})
}
