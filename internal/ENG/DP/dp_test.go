package dp

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func makeTestSchema() *sc.TableSchema {
	return &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt},
			{Name: "name", Type: sc.CTVarchar},
			{Name: "salary", Type: sc.CTFloat},
			{Name: "active", Type: sc.CTBool},
			{Name: "bio", Type: sc.CTText},
			{Name: "avatar", Type: sc.CTBlob},
			{Name: "created", Type: sc.CTTimestamp},
			{Name: "big", Type: sc.CTBigInt},
		},
	}
}

func int64ptr(v int64) []byte  { return sc.EncodeInt(v) }
func float64ptr(v float64) []byte { return sc.EncodeFloat(v) }
func boolptr(v bool) []byte       { return sc.EncodeBool(v) }

func TestEncodeDecodeRowRoundTrip(t *testing.T) {
	schema := makeTestSchema()
	row := sc.Row{
		Values: [][]byte{
			int64ptr(42),
			[]byte("alice"),
			float64ptr(123.45),
			boolptr(true),
			[]byte("hello world"),
			[]byte{0xDE, 0xAD, 0xBE, 0xEF},
			int64ptr(1000000),
			int64ptr(999),
		},
	}

	data, err := EncodeRow(row, schema)
	if err != nil {
		t.Fatalf("EncodeRow: %v", err)
	}
	decoded, err := DecodeRow(data, schema)
	if err != nil {
		t.Fatalf("DecodeRow: %v", err)
	}
	for i, v := range decoded.Values {
		expected := row.Values[i]
		if len(v) != len(expected) {
			t.Errorf("column %d: len=%d, want %d", i, len(v), len(expected))
			continue
		}
		for j := range v {
			if v[j] != expected[j] {
				t.Errorf("column %d byte %d: got %d, want %d", i, j, v[j], expected[j])
			}
		}
	}
}

func TestEncodeDecodeRowMultiRow(t *testing.T) {
	schema := makeTestSchema()
	rows := []sc.Row{
		{Values: [][]byte{int64ptr(1), []byte("a"), float64ptr(1.1), boolptr(true), []byte("x"), []byte{0x01}, int64ptr(100), int64ptr(10)}},
		{Values: [][]byte{int64ptr(2), []byte("bb"), float64ptr(2.2), boolptr(false), []byte("yy"), []byte{0x02, 0x03}, int64ptr(200), int64ptr(20)}},
		{Values: [][]byte{int64ptr(3), []byte("ccc"), float64ptr(3.3), boolptr(true), []byte("zzz"), []byte{0x04, 0x05, 0x06}, int64ptr(300), int64ptr(30)}},
	}
	for i, row := range rows {
		data, err := EncodeRow(row, schema)
		if err != nil {
			t.Fatalf("row %d EncodeRow: %v", i, err)
		}
		decoded, err := DecodeRow(data, schema)
		if err != nil {
			t.Fatalf("row %d DecodeRow: %v", i, err)
		}
		for j := range decoded.Values {
			expected := row.Values[j]
			got := decoded.Values[j]
			if len(got) != len(expected) {
				t.Errorf("row %d col %d: len=%d, want %d", i, j, len(got), len(expected))
				continue
			}
			for k := range got {
				if got[k] != expected[k] {
					t.Errorf("row %d col %d byte %d: got %d, want %d", i, j, k, got[k], expected[k])
				}
			}
		}
	}
}

func TestEncodeDecodeRowEmptyAndNil(t *testing.T) {
	emptySchema := &sc.TableSchema{Columns: nil}
	data, err := EncodeRow(sc.Row{}, emptySchema)
	if err != nil {
		t.Fatalf("EncodeRow empty schema: %v", err)
	}
	decoded, err := DecodeRow(data, emptySchema)
	if err != nil {
		t.Fatalf("DecodeRow empty schema: %v", err)
	}
	if len(decoded.Values) != 0 {
		t.Errorf("len=%d, want 0", len(decoded.Values))
	}

	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "a", Type: sc.CTInt},
			{Name: "b", Type: sc.CTVarchar},
		},
	}
	nilRow := sc.Row{Values: [][]byte{nil, nil}}
	data2, err := EncodeRow(nilRow, schema)
	if err != nil {
		t.Fatalf("EncodeRow nil values: %v", err)
	}
	decoded2, err := DecodeRow(data2, schema)
	if err != nil {
		t.Fatalf("DecodeRow nil values: %v", err)
	}
	if decoded2.Values[0] != nil {
		t.Errorf("col 0: got non-nil, want nil")
	}
	if decoded2.Values[1] != nil {
		t.Errorf("col 1: got non-nil, want nil")
	}
}

func TestEncodeDecodeBlockZeroRows(t *testing.T) {
	kvs, restarts, err := DecodeBlock(nil)
	if err != nil {
		t.Fatalf("DecodeBlock(nil): %v", err)
	}
	if len(kvs) != 0 {
		t.Errorf("kvs len=%d, want 0", len(kvs))
	}
	if restarts != nil {
		t.Errorf("restarts=%v, want nil", restarts)
	}

	data, err := EncodeBlock(nil, 16)
	if err != nil {
		t.Fatalf("EncodeBlock(nil): %v", err)
	}
	if data != nil {
		t.Errorf("expected nil data for nil input")
	}

	data2, err := EncodeBlock([]KV{}, 16)
	if err != nil {
		t.Fatalf("EncodeBlock(empty): %v", err)
	}
	if data2 != nil {
		t.Errorf("expected nil data for empty input")
	}
}

func TestEncodeDecodeBlockRoundTrip(t *testing.T) {
	kvs := []KV{
		{Key: []byte("key1"), Value: []byte("val1")},
		{Key: []byte("key2_longer"), Value: []byte("val2")},
		{Key: []byte("k3"), Value: []byte("value3_very_long")},
	}
	intervals := []int{1, 2, 3, 10}
	for _, interval := range intervals {
		data, err := EncodeBlock(kvs, interval)
		if err != nil {
			t.Fatalf("EncodeBlock(interval=%d): %v", interval, err)
		}
		decoded, restarts, err := DecodeBlock(data)
		if err != nil {
			t.Fatalf("DecodeBlock(interval=%d): %v", interval, err)
		}
		if len(decoded) != len(kvs) {
			t.Fatalf("interval=%d: decoded len=%d, want %d", interval, len(decoded), len(kvs))
		}
		for i := range decoded {
			if string(decoded[i].Key) != string(kvs[i].Key) {
				t.Errorf("interval=%d idx=%d: key mismatch", interval, i)
			}
			if string(decoded[i].Value) != string(kvs[i].Value) {
				t.Errorf("interval=%d idx=%d: value mismatch", interval, i)
			}
		}
		if len(restarts) == 0 {
			t.Errorf("interval=%d: expected at least one restart point", interval)
		}
	}
}

func TestEncodeUint64(t *testing.T) {
	cases := []struct {
		v    uint64
		want int
	}{
		{0, 1},
		{1, 1},
		{127, 1},
		{128, 2},
		{16383, 2},
		{16384, 3},
		{math.MaxUint64, 10},
	}
	for _, c := range cases {
		data := EncodeUint64(c.v)
		if len(data) != c.want {
			t.Errorf("EncodeUint64(%d): len=%d, want %d", c.v, len(data), c.want)
		}
		decoded, n := DecodeUint64(data)
		if decoded != c.v {
			t.Errorf("DecodeUint64(EncodeUint64(%d)): got %d", c.v, decoded)
		}
		if n != c.want {
			t.Errorf("DecodeUint64(EncodeUint64(%d)): n=%d, want %d", c.v, n, c.want)
		}
	}
}

func TestDecodeCorruptData(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "id", Type: sc.CTInt},
			{Name: "name", Type: sc.CTVarchar},
		},
	}

	// Truncated: only null bitmap, missing column data.
	_, err := DecodeRow([]byte{0x00}, schema)
	if err == nil {
		t.Error("expected error for truncated row data")
	}

	// Nil data.
	_, err = DecodeRow(nil, schema)
	if err == nil {
		t.Error("expected error for nil row data")
	}

	// Empty data.
	_, err = DecodeRow([]byte{}, schema)
	if err == nil {
		t.Error("expected error for empty row data")
	}

	// Truncated block without restart footer.
	_, _, err = DecodeBlock([]byte{0x01})
	if err == nil {
		t.Error("expected error for truncated block")
	}

	// Corrupt restart count.
	data := []byte{0x01, 0x02, 0x03, 0x04, 0xFF, 0xFF, 0xFF, 0xFF, 0x05, 0x00, 0x00, 0x00}
	_, _, err = DecodeBlock(data)
	if err == nil {
		t.Error("expected error for corrupt block")
	}
}

func TestConcurrentEncodeDecode(t *testing.T) {
	schema := makeTestSchema()
	row := sc.Row{
		Values: [][]byte{
			int64ptr(1),
			[]byte("concurrent"),
			float64ptr(99.9),
			boolptr(true),
			[]byte("bio"),
			[]byte{0xAB},
			int64ptr(123),
			int64ptr(456),
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := EncodeRow(row, schema)
			if err != nil {
				t.Errorf("EncodeRow: %v", err)
				return
			}
			if _, err := DecodeRow(data, schema); err != nil {
				t.Errorf("DecodeRow: %v", err)
			}
		}()
	}

	kvs := []KV{{Key: []byte("k1"), Value: []byte("v1")}}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := EncodeBlock(kvs, 1)
			if err != nil {
				t.Errorf("EncodeBlock: %v", err)
				return
			}
			if _, _, err := DecodeBlock(data); err != nil {
				t.Errorf("DecodeBlock: %v", err)
			}
		}()
	}

	wg.Wait()
}

func TestPropertyRandomRows(t *testing.T) {
	schema := makeTestSchema()
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 100; i++ {
		values := make([][]byte, len(schema.Columns))
		for j, col := range schema.Columns {
			if rng.Intn(4) == 0 {
				values[j] = nil
				continue
			}
			switch col.Type {
			case sc.CTInt, sc.CTBigInt, sc.CTTimestamp:
				b := make([]byte, 8)
				rng.Read(b)
				values[j] = b
			case sc.CTFloat:
				b := make([]byte, 8)
				rng.Read(b)
				values[j] = b
			case sc.CTBool:
				if rng.Intn(2) == 0 {
					values[j] = []byte{0}
				} else {
					values[j] = []byte{1}
				}
			case sc.CTVarchar, sc.CTText, sc.CTBlob:
				l := rng.Intn(256)
				b := make([]byte, l)
				rng.Read(b)
				values[j] = b
			}
		}

		row := sc.Row{Values: values}
		data, err := EncodeRow(row, schema)
		if err != nil {
			t.Fatalf("iteration %d EncodeRow: %v", i, err)
		}
		decoded, err := DecodeRow(data, schema)
		if err != nil {
			t.Fatalf("iteration %d DecodeRow: %v", i, err)
		}
		for j := range schema.Columns {
			expected := row.Values[j]
			got := decoded.Values[j]
			if expected == nil && got == nil {
				continue
			}
			if (expected == nil) != (got == nil) {
				t.Errorf("iteration %d col %d: nil mismatch", i, j)
				continue
			}
			if len(got) != len(expected) {
				t.Errorf("iteration %d col %d: len=%d, want %d", i, j, len(got), len(expected))
				continue
			}
			for k := range got {
				if got[k] != expected[k] {
					t.Errorf("iteration %d col %d byte %d: got %d, want %d", i, j, k, got[k], expected[k])
				}
			}
		}
	}
}

func TestEncodeRowMismatchedSchema(t *testing.T) {
	schema := &sc.TableSchema{
		Columns: []sc.ColumnDef{
			{Name: "a", Type: sc.CTInt},
		},
	}
	row := sc.Row{Values: [][]byte{int64ptr(1), []byte("extra")}}
	_, err := EncodeRow(row, schema)
	if err == nil {
		t.Error("expected error for row/schema column mismatch")
	}
}

func TestEncodeDecodeBlockLargeKeyValues(t *testing.T) {
	largeKey := make([]byte, 65535)
	largeVal := make([]byte, 65535)
	for i := range largeKey {
		largeKey[i] = byte(i)
		largeVal[i] = byte(i ^ 0xFF)
	}
	kvs := []KV{
		{Key: largeKey, Value: largeVal},
		{Key: []byte("small"), Value: []byte("pair")},
	}

	data, err := EncodeBlock(kvs, 1)
	if err != nil {
		t.Fatalf("EncodeBlock: %v", err)
	}
	decoded, _, err := DecodeBlock(data)
	if err != nil {
		t.Fatalf("DecodeBlock: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("len=%d, want 2", len(decoded))
	}
	if string(decoded[1].Key) != "small" {
		t.Errorf("second key mismatch")
	}
	if len(decoded[0].Key) != 65535 {
		t.Errorf("large key len=%d, want 65535", len(decoded[0].Key))
	}
}

func TestDecodeBlockCorruptSizes(t *testing.T) {
	kvs := []KV{{Key: []byte("k"), Value: []byte("v")}}
	data, _ := EncodeBlock(kvs, 1)

	// Truncate before key length prefix.
	_, _, err := DecodeBlock(data[:1])
	if err == nil {
		t.Error("expected error for severely truncated block")
	}

	// Mutate to a massive restart count.
	mut := make([]byte, len(data))
	copy(mut, data)
	mut[len(mut)-1] = 255
	_, _, err = DecodeBlock(mut)
	if err == nil {
		t.Error("expected error for corrupt restart count")
	}
}
