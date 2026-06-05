package tb

import (
	"errors"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func TestSchemaCodec_RoundTrip_Empty(t *testing.T) {
	schema := &sc.TableSchema{
		TableID: 0,
		Name:    "empty",
	}
	data, err := MarshalTableSchema(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalTableSchema(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Name != schema.Name {
		t.Errorf("Name: got %q, want %q", got.Name, schema.Name)
	}
	if len(got.Columns) != 0 {
		t.Errorf("Columns: got %d, want 0", len(got.Columns))
	}
	if len(got.PrimaryKey) != 0 {
		t.Errorf("PrimaryKey: got %d, want 0", len(got.PrimaryKey))
	}
}

func TestSchemaCodec_RoundTrip_SingleColumn(t *testing.T) {
	schema := &sc.TableSchema{
		Name: "t",
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false, PrimaryKey: true},
		},
		PrimaryKey: []int{0},
	}
	data, err := MarshalTableSchema(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalTableSchema(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Name != "t" {
		t.Errorf("Name: got %q", got.Name)
	}
	if len(got.Columns) != 1 {
		t.Fatalf("Columns: got %d, want 1", len(got.Columns))
	}
	if got.Columns[0].Name != "id" || got.Columns[0].Type != sc.CTInt || got.Columns[0].Nullable {
		t.Errorf("Column mismatch: %+v", got.Columns[0])
	}
	if len(got.PrimaryKey) != 1 || got.PrimaryKey[0] != 0 {
		t.Errorf("PrimaryKey mismatch: %v", got.PrimaryKey)
	}
}

func TestSchemaCodec_RoundTrip_AllColumnTypes(t *testing.T) {
	types := []sc.ColumnType{
		sc.CTInt, sc.CTBigInt, sc.CTVarchar, sc.CTFloat,
		sc.CTBool, sc.CTText, sc.CTBlob, sc.CTTimestamp,
	}
	cols := make([]sc.ColumnDef, len(types))
	for i, ty := range types {
		cols[i] = sc.ColumnDef{
			Name:     "c" + string(rune('A'+i)),
			Type:     ty,
			Nullable: i%2 == 0,
		}
	}
	schema := &sc.TableSchema{Name: "all_types", Columns: cols, PrimaryKey: []int{0, 1}}

	data, err := MarshalTableSchema(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalTableSchema(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.Columns) != len(types) {
		t.Fatalf("Columns: got %d, want %d", len(got.Columns), len(types))
	}
	for i, ty := range types {
		if got.Columns[i].Type != ty {
			t.Errorf("Column[%d].Type: got %d, want %d", i, got.Columns[i].Type, ty)
		}
		if got.Columns[i].Nullable != (i%2 == 0) {
			t.Errorf("Column[%d].Nullable: got %v", i, got.Columns[i].Nullable)
		}
	}
}

func TestSchemaCodec_RoundTrip_WithDefaults(t *testing.T) {
	schema := &sc.TableSchema{
		Name: "with_defaults",
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false},
			{Name: "status", Type: sc.CTVarchar, Nullable: true, Default: []byte("active")},
			{Name: "blob_col", Type: sc.CTBlob, Nullable: true, Default: []byte{0xDE, 0xAD, 0xBE, 0xEF}},
		},
	}
	data, err := MarshalTableSchema(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalTableSchema(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(got.Columns[1].Default) != "active" {
		t.Errorf("default[1]: got %q, want %q", string(got.Columns[1].Default), "active")
	}
	if len(got.Columns[2].Default) != 4 || got.Columns[2].Default[0] != 0xDE {
		t.Errorf("default[2]: got %v", got.Columns[2].Default)
	}
}

func TestSchemaCodec_RoundTrip_CompositePK(t *testing.T) {
	schema := &sc.TableSchema{
		Name: "composite",
		Columns: []sc.ColumnDef{
			{Name: "a", Type: sc.CTInt},
			{Name: "b", Type: sc.CTInt},
			{Name: "c", Type: sc.CTInt},
		},
		PrimaryKey: []int{0, 2},
	}
	data, err := MarshalTableSchema(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalTableSchema(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.PrimaryKey) != 2 || got.PrimaryKey[0] != 0 || got.PrimaryKey[1] != 2 {
		t.Errorf("PK: got %v", got.PrimaryKey)
	}
}

func TestSchemaCodec_RoundTrip_LongName(t *testing.T) {
	longName := make([]byte, 1024)
	for i := range longName {
		longName[i] = byte('a' + (i % 26))
	}
	schema := &sc.TableSchema{Name: string(longName)}
	data, err := MarshalTableSchema(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalTableSchema(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Name != string(longName) {
		t.Errorf("Name mismatch on round-trip (got len=%d, want len=%d)", len(got.Name), len(longName))
	}
}

func TestSchemaCodec_Truncated_Name(t *testing.T) {
	_, err := UnmarshalTableSchema([]byte{0x05, 0x00})
	if !errors.Is(err, ErrSchemaCodecTruncated) {
		t.Fatalf("expected ErrSchemaCodecTruncated, got %v", err)
	}
}

func TestSchemaCodec_Truncated_ColumnCount(t *testing.T) {
	// name len 0, then no column count bytes
	_, err := UnmarshalTableSchema([]byte{0x00, 0x00, 0x00, 0x00})
	if !errors.Is(err, ErrSchemaCodecTruncated) {
		t.Fatalf("expected ErrSchemaCodecTruncated, got %v", err)
	}
}

func TestSchemaCodec_Truncated_ColumnData(t *testing.T) {
	// name len 0, col count 1, then no column data
	data := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	_, err := UnmarshalTableSchema(data)
	if !errors.Is(err, ErrSchemaCodecTruncated) {
		t.Fatalf("expected ErrSchemaCodecTruncated, got %v", err)
	}
}

func TestSchemaCodec_Truncated_Default(t *testing.T) {
	// name 0, col count 1, col name len 1, col name "x", type=0, nullable=0,
	// default len = 100, but no default bytes follow.
	data := []byte{
		0x00, 0x00, 0x00, 0x00, // name len 0
		0x01, 0x00, 0x00, 0x00, // col count 1
		0x01, 0x00, 0x00, 0x00, // col name len 1
		'x',                    // col name
		0x00,                   // type
		0x00,                   // nullable
		0x64, 0x00, 0x00, 0x00, // default len 100
	}
	_, err := UnmarshalTableSchema(data)
	if !errors.Is(err, ErrSchemaCodecTruncated) {
		t.Fatalf("expected ErrSchemaCodecTruncated, got %v", err)
	}
}

func TestSchemaCodec_Truncated_PK(t *testing.T) {
	// name 0, col count 0, pk count 1, but no pk entries
	data := []byte{
		0x00, 0x00, 0x00, 0x00, // name len 0
		0x00, 0x00, 0x00, 0x00, // col count 0
		0x01, 0x00, 0x00, 0x00, // pk count 1
	}
	_, err := UnmarshalTableSchema(data)
	if !errors.Is(err, ErrSchemaCodecTruncated) {
		t.Fatalf("expected ErrSchemaCodecTruncated, got %v", err)
	}
}

func TestSchemaCodec_NilSchema(t *testing.T) {
	_, err := MarshalTableSchema(nil)
	if err == nil {
		t.Fatal("expected error on nil schema")
	}
}
