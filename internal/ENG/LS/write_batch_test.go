package ls

import (
	"fmt"
	"testing"
)

// TestEngineWriteBatch_Basic verifies REQ001421: Engine.WriteBatch
// inserts a contiguous list of key/value pairs and reads them back.
func TestEngineWriteBatch_Basic(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	keys := make([][]byte, 100)
	vals := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		keys[i] = []byte(fmt.Sprintf("k%d", i))
		vals[i] = []byte(fmt.Sprintf("v%d", i))
	}
	if err := eng.WriteBatch(keys, vals); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	for i := 0; i < 100; i++ {
		got, err := eng.Get(keys[i])
		if err != nil {
			t.Fatalf("Get(%s): %v", keys[i], err)
		}
		if string(got) != string(vals[i]) {
			t.Errorf("Get(%s) = %q, want %q", keys[i], got, vals[i])
		}
	}
}

// TestEngineWriteBatch_LengthMismatch returns an error when keys
// and values lengths disagree. REQ001421.
func TestEngineWriteBatch_LengthMismatch(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if err := eng.WriteBatch([][]byte{[]byte("k")}, [][]byte{}); err == nil {
		t.Fatal("expected ErrBatchLengthMismatch on empty values")
	}
}

// BenchmarkEngineWrite_PerRow is the baseline for REQ001421. Each
// iteration writes one row; the steady state is one inserted row
// per call.
func BenchmarkEngineWrite_PerRow(b *testing.B) {
	dir := b.TempDir()
	eng, err := newEngine(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()
	v := []byte("v")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := eng.Write([]byte(fmt.Sprintf("k%d", i)), v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEngineWriteBatch_100 measures the cost of writing 100
// rows per iteration through Engine.WriteBatch. The atomic-Load
// costs are amortised: closed.Load runs once per batch instead of
// per row, and the ShouldFlush check fires once after every row
// succeeds (so still N checks) — only the closed-flag gate is
// saved. The remaining gap should be in the hot loop body itself
// (memtable shard insert + size accounting).
func BenchmarkEngineWriteBatch_100(b *testing.B) {
	dir := b.TempDir()
	eng, err := newEngine(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	// Pre-build the 100-row slice outside the timer so the
	// measurement reflects the WriteBatch path itself, not the
	// allocation pattern of the inputs.
	keys := make([][]byte, 100)
	vals := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		keys[i] = []byte(fmt.Sprintf("kk%d", i))
		vals[i] = []byte("v")
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Re-key per iteration to avoid memtable size cap.
		for j := 0; j < 100; j++ {
			keys[j] = []byte(fmt.Sprintf("kk%d-%d", i, j))
		}
		if err := eng.WriteBatch(keys, vals); err != nil {
			b.Fatal(err)
		}
	}
}
