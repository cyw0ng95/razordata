package DT

import (
	"fmt"
	"testing"

	CT "github.com/cyw0ng95/razordata/internal/SYS/CT"
)

// dtDummyTypeDesc is a fictional extension type used to prove REQ002287 at the
// storage layer: a new type is storable/readable through the TypeDesc registry
// dispatch in EncodeRow/DecodeRow without editing those switches. This is a
// separate test binary from the SYS/CT test, so reusing the extension kind is
// fine.
type dtDummyTypeDesc struct{}

func (dtDummyTypeDesc) Name() string    { return "dt_dummycolor" }
func (dtDummyTypeDesc) Kind() ValueKind { return CT.ValueKind(33) }

func (dtDummyTypeDesc) Validate(v Value) error {
	if v.Kind != CT.ValueKind(33) {
		return fmt.Errorf("dt_dummycolor: kind %d", v.Kind)
	}
	return nil
}
func (dtDummyTypeDesc) Compare(a, b Value) int {
	if a.S < b.S {
		return -1
	}
	if a.S > b.S {
		return 1
	}
	return 0
}
func (dtDummyTypeDesc) Hash(v Value) uint64 { return uint64(len(v.S)) }
func (dtDummyTypeDesc) Encode(v Value) ([]byte, error) {
	return []byte(v.S), nil
}
func (dtDummyTypeDesc) Decode(b []byte) (Value, error) {
	return Value{Kind: CT.ValueKind(33), S: string(b)}, nil
}
func (dtDummyTypeDesc) ParseLiteral(s string) (Value, error) {
	return Value{Kind: CT.ValueKind(33), S: s}, nil
}
func (dtDummyTypeDesc) ScalarEval(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("dt_dummycolor: needs arg")
	}
	return args[0], nil
}

func init() {
	CT.RegisterType(dtDummyTypeDesc{})
}

// TestREQ002287_RowStoreReadRoundTrip proves an extension-type Value survives
// EncodeRow -> DecodeRow (the storage path) purely via the registry dispatch,
// touching zero pre-existing `switch v.Kind` sites.
func TestREQ002287_RowStoreReadRoundTrip(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"id", "color"},
		ColIndex: map[string]int{"id": 0, "color": 1},
	}
	row := Row{
		Cols: schema.Cols,
		Data: []Value{
			NewIntValue(7),
			{Kind: CT.ValueKind(33), S: "emerald"},
		},
	}

	enc, err := EncodeRow(schema, row)
	if err != nil {
		t.Fatalf("EncodeRow: %v", err)
	}
	dec, err := DecodeRow(enc, schema)
	if err != nil {
		t.Fatalf("DecodeRow: %v", err)
	}
	if len(dec.Data) != 2 {
		t.Fatalf("decoded %d cols, want 2", len(dec.Data))
	}
	if dec.Data[0].Kind != KindInt || dec.Data[0].I64 != 7 {
		t.Fatalf("int col corrupted: %+v", dec.Data[0])
	}
	if dec.Data[1].Kind != CT.ValueKind(33) || dec.Data[1].S != "emerald" {
		t.Fatalf("extension col corrupted: %+v", dec.Data[1])
	}
	if !dec.Data[1].Equal(row.Data[1]) {
		t.Fatalf("extension value not equal after round-trip")
	}
}
