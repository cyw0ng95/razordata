package OP

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"

	"encoding/binary"
	"math"
)

func TestCompileRawByteFilter_IntEQ(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColTypes: []LX.TokenType{LX.T_INT, LX.T_FLOAT, LX.T_TEXT},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a", SlotIdx: 0},
		Right: &PS.NumberLiteral{Val: 42},
	}

	if !CanEvaluateOnRaw(pred, schema) {
		t.Fatal("INT EQ should be pushdown-able")
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter == nil {
		t.Fatal("filter should not be nil")
	}

	// Encode row: a=42, b=1.5, c="hello"
	rowBytes := encodeIntRow(42, float64(1.5), "hello")
	if !filter(rowBytes) {
		t.Fatal("row with a=42 should match a=42")
	}

	rowBytes = encodeIntRow(10, float64(1.5), "hello")
	if filter(rowBytes) {
		t.Fatal("row with a=10 should not match a=42")
	}
}

func TestCompileRawByteFilter_FloatGT(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a", "b"},
		ColTypes: []LX.TokenType{LX.T_FLOAT, LX.T_INT},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_GT,
		Left:  &PS.Ident{Name: "a", SlotIdx: 0},
		Right: &PS.FloatLiteral{Val: 3.14},
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter == nil {
		t.Fatal("FLOAT GT should be pushdown-able")
	}

	rowBytes := encodeFloatRow(5.0, 10)
	if !filter(rowBytes) {
		t.Fatal("row with a=5.0 should match a>3.14")
	}

	rowBytes = encodeFloatRow(2.0, 10)
	if filter(rowBytes) {
		t.Fatal("row with a=2.0 should not match a>3.14")
	}
}

func TestCompileRawByteFilter_StringEQ(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a", "b"},
		ColTypes: []LX.TokenType{LX.T_TEXT, LX.T_INT},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a", SlotIdx: 0},
		Right: &PS.StringLiteral{Val: "hello"},
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter == nil {
		t.Fatal("TEXT EQ should be pushdown-able")
	}

	rowBytes := encodeStrRow("hello", 42)
	if !filter(rowBytes) {
		t.Fatal("row with a='hello' should match")
	}

	rowBytes = encodeStrRow("world", 42)
	if filter(rowBytes) {
		t.Fatal("row with a='world' should not match")
	}
}

func TestCompileRawByteFilter_Boolean(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a", "b"},
		ColTypes: []LX.TokenType{LX.T_BOOL, LX.T_INT},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a", SlotIdx: 0},
		Right: &PS.BoolLiteral{Val: true},
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter == nil {
		t.Fatal("BOOL EQ should be pushdown-able")
	}

	rowBytes := encodeBoolRow(true, 42)
	if !filter(rowBytes) {
		t.Fatal("row with a=true should match a=true")
	}

	rowBytes = encodeBoolRow(false, 42)
	if filter(rowBytes) {
		t.Fatal("row with a=false should not match a=true")
	}
}

func TestCompileRawByteFilter_ReversedSides(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a"},
		ColTypes: []LX.TokenType{LX.T_INT},
	}
	// literal on left, column on right
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.NumberLiteral{Val: 42},
		Right: &PS.Ident{Name: "a", SlotIdx: 0},
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter == nil {
		t.Fatal("reversed sides should be pushdown-able")
	}

	rowBytes := encodeIntRow(42, 0, "")
	if !filter(rowBytes) {
		t.Fatal("row with a=42 should match 42=a")
	}
}

func TestCompileRawByteFilter_NonComparable(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a"},
		ColTypes: []LX.TokenType{LX.T_BLOB},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a", SlotIdx: 0},
		Right: &PS.NumberLiteral{Val: 1},
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter != nil {
		t.Fatal("BLOB should not be pushdown-able")
	}
}

func TestCompileRawByteFilter_NullColumn(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a"},
		ColTypes: []LX.TokenType{LX.T_INT},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a", SlotIdx: 0},
		Right: &PS.NumberLiteral{Val: 42},
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter == nil {
		t.Fatal("filter should not be nil")
	}

	rowBytes := encodeIntNull()
	if filter(rowBytes) {
		t.Fatal("NULL should not match any comparison")
	}
}

func TestCompileRawByteFilter_IncorrectSlotIdx(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"a", "b"},
		ColTypes: []LX.TokenType{LX.T_INT, LX.T_FLOAT},
	}
	pred := &PS.BinaryExpr{
		Op:    LX.T_EQ,
		Left:  &PS.Ident{Name: "a", SlotIdx: 5}, // invalid index
		Right: &PS.NumberLiteral{Val: 42},
	}

	filter := CompileRawByteFilter(pred, schema)
	if filter != nil {
		t.Fatal("invalid SlotIdx should not be pushdown-able")
	}
}

// Helper: encode an INT row [a, b=float, c=str] for test.
func encodeIntRow(a int64, b float64, c string) []byte {
	var buf []byte
	buf = append(buf, 0x03) // 3 columns
	buf = append(buf, 0x01) // rvInt
	buf = append(buf, bigEndianUint64(uint64(a))...)
	buf = append(buf, 0x04) // rvFloat
	buf = append(buf, bigEndianUint64(math.Float64bits(b))...)
	buf = append(buf, 0x02) // rvString
	buf = binary.AppendUvarint(buf, uint64(len(c)))
	buf = append(buf, []byte(c)...)
	return buf
}

func encodeFloatRow(a float64, b int64) []byte {
	var buf []byte
	buf = append(buf, 0x02) // 2 columns
	buf = append(buf, 0x04) // rvFloat
	buf = append(buf, bigEndianUint64(math.Float64bits(a))...)
	buf = append(buf, 0x01) // rvInt
	buf = append(buf, bigEndianUint64(uint64(b))...)
	return buf
}

func encodeStrRow(a string, b int64) []byte {
	var buf []byte
	buf = append(buf, 0x02) // 2 columns
	buf = append(buf, 0x02) // rvString
	buf = binary.AppendUvarint(buf, uint64(len(a)))
	buf = append(buf, []byte(a)...)
	buf = append(buf, 0x01) // rvInt
	buf = append(buf, bigEndianUint64(uint64(b))...)
	return buf
}

func encodeBoolRow(a bool, b int64) []byte {
	var buf []byte
	buf = append(buf, 0x02) // 2 columns
	buf = append(buf, 0x03) // rvBool
	buf = append(buf, byte(1))
	if !a {
		buf[len(buf)-1] = 0
	}
	buf = append(buf, 0x01) // rvInt
	buf = append(buf, bigEndianUint64(uint64(b))...)
	return buf
}

func encodeIntNull() []byte {
	return []byte{0x01, 0x00} // 1 col, rvNull
}

func bigEndianUint64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func bigEndianUvarint(v uint64) []byte {
	return make([]byte, binary.PutUvarint(make([]byte, 10), v))
}
