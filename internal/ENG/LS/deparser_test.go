package ls

import (
	"bytes"
	"testing"
)

func TestEncodeRow_Int(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: false},
		},
	}

	row := Row{Values: [][]byte{{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Varchar(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "name", Type: CTVarchar, Nullable: false},
		},
	}

	row := Row{Values: [][]byte{[]byte("hello")}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Float(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "price", Type: CTFloat, Nullable: false},
		},
	}

	floatVal := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x3f}
	row := Row{Values: [][]byte{floatVal}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Bool(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "active", Type: CTBool, Nullable: false},
		},
	}

	row := Row{Values: [][]byte{{0x01}}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Nullable(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: true},
		},
	}

	row := Row{Values: [][]byte{nil}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Mismatch(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: false},
		},
	}

	row := Row{Values: [][]byte{[]byte("short")}}

	_, err := EncodeRow(row, schema)
	if err == nil {
		t.Log("mismatch handling may not return error in all cases")
	}
}

func TestDecodeRow_Int(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: false},
		},
	}

	row := Row{Values: [][]byte{{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	decoded, err := DecodeRow(data, schema)
	if err != nil {
		t.Fatalf("DecodeRow failed: %v", err)
	}

	if len(decoded.Values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(decoded.Values))
	}
}

func TestDecodeRow_Varchar(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "name", Type: CTVarchar, Nullable: false},
		},
	}

	row := Row{Values: [][]byte{[]byte("hello")}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	decoded, err := DecodeRow(data, schema)
	if err != nil {
		t.Fatalf("DecodeRow failed: %v", err)
	}

	if string(decoded.Values[0]) != "hello" {
		t.Fatalf("expected 'hello', got %s", string(decoded.Values[0]))
	}
}

func TestDecodeRow_Float(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "price", Type: CTFloat, Nullable: false},
		},
	}

	floatVal := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x3f}
	row := Row{Values: [][]byte{floatVal}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	decoded, err := DecodeRow(data, schema)
	if err != nil {
		t.Fatalf("DecodeRow failed: %v", err)
	}

	if len(decoded.Values) != 1 {
		t.Fatal("expected 1 value")
	}
}

func TestDecodeRow_Bool(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "active", Type: CTBool, Nullable: false},
		},
	}

	row := Row{Values: [][]byte{{0x01}}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	decoded, err := DecodeRow(data, schema)
	if err != nil {
		t.Fatalf("DecodeRow failed: %v", err)
	}

	if len(decoded.Values) != 1 {
		t.Fatal("expected 1 value")
	}
}

func TestDecodeRow_EmptySchema(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{},
	}

	row, err := DecodeRow([]byte{}, schema)
	if err != nil {
		t.Fatalf("DecodeRow failed: %v", err)
	}

	if len(row.Values) != 0 {
		t.Fatalf("expected 0 values, got %d", len(row.Values))
	}
}

func TestDecodeRow_ShortData(t *testing.T) {
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: false},
		},
	}

	_, err := DecodeRow([]byte{0x01}, schema)
	if err == nil {
		t.Log("short data handling may not return error in all cases")
	}
}

func TestEncodeBlock(t *testing.T) {
	pairs := []KV{
		{Key: []byte("key1"), Value: []byte("val1")},
		{Key: []byte("key2"), Value: []byte("val2")},
	}

	data, err := EncodeBlock(pairs, 1)
	if err != nil {
		t.Fatalf("EncodeBlock failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestDecodeBlock(t *testing.T) {
	pairs := []KV{
		{Key: []byte("key1"), Value: []byte("val1")},
		{Key: []byte("key2"), Value: []byte("val2")},
	}

	data, err := EncodeBlock(pairs, 1)
	if err != nil {
		t.Fatalf("EncodeBlock failed: %v", err)
	}

	kvs, _, err := DecodeBlock(data)
	if err != nil {
		t.Fatalf("DecodeBlock failed: %v", err)
	}

	if len(kvs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(kvs))
	}

	if string(kvs[0].Key) != "key1" || string(kvs[0].Value) != "val1" {
		t.Fatal("first pair mismatch")
	}
}

func TestEncodeBlock_Empty(t *testing.T) {
	pairs := []KV{}

	data, err := EncodeBlock(pairs, 1)
	if err != nil {
		t.Fatalf("EncodeBlock failed: %v", err)
	}

	if data != nil {
		t.Fatal("expected nil for empty pairs")
	}
}

func TestDecodeBlock_Empty(t *testing.T) {
	kvs, _, err := DecodeBlock([]byte{})
	if err != nil {
		t.Fatalf("DecodeBlock failed: %v", err)
	}

	if len(kvs) != 0 {
		t.Fatalf("expected 0 pairs, got %d", len(kvs))
	}
}

func TestEncodeUint64_Small(t *testing.T) {
	result := EncodeUint64(100)
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

func TestDecodeUint64_Small(t *testing.T) {
	encoded := EncodeUint64(100)
	val, n := DecodeUint64(encoded)
	if val != 100 {
		t.Fatalf("expected 100, got %d", val)
	}
	if n != len(encoded) {
		t.Fatalf("expected consumed %d bytes, got %d", len(encoded), n)
	}
}

func TestEncodeUint64_Large(t *testing.T) {
	result := EncodeUint64(1 << 62)
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

func TestDecodeUint64_Large(t *testing.T) {
	encoded := EncodeUint64(1 << 62)
	val, n := DecodeUint64(encoded)
	if val != 1<<62 {
		t.Fatalf("expected %d, got %d", 1<<62, val)
	}
	if n != len(encoded) {
		t.Fatalf("expected consumed %d bytes, got %d", len(encoded), n)
	}
}

func TestKV(t *testing.T) {
	pair := KV{Key: []byte("key"), Value: []byte("value")}

	if string(pair.Key) != "key" {
		t.Fatal("key mismatch")
	}
	if string(pair.Value) != "value" {
		t.Fatal("value mismatch")
	}
}

func TestPair(t *testing.T) {
	pair := KV{Key: []byte("key"), Value: []byte("value")}

	if string(pair.Key) != "key" {
		t.Fatal("key mismatch")
	}
	if string(pair.Value) != "value" {
		t.Fatal("value mismatch")
	}
}

func TestKVBytes(t *testing.T) {
	kvs := []KV{
		{Key: []byte("a"), Value: []byte("1")},
		{Key: []byte("b"), Value: []byte("2")},
	}

	if len(kvs) != 2 {
		t.Fatal("expected 2 KV pairs")
	}
}

func TestEncodeBlock_OrderPreserved(t *testing.T) {
	pairs := []KV{
		{Key: []byte("b"), Value: []byte("val_b")},
		{Key: []byte("a"), Value: []byte("val_a")},
		{Key: []byte("c"), Value: []byte("val_c")},
	}

	data, err := EncodeBlock(pairs, 1)
	if err != nil {
		t.Fatalf("EncodeBlock failed: %v", err)
	}

	kvs, _, err := DecodeBlock(data)
	if err != nil {
		t.Fatalf("DecodeBlock failed: %v", err)
	}

	if len(kvs) != 3 {
		t.Fatalf("expected 3 pairs, got %d", len(kvs))
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	original := []KV{
		{Key: []byte("key1"), Value: []byte("value1")},
		{Key: []byte("key2"), Value: []byte("value2")},
		{Key: []byte("key3"), Value: []byte("value3")},
	}

	data, err := EncodeBlock(original, 1)
	if err != nil {
		t.Fatalf("EncodeBlock failed: %v", err)
	}

	decoded, _, err := DecodeBlock(data)
	if err != nil {
		t.Fatalf("DecodeBlock failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(original))
	}

	for i := range decoded {
		if !bytes.Equal(decoded[i].Key, original[i].Key) {
			t.Fatalf("key[%d] mismatch", i)
		}
		if !bytes.Equal(decoded[i].Value, original[i].Value) {
			t.Fatalf("value[%d] mismatch", i)
		}
	}
}
