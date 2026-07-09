package WT

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestValidateStrictRow_NonStrictSkips(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"id", "name"},
		ColTypes: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT},
		Strict:   false,
	}
	row := DT.Row{
		Data: []DT.Value{DT.NewTextValue("not_int"), DT.NewIntValue(42)},
	}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("ValidateStrictRow on non-strict table: %v", err)
	}
}

func TestValidateStrictRow_IntegerMatch(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"id"},
		ColTypes: []LX.TokenType{LX.T_INT_KW},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NewIntValue(42)}}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("INTEGER match: %v", err)
	}
}

func TestValidateStrictRow_IntegerMismatch(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"id"},
		ColTypes: []LX.TokenType{LX.T_INT_KW},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NewTextValue("hello")}}
	err := ValidateStrictRow(schema, row)
	if err == nil {
		t.Fatal("expected error for INTEGER column with TEXT value")
	}
}

func TestValidateStrictRow_TextMatch(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"name"},
		ColTypes: []LX.TokenType{LX.T_TEXT},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NewTextValue("hello")}}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("TEXT match: %v", err)
	}
}

func TestValidateStrictRow_TextMismatch(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"name"},
		ColTypes: []LX.TokenType{LX.T_TEXT},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NewIntValue(42)}}
	err := ValidateStrictRow(schema, row)
	if err == nil {
		t.Fatal("expected error for TEXT column with INTEGER value")
	}
}

func TestValidateStrictRow_NullAllowed(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"id", "name"},
		ColTypes: []LX.TokenType{LX.T_INT_KW, LX.T_TEXT},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NullValue(), DT.NullValue()}}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("NULL values in STRICT table: %v", err)
	}
}

func TestValidateStrictRow_FloatMatch(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"score"},
		ColTypes: []LX.TokenType{LX.T_FLOAT_KW},
		Strict:   true,
	}
	// STRICT REAL accepts both REAL and INTEGER
	row := DT.Row{Data: []DT.Value{DT.NewFloatValue(3.14)}}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("REAL match: %v", err)
	}
}

func TestValidateStrictRow_FloatAcceptsInt(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"score"},
		ColTypes: []LX.TokenType{LX.T_FLOAT_KW},
		Strict:   true,
	}
	// STRICT REAL accepts INTEGER (SQLite compat)
	row := DT.Row{Data: []DT.Value{DT.NewIntValue(42)}}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("REAL accepts INTEGER: %v", err)
	}
}

func TestValidateStrictRow_BlobMatch(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"data"},
		ColTypes: []LX.TokenType{LX.T_BLOB},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NewBlobValue([]byte("hello"))}}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("BLOB match: %v", err)
	}
}

func TestValidateStrictRow_BlobMismatch(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"data"},
		ColTypes: []LX.TokenType{LX.T_BLOB},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NewTextValue("hello")}}
	err := ValidateStrictRow(schema, row)
	if err == nil {
		t.Fatal("expected error for BLOB column with TEXT value")
	}
}

func TestValidateStrictRow_UnknownColType(t *testing.T) {
	schema := &DT.StoreSchema{
		Cols:     []string{"val"},
		ColTypes: []LX.TokenType{0},
		Strict:   true,
	}
	row := DT.Row{Data: []DT.Value{DT.NewIntValue(42)}}
	err := ValidateStrictRow(schema, row)
	if err != nil {
		t.Fatalf("unknown col type 0 should accept any value: %v", err)
	}
}

func TestKindName(t *testing.T) {
	tests := []struct {
		kind DT.ValueKind
		want string
	}{
		{DT.KindNull, "NULL"},
		{DT.KindInt, "INTEGER"},
		{DT.KindFloat, "REAL"},
		{DT.KindText, "TEXT"},
		{DT.KindBlob, "BLOB"},
		{EV.KindBool, "UNKNOWN"},
	}
	for _, tc := range tests {
		got := kindName(tc.kind)
		if got != tc.want {
			t.Errorf("kindName(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}