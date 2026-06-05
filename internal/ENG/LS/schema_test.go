package ls

import (
	"testing"
)

func TestEncodeDecodeInt(t *testing.T) {
	original := int64(12345)
	encoded := EncodeInt(original)

	decoded, err := DecodeInt(encoded)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if decoded != original {
		t.Fatalf("expected %d, got %d", original, decoded)
	}
}

func TestEncodeDecodeFloat(t *testing.T) {
	original := float64(3.14159)
	encoded := EncodeFloat(original)

	decoded, err := DecodeFloat(encoded)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if decoded != original {
		t.Fatalf("expected %f, got %f", original, decoded)
	}
}

func TestEncodeDecodeBool(t *testing.T) {
	testCases := []bool{true, false}

	for _, original := range testCases {
		encoded := EncodeBool(original)
		decoded, err := DecodeBool(encoded)
		if err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if decoded != original {
			t.Fatalf("expected %v, got %v", original, decoded)
		}
	}
}

func TestEncodeDecodeVarchar(t *testing.T) {
	original := "hello world"
	encoded := EncodeVarchar(original)

	decoded, err := DecodeVarchar(encoded)
	if err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if decoded != original {
		t.Fatalf("expected %s, got %s", original, decoded)
	}
}

func TestValidateRow_Success(t *testing.T) {
	v := NewValidator()

	schema := &TableSchema{
		TableID: 1,
		Name:    "users",
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: false},
			{Name: "name", Type: CTVarchar, Nullable: false},
		},
	}

	row := Row{
		Values: [][]byte{
			EncodeInt(1),
			EncodeVarchar("Alice"),
		},
	}

	if err := v.ValidateRow(row, schema); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRow_NullNotNullable(t *testing.T) {
	v := NewValidator()

	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: false},
		},
	}

	row := Row{
		Values: [][]byte{nil},
	}

	if err := v.ValidateRow(row, schema); err != ErrNullValue {
		t.Fatalf("expected ErrNullValue, got %v", err)
	}
}

func TestValidateRow_NullNullable(t *testing.T) {
	v := NewValidator()

	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt, Nullable: true},
		},
	}

	row := Row{
		Values: [][]byte{nil},
	}

	if err := v.ValidateRow(row, schema); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRow_TypeMismatch(t *testing.T) {
	v := NewValidator()

	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "id", Type: CTInt},
		},
	}

	row := Row{
		Values: [][]byte{[]byte("not an int")},
	}

	if err := v.ValidateRow(row, schema); err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestCompareColumnDef(t *testing.T) {
	v := NewValidator()

	col1 := ColumnDef{Name: "id", Type: CTInt, Nullable: false, PrimaryKey: true}
	col2 := ColumnDef{Name: "id", Type: CTInt, Nullable: false, PrimaryKey: true}
	col3 := ColumnDef{Name: "name", Type: CTVarchar, Nullable: false, PrimaryKey: false}

	if !v.CompareColumnDef(col1, col2) {
		t.Fatal("expected equal column defs")
	}

	if v.CompareColumnDef(col1, col3) {
		t.Fatal("expected unequal column defs")
	}
}

func TestValidateRow_AllTypes(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		name  string
		col   ColumnDef
		valid bool
	}{
		{"int valid", ColumnDef{Name: "c", Type: CTInt}, true},
		{"bigint valid", ColumnDef{Name: "c", Type: CTBigInt}, true},
		{"float valid", ColumnDef{Name: "c", Type: CTFloat}, true},
		{"bool valid", ColumnDef{Name: "c", Type: CTBool}, true},
		{"varchar valid", ColumnDef{Name: "c", Type: CTVarchar}, true},
		{"text valid", ColumnDef{Name: "c", Type: CTText}, true},
		{"blob valid", ColumnDef{Name: "c", Type: CTBlob}, true},
		{"timestamp valid", ColumnDef{Name: "c", Type: CTTimestamp}, true},
		{"int wrong size", ColumnDef{Name: "c", Type: CTInt}, false},
		{"bigint wrong size", ColumnDef{Name: "c", Type: CTBigInt}, false},
		{"float wrong size", ColumnDef{Name: "c", Type: CTFloat}, false},
		{"bool wrong size", ColumnDef{Name: "c", Type: CTBool}, false},
		{"timestamp wrong size", ColumnDef{Name: "c", Type: CTTimestamp}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var val []byte
			switch tt.col.Type {
			case CTInt, CTBigInt, CTFloat, CTTimestamp:
				if tt.valid {
					val = make([]byte, 8)
				} else {
					val = make([]byte, 4)
				}
			case CTBool:
				if tt.valid {
					val = make([]byte, 1)
				} else {
					val = make([]byte, 2)
				}
			default:
				val = []byte("test")
			}

			schema := &TableSchema{Columns: []ColumnDef{tt.col}}
			row := Row{Values: [][]byte{val}}

			err := v.ValidateRow(row, schema)
			if tt.valid && err != nil {
				t.Errorf("expected valid, got %v", err)
			}
			if !tt.valid && err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestEncodeDecodeText(t *testing.T) {
	original := "Hello, World!"

	encoded := EncodeText(original)
	decoded, err := DecodeText(encoded)
	if err != nil {
		t.Fatalf("DecodeText failed: %v", err)
	}
	if decoded != original {
		t.Fatalf("expected %s, got %s", original, decoded)
	}
}

func TestEncodeDecodeBlob(t *testing.T) {
	original := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	encoded := EncodeBlob(original)
	decoded, err := DecodeBlob(encoded)
	if err != nil {
		t.Fatalf("DecodeBlob failed: %v", err)
	}
	if string(decoded) != string(original) {
		t.Fatalf("expected %v, got %v", original, decoded)
	}
}

func TestEncodeDecodeTimestamp(t *testing.T) {
	original := int64(1234567890)

	encoded := EncodeTimestamp(original)
	decoded, err := DecodeTimestamp(encoded)
	if err != nil {
		t.Fatalf("DecodeTimestamp failed: %v", err)
	}
	if decoded != original {
		t.Fatalf("expected %d, got %d", original, decoded)
	}
}

func TestDecodeInt_InvalidLength(t *testing.T) {
	_, err := DecodeInt([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeFloat_InvalidLength(t *testing.T) {
	_, err := DecodeFloat([]byte{1})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeBool_InvalidLength(t *testing.T) {
	_, err := DecodeBool([]byte{1, 0})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestCompareColumnDef_DifferentNames(t *testing.T) {
	v := NewValidator()
	col1 := ColumnDef{Name: "a", Type: CTInt}
	col2 := ColumnDef{Name: "b", Type: CTInt}

	if v.CompareColumnDef(col1, col2) {
		t.Error("expected false for different names")
	}
}

func TestCompareColumnDef_DifferentTypes(t *testing.T) {
	v := NewValidator()
	col1 := ColumnDef{Name: "a", Type: CTInt}
	col2 := ColumnDef{Name: "a", Type: CTVarchar}

	if v.CompareColumnDef(col1, col2) {
		t.Error("expected false for different types")
	}
}

func TestCompareColumnDef_DifferentNullable(t *testing.T) {
	v := NewValidator()
	col1 := ColumnDef{Name: "a", Type: CTInt, Nullable: true}
	col2 := ColumnDef{Name: "a", Type: CTInt, Nullable: false}

	if v.CompareColumnDef(col1, col2) {
		t.Error("expected false for different nullable")
	}
}

func TestCompareColumnDef_DifferentPrimaryKey(t *testing.T) {
	v := NewValidator()
	col1 := ColumnDef{Name: "a", Type: CTInt, PrimaryKey: true}
	col2 := ColumnDef{Name: "a", Type: CTInt, PrimaryKey: false}

	if v.CompareColumnDef(col1, col2) {
		t.Error("expected false for different primary key")
	}
}

func TestValidateConstraints_DefaultValue(t *testing.T) {
	v := NewValidator()
	col := ColumnDef{Name: "c", Type: CTVarchar, Default: []byte("default")}

	err := v.ValidateConstraints([]byte("default"), &col)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRow_WrongColumnCount(t *testing.T) {
	v := NewValidator()
	schema := &TableSchema{
		Columns: []ColumnDef{
			{Name: "a", Type: CTInt},
			{Name: "b", Type: CTInt},
		},
	}
	row := Row{Values: [][]byte{make([]byte, 8)}}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wrong column count")
		}
	}()
	v.ValidateRow(row, schema)
}

func TestEncodeVarchar(t *testing.T) {
	original := "test string"
	encoded := EncodeVarchar(original)
	if string(encoded) != original {
		t.Errorf("expected %s, got %s", original, string(encoded))
	}
}

func TestEncodeInt_AllValues(t *testing.T) {
	tests := []int64{0, 1, -1, 127, -128, 255, -255, 32767, -32768, 2147483647, -2147483648}

	for _, v := range tests {
		encoded := EncodeInt(v)
		decoded, err := DecodeInt(encoded)
		if err != nil {
			t.Errorf("DecodeInt failed for %d: %v", v, err)
		}
		if decoded != v {
			t.Errorf("expected %d, got %d", v, decoded)
		}
	}
}
