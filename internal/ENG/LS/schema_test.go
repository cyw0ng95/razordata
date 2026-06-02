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
