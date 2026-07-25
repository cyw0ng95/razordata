package OP

import (
	"bytes"
	"os"
	"strings"
	"testing"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// roundTripSpill writes a batch to a buffer and reads it back,
// returning the decoded batch. Caller owns the returned batch.
func roundTripSpill(t *testing.T, b *UT.Batch) *UT.Batch {
	t.Helper()
	var buf bytes.Buffer
	if err := writeBatch(&buf, b); err != nil {
		t.Fatalf("writeBatch: %v", err)
	}
	got, err := readBatch(&buf)
	if err != nil {
		t.Fatalf("readBatch: %v", err)
	}
	if got == nil {
		t.Fatal("readBatch returned nil, expected a batch")
	}
	return got
}

// TestSpillRoundTrip_IntColumn verifies INT column round-trip. REQ001999.
func TestSpillRoundTrip_IntColumn(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "k")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{1, 2, 3, -42, 9223372036854775807}
	b.Size = 5

	got := roundTripSpill(t, b)
	defer got.Put()
	if got.Size != 5 {
		t.Fatalf("Size: got %d, want 5", got.Size)
	}
	for i, want := range b.Cols[0].Data.Ints {
		if got.Cols[0].Data.Ints[i] != want {
			t.Errorf("row %d: got %d, want %d", i, got.Cols[0].Data.Ints[i], want)
		}
	}
}

// TestSpillRoundTrip_BigIntColumn verifies T_BIGINT round-trip. REQ001999.
func TestSpillRoundTrip_BigIntColumn(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "k")
	b.Cols[0].Type = LX.T_BIGINT
	b.Cols[0].Data.Ints = []int64{1, -1, 0}
	b.Size = 3

	got := roundTripSpill(t, b)
	defer got.Put()
	if got.Cols[0].Type != LX.T_BIGINT {
		t.Errorf("Type: got %v, want T_BIGINT", got.Cols[0].Type)
	}
}

// TestSpillRoundTrip_FloatColumn verifies float64 precision. REQ001999.
func TestSpillRoundTrip_FloatColumn(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "f")
	b.Cols[0].Type = LX.T_FLOAT_KW
	b.Cols[0].Data.Floats = []float64{3.14, -0.0, 1e100, 1.7976931348623157e308}
	b.Size = 4

	got := roundTripSpill(t, b)
	defer got.Put()
	for i, want := range b.Cols[0].Data.Floats {
		if got.Cols[0].Data.Floats[i] != want {
			t.Errorf("row %d: got %g, want %g", i, got.Cols[0].Data.Floats[i], want)
		}
	}
}

// TestSpillRoundTrip_BoolColumn verifies bool round-trip. REQ001999.
func TestSpillRoundTrip_BoolColumn(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "b")
	b.Cols[0].Type = LX.T_BOOL
	b.Cols[0].Data.Bools = []bool{true, false, true, true, false}
	b.Size = 5

	got := roundTripSpill(t, b)
	defer got.Put()
	for i, want := range b.Cols[0].Data.Bools {
		if got.Cols[0].Data.Bools[i] != want {
			t.Errorf("row %d: got %v, want %v", i, got.Cols[0].Data.Bools[i], want)
		}
	}
}

// TestSpillRoundTrip_TextColumn verifies string round-trip including
// empty, unicode, and long strings. REQ001999.
func TestSpillRoundTrip_TextColumn(t *testing.T) {
	long := strings.Repeat("x", 10000)
	b := UT.GetBatch(1)
	b.SetColumnName(0, "s")
	b.Cols[0].Type = LX.T_TEXT
	b.Cols[0].Data.Strs = []string{"hello", "", "héllo世界", long, "\x00\x01\x02"}
	b.Size = 5

	got := roundTripSpill(t, b)
	defer got.Put()
	for i, want := range b.Cols[0].Data.Strs {
		if got.Cols[0].Data.Strs[i] != want {
			t.Errorf("row %d: got %q, want %q", i, got.Cols[0].Data.Strs[i], want)
		}
	}
}

// TestSpillRoundTrip_VarcharColumn verifies T_VARCHAR round-trip. REQ001999.
func TestSpillRoundTrip_VarcharColumn(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "v")
	b.Cols[0].Type = LX.T_VARCHAR
	b.Cols[0].Data.Strs = []string{"a", "bb"}
	b.Size = 2

	got := roundTripSpill(t, b)
	defer got.Put()
	if got.Cols[0].Type != LX.T_VARCHAR {
		t.Errorf("Type: got %v, want T_VARCHAR", got.Cols[0].Type)
	}
}

// TestSpillRoundTrip_BlobColumn verifies T_BLOB with binary bytes. REQ001999.
func TestSpillRoundTrip_BlobColumn(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "bl")
	b.Cols[0].Type = LX.T_BLOB
	b.Cols[0].Data.Strs = []string{"\x00\x00\x00", "\xFF\xFE\xFD"}
	b.Size = 2

	got := roundTripSpill(t, b)
	defer got.Put()
	for i, want := range b.Cols[0].Data.Strs {
		if got.Cols[0].Data.Strs[i] != want {
			t.Errorf("row %d: got %q, want %q", i, got.Cols[0].Data.Strs[i], want)
		}
	}
}

// TestSpillRoundTrip_NullsBitmap verifies mixed null/non-null. REQ001999.
func TestSpillRoundTrip_NullsBitmap(t *testing.T) {
	b := UT.GetBatch(2)
	b.SetColumnName(0, "k")
	b.SetColumnName(1, "v")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{1, 0, 3, 0, 5}
	b.Cols[0].Nulls = []bool{false, true, false, true, false}
	b.Cols[1].Type = LX.T_TEXT
	b.Cols[1].Data.Strs = []string{"a", "", "c", "", "e"}
	b.Cols[1].Nulls = []bool{false, false, false, true, false}
	b.Size = 5

	got := roundTripSpill(t, b)
	defer got.Put()
	for i := 0; i < 5; i++ {
		if got.Cols[0].Nulls[i] != b.Cols[0].Nulls[i] {
			t.Errorf("col0 nulls[%d]: got %v, want %v", i, got.Cols[0].Nulls[i], b.Cols[0].Nulls[i])
		}
		if got.Cols[1].Nulls[i] != b.Cols[1].Nulls[i] {
			t.Errorf("col1 nulls[%d]: got %v, want %v", i, got.Cols[1].Nulls[i], b.Cols[1].Nulls[i])
		}
	}
}

// TestSpillRoundTrip_MultiColumnMixedTypes verifies a batch with
// int, float, text, bool columns. REQ001999.
func TestSpillRoundTrip_MultiColumnMixedTypes(t *testing.T) {
	b := UT.GetBatch(4)
	b.SetColumnName(0, "i")
	b.SetColumnName(1, "f")
	b.SetColumnName(2, "s")
	b.SetColumnName(3, "b")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{10, 20}
	b.Cols[1].Type = LX.T_FLOAT_KW
	b.Cols[1].Data.Floats = []float64{1.5, 2.5}
	b.Cols[2].Type = LX.T_TEXT
	b.Cols[2].Data.Strs = []string{"foo", "bar"}
	b.Cols[3].Type = LX.T_BOOL
	b.Cols[3].Data.Bools = []bool{true, false}
	b.Size = 2

	got := roundTripSpill(t, b)
	defer got.Put()
	if got.Size != 2 {
		t.Fatalf("Size: got %d, want 2", got.Size)
	}
	if got.Cols[0].Data.Ints[1] != 20 {
		t.Errorf("int[1]: got %d, want 20", got.Cols[0].Data.Ints[1])
	}
	if got.Cols[1].Data.Floats[0] != 1.5 {
		t.Errorf("float[0]: got %g, want 1.5", got.Cols[1].Data.Floats[0])
	}
	if got.Cols[2].Data.Strs[1] != "bar" {
		t.Errorf("str[1]: got %q, want bar", got.Cols[2].Data.Strs[1])
	}
	if got.Cols[3].Data.Bools[0] != true {
		t.Errorf("bool[0]: got %v, want true", got.Cols[3].Data.Bools[0])
	}
}

// TestSpillRoundTrip_EmptyBatch verifies a 0-row batch. REQ001999.
func TestSpillRoundTrip_EmptyBatch(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "k")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{}
	b.Size = 0

	got := roundTripSpill(t, b)
	defer got.Put()
	if got.Size != 0 {
		t.Fatalf("Size: got %d, want 0", got.Size)
	}
}

// TestSpillRoundTrip_FullBatch verifies a 1024-row batch (BatchSize). REQ001999.
func TestSpillRoundTrip_FullBatch(t *testing.T) {
	n := UT.BatchSize
	b := UT.GetBatch(1)
	b.SetColumnName(0, "k")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = make([]int64, n)
	for i := 0; i < n; i++ {
		b.Cols[0].Data.Ints[i] = int64(i)
	}
	b.Size = n

	got := roundTripSpill(t, b)
	defer got.Put()
	if got.Size != n {
		t.Fatalf("Size: got %d, want %d", got.Size, n)
	}
	for i := 0; i < n; i++ {
		if got.Cols[0].Data.Ints[i] != int64(i) {
			t.Fatalf("row %d: got %d, want %d", i, got.Cols[0].Data.Ints[i], i)
		}
	}
}

// TestSpillRoundTrip_MultiBatch verifies two batches in one stream. REQ001999.
func TestSpillRoundTrip_MultiBatch(t *testing.T) {
	b1 := UT.GetBatch(1)
	b1.SetColumnName(0, "k")
	b1.Cols[0].Type = LX.T_INT_KW
	b1.Cols[0].Data.Ints = []int64{1, 2}
	b1.Size = 2

	b2 := UT.GetBatch(1)
	b2.SetColumnName(0, "k")
	b2.Cols[0].Type = LX.T_INT_KW
	b2.Cols[0].Data.Ints = []int64{3, 4}
	b2.Size = 2

	var buf bytes.Buffer
	if err := writeBatch(&buf, b1); err != nil {
		t.Fatalf("writeBatch 1: %v", err)
	}
	if err := writeBatch(&buf, b2); err != nil {
		t.Fatalf("writeBatch 2: %v", err)
	}

	got1, err := readBatch(&buf)
	if err != nil {
		t.Fatalf("readBatch 1: %v", err)
	}
	defer got1.Put()
	if got1.Size != 2 || got1.Cols[0].Data.Ints[1] != 2 {
		t.Fatalf("batch 1: got Size=%d val=%d, want Size=2 val=2", got1.Size, got1.Cols[0].Data.Ints[1])
	}

	got2, err := readBatch(&buf)
	if err != nil {
		t.Fatalf("readBatch 2: %v", err)
	}
	defer got2.Put()
	if got2.Size != 2 || got2.Cols[0].Data.Ints[1] != 4 {
		t.Fatalf("batch 2: got Size=%d val=%d, want Size=2 val=4", got2.Size, got2.Cols[0].Data.Ints[1])
	}

	// EOF.
	got3, err := readBatch(&buf)
	if err != nil {
		t.Fatalf("readBatch 3 (EOF): %v", err)
	}
	if got3 != nil {
		t.Fatal("expected nil at EOF")
		got3.Put()
	}
}

// TestSpillRoundTrip_PutReturnsToPool verifies the decoded batch is
// pooled and Put() doesn't panic. REQ001999.
func TestSpillRoundTrip_PutReturnsToPool(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "k")
	b.Cols[0].Type = LX.T_INT_KW
	b.Cols[0].Data.Ints = []int64{42}
	b.Size = 1

	got := roundTripSpill(t, b)
	if !got.Pooled {
		t.Error("expected Pooled=true on decoded batch")
	}
	got.Put() // should not panic
}

// TestSpillRoundTrip_UnsupportedType verifies that T_TIMESTAMP is
// rejected with an error. REQ001999.
func TestSpillRoundTrip_UnsupportedType(t *testing.T) {
	b := UT.GetBatch(1)
	b.SetColumnName(0, "ts")
	b.Cols[0].Type = LX.T_TIMESTAMP
	b.Cols[0].Data.Ints = []int64{1}
	b.Size = 1

	var buf bytes.Buffer
	err := writeBatch(&buf, b)
	if err == nil {
		t.Fatal("expected error for unsupported type T_TIMESTAMP")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention 'unsupported', got: %v", err)
	}
}

// TestSpillRoundTrip_BadMagic verifies wrong magic is rejected. REQ001999.
func TestSpillRoundTrip_BadMagic(t *testing.T) {
	var buf bytes.Buffer
	// Write bad magic.
	buf.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF})
	buf.Write([]byte{0x01, 0x00}) // version
	buf.Write([]byte{0x00, 0x00}) // nCols
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // nRows

	_, err := readBatch(&buf)
	if err == nil {
		t.Fatal("expected error for bad magic")
	}
	if !strings.Contains(err.Error(), "magic") {
		t.Errorf("error should mention 'magic', got: %v", err)
	}
}

// TestSpillRoundTrip_BadVersion verifies wrong version is rejected. REQ001999.
func TestSpillRoundTrip_BadVersion(t *testing.T) {
	var buf bytes.Buffer
	// Write correct magic, bad version.
	buf.Write([]byte{0x4A, 0x48, 0x52, 0x47}) // "GRHJ" LE
	buf.Write([]byte{0x63, 0x00}) // version 99
	buf.Write([]byte{0x00, 0x00}) // nCols
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // nRows

	_, err := readBatch(&buf)
	if err == nil {
		t.Fatal("expected error for bad version")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention 'version', got: %v", err)
	}
}

// TestSpillRoundTrip_EmptyStream verifies that reading from an empty
// stream returns (nil, nil) — clean EOF. REQ001999.
func TestSpillRoundTrip_EmptyStream(t *testing.T) {
	var buf bytes.Buffer
	got, err := readBatch(&buf)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil batch for empty stream")
	}
}

// TestSpillFile_WriteSealRead verifies the spillFile lifecycle: write
// batches, seal, open reader, read back. REQ001999.
func TestSpillFile_WriteSealRead(t *testing.T) {
	sf, err := newSpillFile("")
	if err != nil {
		t.Fatalf("newSpillFile: %v", err)
	}
	defer sf.Close()

	b1 := UT.GetBatch(1)
	b1.SetColumnName(0, "k")
	b1.Cols[0].Type = LX.T_INT_KW
	b1.Cols[0].Data.Ints = []int64{1, 2, 3}
	b1.Size = 3

	b2 := UT.GetBatch(1)
	b2.SetColumnName(0, "k")
	b2.Cols[0].Type = LX.T_INT_KW
	b2.Cols[0].Data.Ints = []int64{4, 5}
	b2.Size = 2

	if err := sf.WriteBatch(b1); err != nil {
		t.Fatalf("WriteBatch 1: %v", err)
	}
	if err := sf.WriteBatch(b2); err != nil {
		t.Fatalf("WriteBatch 2: %v", err)
	}
	if err := sf.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	r, err := sf.OpenReader()
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	got1, err := readBatch(r)
	if err != nil {
		t.Fatalf("readBatch 1: %v", err)
	}
	if got1 == nil || got1.Size != 3 {
		t.Fatalf("batch 1: got %+v", got1)
	}
	got1.Put()

	got2, err := readBatch(r)
	if err != nil {
		t.Fatalf("readBatch 2: %v", err)
	}
	if got2 == nil || got2.Size != 2 {
		t.Fatalf("batch 2: got %+v", got2)
	}
	got2.Put()

	got3, err := readBatch(r)
	if err != nil {
		t.Fatalf("readBatch 3 (EOF): %v", err)
	}
	if got3 != nil {
		t.Fatal("expected nil at EOF")
		got3.Put()
	}
}

// TestSpillFile_CloseRemovesFile verifies that Close removes the temp
// file from disk. REQ001999.
func TestSpillFile_CloseRemovesFile(t *testing.T) {
	sf, err := newSpillFile("")
	if err != nil {
		t.Fatalf("newSpillFile: %v", err)
	}
	path := sf.Path()
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	if err := sf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// File should be gone.
	if _, err := os.Stat(path); err == nil {
		t.Error("temp file still exists after Close")
	}
}
