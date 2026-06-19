package sc

import (
	"math"
	"strings"
	"testing"
	"testing/quick"
)

func TestEncodeDecodeInt(t *testing.T) {
	tests := []struct {
		name  string
		value int64
	}{
		{"zero", 0},
		{"positive", 42},
		{"negative", -42},
		{"max int64", math.MaxInt64},
		{"min int64", math.MinInt64},
		{"one", 1},
		{"negative one", -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeInt(tc.value)
			decoded, err := DecodeInt(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %d, got %d", tc.value, decoded)
			}
		})
	}
}

func TestDecodeIntInvalidLength(t *testing.T) {
	_, err := DecodeInt([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeDecodeFloat(t *testing.T) {
	tests := []struct {
		name  string
		value float64
	}{
		{"zero", 0},
		{"positive", 3.14},
		{"negative", -2.71},
		{"max float64", math.MaxFloat64},
		{"smallest positive", math.SmallestNonzeroFloat64},
		{"NaN", math.NaN()},
		{"Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeFloat(tc.value)
			decoded, err := DecodeFloat(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.IsNaN(tc.value) {
				if !math.IsNaN(decoded) {
					t.Fatal("expected NaN")
				}
				return
			}
			if decoded != tc.value {
				t.Fatalf("expected %v, got %v", tc.value, decoded)
			}
		})
	}
}

func TestDecodeFloatInvalidLength(t *testing.T) {
	_, err := DecodeFloat([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeDecodeBool(t *testing.T) {
	tests := []struct {
		name  string
		value bool
	}{
		{"true", true},
		{"false", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeBool(tc.value)
			decoded, err := DecodeBool(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %v, got %v", tc.value, decoded)
			}
		})
	}
}

func TestDecodeBoolInvalidLength(t *testing.T) {
	_, err := DecodeBool([]byte{1, 2})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeDecodeVarchar(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"short", "hello"},
		{"with spaces", "hello world"},
		{"unicode", "你好世界"},
		{"special chars", "a\tb\nc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeVarchar(tc.value)
			decoded, err := DecodeVarchar(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %q, got %q", tc.value, decoded)
			}
		})
	}
}

func TestEncodeDecodeText(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"short", "hello"},
		{"long", strings.Repeat("a", 10000)},
		{"unicode", "你好世界"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeText(tc.value)
			decoded, err := DecodeText(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %q, got %q", tc.value, decoded)
			}
		})
	}
}

func TestEncodeDecodeBlob(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"single byte", []byte{0xFF}},
		{"multiple bytes", []byte{0x00, 0x01, 0x02, 0x03}},
		{"large", make([]byte, 10000)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeBlob(tc.value)
			decoded, err := DecodeBlob(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tc.value) == 0 && len(decoded) == 0 {
				return
			}
			if len(decoded) != len(tc.value) {
				t.Fatalf("expected len %d, got %d", len(tc.value), len(decoded))
			}
			for i := range tc.value {
				if decoded[i] != tc.value[i] {
					t.Fatalf("byte %d: expected %d, got %d", i, tc.value[i], decoded[i])
				}
			}
		})
	}
}

func TestEncodeDecodeTimestamp(t *testing.T) {
	tests := []struct {
		name  string
		value int64
	}{
		{"zero", 0},
		{"positive", 1700000000},
		{"negative", -1},
		{"max int64", math.MaxInt64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeTimestamp(tc.value)
			decoded, err := DecodeTimestamp(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %d, got %d", tc.value, decoded)
			}
		})
	}
}

func TestDecodeTimestampInvalidLength(t *testing.T) {
	_, err := DecodeTimestamp([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestEncodeDecodeBigInt(t *testing.T) {
	tests := []struct {
		name  string
		value int64
	}{
		{"zero", 0},
		{"positive", 9999999999},
		{"negative", -9999999999},
		{"max int64", math.MaxInt64},
		{"min int64", math.MinInt64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeBigInt(tc.value)
			decoded, err := DecodeBigInt(encoded)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decoded != tc.value {
				t.Fatalf("expected %d, got %d", tc.value, decoded)
			}
		})
	}
}

func TestDecodeBigIntInvalidLength(t *testing.T) {
	_, err := DecodeBigInt([]byte{1, 2, 3})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestValidateRow(t *testing.T) {
	v := NewValidator()

	t.Run("valid row", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "id", Type: CTInt, Nullable: false},
				{Name: "name", Type: CTVarchar, Nullable: true},
			},
		}
		row := Row{Values: [][]byte{EncodeInt(1), EncodeVarchar("alice")}}
		if err := v.ValidateRow(row, schema); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("null value not allowed", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "id", Type: CTInt, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{nil}}
		if err := v.ValidateRow(row, schema); err != ErrNullValue {
			t.Fatalf("expected ErrNullValue, got %v", err)
		}
	})

	t.Run("null value allowed", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "name", Type: CTVarchar, Nullable: true},
			},
		}
		row := Row{Values: [][]byte{nil}}
		if err := v.ValidateRow(row, schema); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("type mismatch on int", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "id", Type: CTInt, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{[]byte{1, 2, 3}}}
		if err := v.ValidateRow(row, schema); err != ErrTypeMismatch {
			t.Fatalf("expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("type mismatch on bool", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "flag", Type: CTBool, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{[]byte{1, 2}}}
		if err := v.ValidateRow(row, schema); err != ErrTypeMismatch {
			t.Fatalf("expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("type mismatch on float", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "val", Type: CTFloat, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{[]byte{1, 2, 3}}}
		if err := v.ValidateRow(row, schema); err != ErrTypeMismatch {
			t.Fatalf("expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("type mismatch on timestamp", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "ts", Type: CTTimestamp, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{[]byte{1, 2, 3}}}
		if err := v.ValidateRow(row, schema); err != ErrTypeMismatch {
			t.Fatalf("expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("type mismatch on bigint", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "val", Type: CTBigInt, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{[]byte{1, 2, 3}}}
		if err := v.ValidateRow(row, schema); err != ErrTypeMismatch {
			t.Fatalf("expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("varchar any length passes", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "name", Type: CTVarchar, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{EncodeVarchar("")}}
		if err := v.ValidateRow(row, schema); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("text any length passes", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "body", Type: CTText, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{EncodeText("long text")}}
		if err := v.ValidateRow(row, schema); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("blob any length passes", func(t *testing.T) {
		schema := &TableSchema{
			Columns: []ColumnDef{
				{Name: "data", Type: CTBlob, Nullable: false},
			},
		}
		row := Row{Values: [][]byte{EncodeBlob([]byte{})}}
		if err := v.ValidateRow(row, schema); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateType(t *testing.T) {
	v := NewValidator()

	t.Run("unknown type returns ErrInvalidValue", func(t *testing.T) {
		err := v.ValidateType([]byte{0}, ColumnType(255))
		if err != ErrInvalidValue {
			t.Fatalf("expected ErrInvalidValue, got %v", err)
		}
	})

	t.Run("nil value returns nil", func(t *testing.T) {
		err := v.ValidateType(nil, CTInt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateConstraints(t *testing.T) {
	v := NewValidator()

	t.Run("no default always succeeds", func(t *testing.T) {
		col := &ColumnDef{Name: "id", Type: CTInt, Default: nil}
		if err := v.ValidateConstraints(EncodeInt(42), col); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("value equals default succeeds", func(t *testing.T) {
		col := &ColumnDef{Name: "id", Type: CTInt, Default: EncodeInt(0)}
		if err := v.ValidateConstraints(EncodeInt(0), col); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("value differs from default succeeds", func(t *testing.T) {
		col := &ColumnDef{Name: "id", Type: CTInt, Default: EncodeInt(0)}
		if err := v.ValidateConstraints(EncodeInt(1), col); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("default with empty bytes", func(t *testing.T) {
		col := &ColumnDef{Name: "name", Type: CTVarchar, Default: []byte{}}
		if err := v.ValidateConstraints(EncodeVarchar(""), col); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestCompareColumnDef(t *testing.T) {
	v := NewValidator()

	t.Run("identical", func(t *testing.T) {
		a := ColumnDef{Name: "id", Type: CTInt, Nullable: false, PrimaryKey: true}
		b := ColumnDef{Name: "id", Type: CTInt, Nullable: false, PrimaryKey: true}
		if !v.CompareColumnDef(a, b) {
			t.Fatal("expected true for identical column defs")
		}
	})

	t.Run("different name", func(t *testing.T) {
		a := ColumnDef{Name: "id"}
		b := ColumnDef{Name: "name"}
		if v.CompareColumnDef(a, b) {
			t.Fatal("expected false for different name")
		}
	})

	t.Run("different type", func(t *testing.T) {
		a := ColumnDef{Name: "id", Type: CTInt}
		b := ColumnDef{Name: "id", Type: CTBigInt}
		if v.CompareColumnDef(a, b) {
			t.Fatal("expected false for different type")
		}
	})

	t.Run("different nullable", func(t *testing.T) {
		a := ColumnDef{Name: "id", Nullable: false}
		b := ColumnDef{Name: "id", Nullable: true}
		if v.CompareColumnDef(a, b) {
			t.Fatal("expected false for different nullable")
		}
	})

	t.Run("different primary key", func(t *testing.T) {
		a := ColumnDef{Name: "id", PrimaryKey: false}
		b := ColumnDef{Name: "id", PrimaryKey: true}
		if v.CompareColumnDef(a, b) {
			t.Fatal("expected false for different primary key")
		}
	})
}

func TestDecodeIntErrorsOnWrongLen(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"too short", []byte{1, 2, 3, 4}},
		{"too long", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeInt(tc.data)
			if err != ErrTypeMismatch {
				t.Fatalf("expected ErrTypeMismatch, got %v", err)
			}
		})
	}
}

func TestDecodeFloatErrorsOnWrongLen(t *testing.T) {
	_, err := DecodeFloat([]byte{1, 2, 3, 4})
	if err != ErrTypeMismatch {
		t.Fatalf("expected ErrTypeMismatch, got %v", err)
	}
}

func TestDecodeBoolErrorsOnWrongLen(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"too long", []byte{1, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeBool(tc.data)
			if err != ErrTypeMismatch {
				t.Fatalf("expected ErrTypeMismatch, got %v", err)
			}
		})
	}
}

func TestEncodeDecodeIntFuzz(t *testing.T) {
	f := func(v int64) bool {
		decoded, err := DecodeInt(EncodeInt(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeFloatFuzz(t *testing.T) {
	f := func(v float64) bool {
		decoded, err := DecodeFloat(EncodeFloat(v))
		if err != nil {
			return false
		}
		if math.IsNaN(v) {
			return math.IsNaN(decoded)
		}
		return decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeBoolFuzz(t *testing.T) {
	f := func(v bool) bool {
		decoded, err := DecodeBool(EncodeBool(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeVarcharFuzz(t *testing.T) {
	f := func(v string) bool {
		decoded, err := DecodeVarchar(EncodeVarchar(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeTextFuzz(t *testing.T) {
	f := func(v string) bool {
		decoded, err := DecodeText(EncodeText(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeBlobFuzz(t *testing.T) {
	f := func(v []byte) bool {
		decoded, err := DecodeBlob(EncodeBlob(v))
		if err != nil {
			return false
		}
		if len(v) == 0 && len(decoded) == 0 {
			return true
		}
		if len(decoded) != len(v) {
			return false
		}
		for i := range v {
			if decoded[i] != v[i] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeTimestampFuzz(t *testing.T) {
	f := func(v int64) bool {
		decoded, err := DecodeTimestamp(EncodeTimestamp(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeDecodeBigIntFuzz(t *testing.T) {
	f := func(v int64) bool {
		decoded, err := DecodeBigInt(EncodeBigInt(v))
		return err == nil && decoded == v
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}
