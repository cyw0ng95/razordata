package id

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"sort"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
)

func TestEncodeKey_SingleColumn_Int(t *testing.T) {
	types := []sc.ColumnType{sc.CTInt}
	values := [][]byte{{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}}

	enc, err := EncodeKey(types, values)
	if err != nil {
		t.Fatalf("EncodeKey: %v", err)
	}
	got, err := DecodeKey(enc, types)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if !bytes.Equal(got[0], values[0]) {
		t.Errorf("round-trip mismatch: got %v, want %v", got[0], values[0])
	}
}

func TestEncodeKey_SingleColumn_AllTypes(t *testing.T) {
	types := []sc.ColumnType{
		sc.CTInt, sc.CTBigInt, sc.CTVarchar, sc.CTText,
		sc.CTBool, sc.CTFloat, sc.CTTimestamp, sc.CTBlob,
	}
	values := [][]byte{
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		[]byte("varchar"),
		[]byte("text"),
		{0x01},
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		{0xDE, 0xAD, 0xBE, 0xEF},
	}
	enc, err := EncodeKey(types, values)
	if err != nil {
		t.Fatalf("EncodeKey: %v", err)
	}
	got, err := DecodeKey(enc, types)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	for i, want := range values {
		if !bytes.Equal(got[i], want) {
			t.Errorf("column %d: got %v, want %v", i, got[i], want)
		}
	}
}

func TestEncodeKey_TwoColumn_IntText(t *testing.T) {
	types := []sc.ColumnType{sc.CTInt, sc.CTVarchar}
	values := [][]byte{
		{0x2A, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		[]byte("alice"),
	}
	enc, err := EncodeKey(types, values)
	if err != nil {
		t.Fatalf("EncodeKey: %v", err)
	}
	got, err := DecodeKey(enc, types)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if !bytes.Equal(got[0], values[0]) {
		t.Errorf("col 0: got %v, want %v", got[0], values[0])
	}
	if string(got[1]) != "alice" {
		t.Errorf("col 1: got %q, want %q", got[1], "alice")
	}
}

func TestEncodeKey_ThreeColumn_Composite(t *testing.T) {
	types := []sc.ColumnType{sc.CTInt, sc.CTVarchar, sc.CTBool}
	values := [][]byte{
		{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		[]byte("hello"),
		{0x01},
	}
	enc, err := EncodeKey(types, values)
	if err != nil {
		t.Fatalf("EncodeKey: %v", err)
	}
	got, err := DecodeKey(enc, types)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	for i, want := range values {
		if !bytes.Equal(got[i], want) {
			t.Errorf("col %d: got %v, want %v", i, got[i], want)
		}
	}
}

func TestEncodeKey_NullColumns(t *testing.T) {
	types := []sc.ColumnType{sc.CTInt, sc.CTVarchar, sc.CTInt}
	values := [][]byte{
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		nil, // NULL varchar
		nil, // NULL int
	}
	enc, err := EncodeKey(types, values)
	if err != nil {
		t.Fatalf("EncodeKey: %v", err)
	}
	got, err := DecodeKey(enc, types)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if !bytes.Equal(got[0], values[0]) {
		t.Errorf("col 0: got %v, want %v", got[0], values[0])
	}
	if got[1] != nil {
		t.Errorf("col 1: expected nil, got %v", got[1])
	}
	if got[2] != nil {
		t.Errorf("col 2: expected nil, got %v", got[2])
	}
}

func TestEncodeKey_ColumnCountMismatch(t *testing.T) {
	types := []sc.ColumnType{sc.CTInt, sc.CTInt}
	values := [][]byte{{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}}
	if _, err := EncodeKey(types, values); !errors.Is(err, ErrKeyCodecColumnCount) {
		t.Fatalf("expected ErrKeyCodecColumnCount, got %v", err)
	}
}

func TestEncodeKey_UnsupportedType(t *testing.T) {
	types := []sc.ColumnType{sc.ColumnType(99)}
	values := [][]byte{nil}
	if _, err := EncodeKey(types, values); !errors.Is(err, ErrKeyCodecTypeMismatch) {
		t.Fatalf("expected ErrKeyCodecTypeMismatch, got %v", err)
	}
}

func TestEncodeKey_DecodeTruncated(t *testing.T) {
	// Concrete tag (0x01) with no body bytes — truncated.
	types := []sc.ColumnType{sc.CTInt}
	if _, err := DecodeKey([]byte{0x01}, types); !errors.Is(err, ErrKeyCodecTruncated) {
		t.Fatalf("expected ErrKeyCodecTruncated, got %v", err)
	}
	// Empty input for a non-NULL column — truncated.
	if _, err := DecodeKey(nil, types); !errors.Is(err, ErrKeyCodecTruncated) {
		t.Fatalf("expected ErrKeyCodecTruncated for nil input, got %v", err)
	}
}

func TestEncodeKey_DecodeLengthOverflow(t *testing.T) {
	// Concrete tag + ord + length prefix that overflows the buffer.
	types := []sc.ColumnType{sc.CTInt}
	// tagConcrete (0x01), ord=0 (CTInt), length 100 (0x64 0x00),
	// no value bytes follow.
	encoded := []byte{0x01, 0x00, 0x64, 0x00}
	if _, err := DecodeKey(encoded, types); !errors.Is(err, ErrKeyCodecTruncated) {
		t.Fatalf("expected ErrKeyCodecTruncated, got %v", err)
	}
}

func TestEncodeKey_DecodeTypeMismatch(t *testing.T) {
	// Concrete tag for one type, but the caller asks for another.
	types := []sc.ColumnType{sc.CTBigInt}
	// tagConcrete (0x01), ord=0 (CTInt), length 0.
	encoded := []byte{0x01, 0x00, 0x00, 0x00}
	if _, err := DecodeKey(encoded, types); !errors.Is(err, ErrKeyCodecTypeMismatch) {
		t.Fatalf("expected ErrKeyCodecTypeMismatch, got %v", err)
	}
}

func TestEncodeKey_DecodeBadTag(t *testing.T) {
	// Unknown tag (0x99) — neither NULL nor concrete.
	types := []sc.ColumnType{sc.CTInt}
	encoded := []byte{0x99}
	if _, err := DecodeKey(encoded, types); !errors.Is(err, ErrKeyCodecTypeMismatch) {
		t.Fatalf("expected ErrKeyCodecTypeMismatch, got %v", err)
	}
}

func TestEncodeKey_DecodeTruncatedHeader(t *testing.T) {
	// Concrete tag but no ord/length bytes.
	types := []sc.ColumnType{sc.CTInt}
	encoded := []byte{0x01}
	if _, err := DecodeKey(encoded, types); !errors.Is(err, ErrKeyCodecTruncated) {
		t.Fatalf("expected ErrKeyCodecTruncated, got %v", err)
	}
}

func TestEncodeKey_ValueTooLong(t *testing.T) {
	// A value > 65535 bytes is rejected at encode time.
	types := []sc.ColumnType{sc.CTInt}
	val := make([]byte, 65536)
	if _, err := EncodeKey(types, [][]byte{val}); !errors.Is(err, ErrKeyCodecValueTooLong) {
		t.Fatalf("expected ErrKeyCodecValueTooLong, got %v", err)
	}
}

func TestEncodeKey_DecodeEmptyValue(t *testing.T) {
	// Concrete tag + ord + length 0 is valid (empty value, not NULL).
	types := []sc.ColumnType{sc.CTVarchar}
	// tagConcrete (0x01), ord=5 (CTVarchar), length 0.
	encoded := []byte{0x01, 0x05, 0x00, 0x00}
	got, err := DecodeKey(encoded, types)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if len(got[0]) != 0 || got[0] == nil {
		// Empty varchar is a zero-length slice, not nil.
		t.Errorf("expected empty (non-nil) slice, got %v", got[0])
	}
}

func TestEncodeKey_Order_IntThenText(t *testing.T) {
	// Two-column PK (int, text). Sort the encoded keys and verify
	// the typed values sort the same way. The int values are
	// encoded in big-endian byte order so the raw byte comparison
	// matches numeric comparison.
	type kv struct {
		intVal  int64
		text    string
		encoded []byte
	}
	rng := rand.New(rand.NewSource(42))
	pairs := make([]kv, 100)
	for i := range pairs {
		pairs[i] = kv{
			intVal: rng.Int63(),
			text:   randString(rng, 8),
		}
		types := []sc.ColumnType{sc.CTInt, sc.CTVarchar}
		intBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(intBytes, uint64(pairs[i].intVal))
		enc, err := EncodeKey(types, [][]byte{intBytes, []byte(pairs[i].text)})
		if err != nil {
			t.Fatalf("EncodeKey: %v", err)
		}
		pairs[i].encoded = enc
	}
	// Sort by encoded bytes.
	sort.Slice(pairs, func(i, j int) bool {
		return bytes.Compare(pairs[i].encoded, pairs[j].encoded) < 0
	})
	// Verify the same order is obtained by sorting the typed values.
	sorted := make([]kv, len(pairs))
	copy(sorted, pairs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].intVal != sorted[j].intVal {
			return sorted[i].intVal < sorted[j].intVal
		}
		return sorted[i].text < sorted[j].text
	})
	for i := range pairs {
		if pairs[i].intVal != sorted[i].intVal || pairs[i].text != sorted[i].text {
			t.Errorf("position %d: got (%d, %s), want (%d, %s)",
				i, pairs[i].intVal, pairs[i].text,
				sorted[i].intVal, sorted[i].text)
		}
	}
}

func TestEncodeKey_Order_TextOnly(t *testing.T) {
	// Single-column text PK. The codec sorts by (length, content),
	// not by natural string order. This test pins that behavior
	// down so future refactors don't quietly change it.
	type kv struct {
		text    string
		encoded []byte
	}
	inputs := []string{"", "a", "abc", "b", "hello", "world", "\x00", "\xff"}
	pairs := make([]kv, len(inputs))
	for i, s := range inputs {
		enc, err := EncodeKey([]sc.ColumnType{sc.CTVarchar}, [][]byte{[]byte(s)})
		if err != nil {
			t.Fatalf("EncodeKey: %v", err)
		}
		pairs[i] = kv{text: s, encoded: enc}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return bytes.Compare(pairs[i].encoded, pairs[j].encoded) < 0
	})
	// The expected order is: by length, then by content.
	// Lengths: 0(""), 1("a", "\x00", "\xff"), 3("abc"), 5("hello", "world").
	// Within length 1, byte order: "\x00" < "a" < "b" ... wait, no
	// "\xff" — sort: 0x00, 0x61, 0x62, 0xff.
	wantOrder := []string{"", "\x00", "a", "b", "\xff", "abc", "hello", "world"}
	for i, p := range pairs {
		if p.text != wantOrder[i] {
			t.Errorf("position %d: got %q, want %q", i, p.text, wantOrder[i])
		}
	}
}

func TestEncodeKey_Order_NullVsConcrete(t *testing.T) {
	// Single-column PK; one entry is NULL, one is concrete. The
	// NULL entry must sort before the concrete entry.
	types := []sc.ColumnType{sc.CTInt}
	nullEnc, _ := EncodeKey(types, [][]byte{nil})
	concEnc, _ := EncodeKey(types, [][]byte{{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}})
	if bytes.Compare(nullEnc, concEnc) >= 0 {
		t.Errorf("NULL should sort before concrete, got nullEnc=%x concEnc=%x", nullEnc, concEnc)
	}
}

func TestEncodeKey_Order_MixedTypes(t *testing.T) {
	// Single-column PKs of different types. Order must be:
	// CTInt < CTBigInt < CTFloat < CTBool < CTTimestamp <
	// CTVarchar < CTText < CTBlob.
	types := []sc.ColumnType{
		sc.CTInt, sc.CTBigInt, sc.CTFloat, sc.CTBool, sc.CTTimestamp,
		sc.CTVarchar, sc.CTText, sc.CTBlob,
	}
	encs := make([][]byte, len(types))
	for i, ty := range types {
		var val []byte
		switch ty {
		case sc.CTBool:
			val = []byte{0x00}
		case sc.CTVarchar, sc.CTText, sc.CTBlob:
			val = []byte("x")
		default:
			val = []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		}
		enc, err := EncodeKey([]sc.ColumnType{ty}, [][]byte{val})
		if err != nil {
			t.Fatalf("EncodeKey(%d): %v", i, err)
		}
		encs[i] = enc
	}
	for i := 0; i < len(encs)-1; i++ {
		if bytes.Compare(encs[i], encs[i+1]) >= 0 {
			t.Errorf("type order wrong at position %d: %x !< %x", i, encs[i], encs[i+1])
		}
	}
}

func TestEncodeKey_VarintLengthBoundaries(t *testing.T) {
	// 1-byte varint (len 0..127), 2-byte varint (len 128..16383)
	types := []sc.ColumnType{sc.CTBlob}
	for _, l := range []int{0, 1, 127, 128, 1024, 16383, 16384} {
		val := make([]byte, l)
		for i := range val {
			val[i] = byte(i)
		}
		enc, err := EncodeKey(types, [][]byte{val})
		if err != nil {
			t.Fatalf("EncodeKey len=%d: %v", l, err)
		}
		got, err := DecodeKey(enc, types)
		if err != nil {
			t.Fatalf("DecodeKey len=%d: %v", l, err)
		}
		if len(got[0]) != l {
			t.Errorf("len=%d: round-trip got %d bytes", l, len(got[0]))
		}
	}
}

func TestEncodeKey_DecodeZeroColumns(t *testing.T) {
	got, err := DecodeKey(nil, nil)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d entries", len(got))
	}
}

func TestEncodeKey_Property_RoundTripFuzz(t *testing.T) {
	// Generate 1000 random composite keys and verify round-trip.
	rng := rand.New(rand.NewSource(7))
	types := []sc.ColumnType{sc.CTInt, sc.CTVarchar, sc.CTBool}
	for i := 0; i < 1000; i++ {
		intVal := make([]byte, 8)
		binaryPutUint64(intVal, uint64(rng.Int63()))
		textVal := []byte(randString(rng, rng.Intn(32)))
		var boolVal []byte
		if rng.Intn(2) == 0 {
			boolVal = []byte{0x00}
		} else {
			boolVal = []byte{0x01}
		}
		values := [][]byte{intVal, textVal, boolVal}
		enc, err := EncodeKey(types, values)
		if err != nil {
			t.Fatalf("EncodeKey iter=%d: %v", i, err)
		}
		got, err := DecodeKey(enc, types)
		if err != nil {
			t.Fatalf("DecodeKey iter=%d: %v", i, err)
		}
		if !bytes.Equal(got[0], intVal) || !bytes.Equal(got[1], textVal) || !bytes.Equal(got[2], boolVal) {
			t.Errorf("iter %d: round-trip mismatch", i)
		}
	}
}

func randString(rng *rand.Rand, n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(out)
}

func binaryPutUint64(buf []byte, v uint64) {
	_ = buf[7] // bounds check
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
	buf[3] = byte(v >> 24)
	buf[4] = byte(v >> 32)
	buf[5] = byte(v >> 40)
	buf[6] = byte(v >> 48)
	buf[7] = byte(v >> 56)
}
