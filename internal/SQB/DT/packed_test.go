package DT

import (
	"encoding/binary"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestPackedRow_IntOnly(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c"},
		ColTypes: []LX.TokenType{LX.TokenType(KindInt), LX.TokenType(KindInt), LX.TokenType(KindInt)},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2},
	}

	row := Row{
		Cols:     schema.Cols,
		ColIndex: schema.ColIndex,
		Data: []Value{
			NewIntValue(1),
			NullValue(),
			NewIntValue(3),
		},
	}

	packed := PackRow(schema, row)
	if packed == nil {
		t.Fatal("PackRow returned nil for int-only schema")
	}
	if len(packed) != 27 {
		t.Fatalf("packed length = %d, want 27", len(packed))
	}
	if packed[9] != nullTag {
		t.Errorf("column b null tag = %d, want 0", packed[9])
	}
}

func TestPackedRow_CanPack(t *testing.T) {
	tests := []struct {
		name   string
		types  []LX.TokenType
		wantOK bool
	}{
		{"int-only", []LX.TokenType{LX.TokenType(KindInt), LX.TokenType(KindInt), LX.TokenType(KindInt)}, true},
		{"float-only", []LX.TokenType{LX.TokenType(KindFloat), LX.TokenType(KindFloat)}, true},
		{"bool-only", []LX.TokenType{LX.TokenType(KindBool), LX.TokenType(KindBool)}, true},
		{"mixed", []LX.TokenType{LX.TokenType(KindInt), LX.TokenType(KindText)}, false},
		{"single-int", []LX.TokenType{LX.TokenType(KindInt)}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema := &StoreSchema{
				Cols:     make([]string, len(tc.types)),
				ColTypes: make([]LX.TokenType, len(tc.types)),
				ColIndex: make(map[string]int),
			}
			for i := range tc.types {
				schema.Cols[i] = "c"
				schema.ColTypes[i] = tc.types[i]
				schema.ColIndex["c"] = i
			}
			if got := schema.CanPack(); got != tc.wantOK {
				t.Errorf("CanPack() = %v, want %v", got, tc.wantOK)
			}
		})
	}
}

func TestUnpackRowInto_Int(t *testing.T) {
	schema := &StoreSchema{
		Cols:     []string{"x", "y"},
		ColTypes: []LX.TokenType{LX.TokenType(KindInt), LX.TokenType(KindInt)},
		ColIndex: map[string]int{"x": 0, "y": 1},
	}

	buf := make([]byte, 18)
	buf[0] = 1
	binary.BigEndian.PutUint64(buf[1:9], uint64(42))
	buf[9] = nullTag
	buf[17] = 1
	binary.BigEndian.PutUint64(buf[10:18], uint64(100))

	arena := &RowArena{}
	row := arena.AllocRow(2, schema)
	err := UnpackRowInto(row, buf, schema)
	if err != nil {
		t.Fatalf("UnpackRowInto: %v", err)
	}

	if row.Data[0].Kind != KindInt || row.Data[0].I64 != 42 {
		t.Errorf("x = %v, want 42", row.Data[0])
	}
	if row.Data[1].Kind != KindNull {
		t.Errorf("y = %v, want NULL", row.Data[1])
	}
	arena.Reset()
}

func BenchmarkPackedRow_Decode(b *testing.B) {
	schema := &StoreSchema{
		Cols:     []string{"a", "b", "c", "d", "e"},
		ColTypes: []LX.TokenType{LX.TokenType(KindInt), LX.TokenType(KindInt), LX.TokenType(KindInt), LX.TokenType(KindInt), LX.TokenType(KindInt)},
		ColIndex: map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4},
	}

	buf := make([]byte, 45)
	for i := 0; i < 5; i++ {
		off := i * 9
		buf[off] = 1
		binary.BigEndian.PutUint64(buf[off+1:off+9], uint64(i*1000))
	}

	b.Run("ValueSlice", func(b *testing.B) {
		arena := &RowArena{}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			row := arena.AllocRow(5, schema)
			for j := 0; j < 5; j++ {
				row.Data[j] = NewIntValue(int64(j * 1000))
			}
			if i%100 == 0 {
				arena.Reset()
			}
		}
		arena.Reset()
	})

	b.Run("PackedRow", func(b *testing.B) {
		arena := &RowArena{}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			row := arena.AllocRow(5, schema)
			err := UnpackRowInto(row, buf, schema)
			if err != nil {
				arena.Reset()
				b.Fatal(err)
			}
		}
		arena.Reset()
	})
}
