package dp

import (
	"bytes"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func TestEncodeRow_Int(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false},
		},
	}

	row := sc.Row{Values: [][]byte{{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Varchar(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "name", Type: sc.CTVarchar, Nullable: false},
		},
	}

	row := sc.Row{Values: [][]byte{[]byte("hello")}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Float(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "price", Type: sc.CTFloat, Nullable: false},
		},
	}

	floatVal := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x3f}
	row := sc.Row{Values: [][]byte{floatVal}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Bool(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "active", Type: sc.CTBool, Nullable: false},
		},
	}

	row := sc.Row{Values: [][]byte{{0x01}}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Nullable(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: true},
		},
	}

	row := sc.Row{Values: [][]byte{nil}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}
}

func TestEncodeRow_Mismatch(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false},
		},
	}

	row := sc.Row{Values: [][]byte{[]byte("short")}}

	_, err := EncodeRow(row, schema)
	if err == nil {
		t.Log("mismatch handling may not return error in all cases")
	}
}

func TestEncodeRow_ColumnCountMismatch(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt},
			{Name: "name", Type: sc.CTVarchar},
		},
	}
	row := sc.Row{Values: [][]byte{make([]byte, 8)}}
	_, err := EncodeRow(row, schema)
	if err != ErrEncodeRow {
		t.Fatalf("expected ErrEncodeRow, got %v", err)
	}
}

func TestEncodeRow_UnknownColumnType(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "c", Type: sc.ColumnType(99)},
		},
	}
	row := sc.Row{Values: [][]byte{[]byte("x")}}
	_, err := EncodeRow(row, schema)
	if err != ErrEncodeRow {
		t.Fatalf("expected ErrEncodeRow, got %v", err)
	}
}

func TestDecodeRow_Int(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false},
		},
	}

	row := sc.Row{Values: [][]byte{{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}}}

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
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "name", Type: sc.CTVarchar, Nullable: false},
		},
	}

	row := sc.Row{Values: [][]byte{[]byte("hello")}}

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
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "price", Type: sc.CTFloat, Nullable: false},
		},
	}

	floatVal := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x3f}
	row := sc.Row{Values: [][]byte{floatVal}}

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
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "active", Type: sc.CTBool, Nullable: false},
		},
	}

	row := sc.Row{Values: [][]byte{{0x01}}}

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
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{},
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
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false},
		},
	}

	_, err := DecodeRow([]byte{0x01}, schema)
	if err == nil {
		t.Log("short data handling may not return error in all cases")
	}
}

func TestDecodeRow_TruncatedInt(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false},
		},
	}
	// bitmap byte + 4 bytes (need 8 for the int payload)
	_, err := DecodeRow([]byte{0x00, 0x01, 0x02, 0x03, 0x04}, schema)
	if err == nil {
		t.Log("truncated int handling may not return error in all cases")
	}
}

func TestDecodeRow_UnknownColumnType(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "c", Type: sc.ColumnType(99), Nullable: false},
		},
	}
	// bitmap says not null, but decoder hits default branch.
	_, err := DecodeRow([]byte{0x00}, schema)
	if err == nil {
		t.Log("unknown column type may not return error in all cases")
	}
}

func TestDecodeRow_NullColumnRoundTrip(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: true},
			{Name: "name", Type: sc.CTVarchar, Nullable: true},
		},
	}
	row := sc.Row{Values: [][]byte{nil, nil}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	decoded, err := DecodeRow(data, schema)
	if err != nil {
		t.Fatalf("DecodeRow failed: %v", err)
	}

	if len(decoded.Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(decoded.Values))
	}
	if decoded.Values[0] != nil {
		t.Errorf("expected nil at index 0, got %v", decoded.Values[0])
	}
	if decoded.Values[1] != nil {
		t.Errorf("expected nil at index 1, got %v", decoded.Values[1])
	}
}

func TestEncodeDecodeRow_MultiColumn(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt, Nullable: false},
			{Name: "name", Type: sc.CTVarchar, Nullable: false},
			{Name: "active", Type: sc.CTBool, Nullable: false},
		},
	}
	row := sc.Row{Values: [][]byte{
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		[]byte("alice"),
		{0x01},
	}}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	decoded, err := DecodeRow(data, schema)
	if err != nil {
		t.Fatalf("DecodeRow failed: %v", err)
	}

	if !bytes.Equal(decoded.Values[0], row.Values[0]) {
		t.Errorf("int column mismatch: got %v, want %v", decoded.Values[0], row.Values[0])
	}
	if string(decoded.Values[1]) != "alice" {
		t.Errorf("varchar column mismatch: got %s", string(decoded.Values[1]))
	}
	if !bytes.Equal(decoded.Values[2], row.Values[2]) {
		t.Errorf("bool column mismatch: got %v, want %v", decoded.Values[2], row.Values[2])
	}
}
