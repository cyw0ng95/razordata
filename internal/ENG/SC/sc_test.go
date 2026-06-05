package sc

import "testing"

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
			make([]byte, 8),
			[]byte("Alice"),
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

// TestValidateRow_WrongColumnCount preserves the v1 behavior: a row
// with fewer values than columns causes an index-out-of-range panic
// in ValidateRow. The test uses a defer/recover to assert the panic
// and lock the behavior down so future refactors don't quietly
// change it.
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

func TestValidateType_UnknownType(t *testing.T) {
	v := NewValidator()
	err := v.ValidateType([]byte{1}, ColumnType(99))
	if err != ErrInvalidValue {
		t.Fatalf("expected ErrInvalidValue, got %v", err)
	}
}

func TestValidateType_NilVal(t *testing.T) {
	v := NewValidator()
	if err := v.ValidateType(nil, CTInt); err != nil {
		t.Fatalf("nil val should be accepted at type level, got %v", err)
	}
}

func TestNewValidator_NonNil(t *testing.T) {
	if NewValidator() == nil {
		t.Fatal("NewValidator returned nil")
	}
}
