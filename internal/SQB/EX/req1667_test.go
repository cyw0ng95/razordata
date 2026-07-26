package EX

import (
	"context"
	"testing"

	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
)

// REQ001667: needsStableKey defaults to true so the UPDATE/DELETE path
// (ExtractPKForUpdate) always sees an independent StoreKey copy.
func TestSeqScan_NeedsStableKey_Default(t *testing.T) {
	if got := OP.NewSeqScan("t").NeedsStableKey(); !got {
		t.Errorf("NewSeqScan: NeedsStableKey() = false, want true (default safe)")
	}
	// Register a schema so NewSeqScanWithStore succeeds.
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	ex.RegisterTableWithPK("kv", []string{"k", "v"}, "k")
	ss, err := OP.NewSeqScanWithStore(&engineStore{eng: eng}, "kv")
	if err != nil {
		t.Fatalf("NewSeqScanWithStore: %v", err)
	}
	if got := ss.NeedsStableKey(); !got {
		t.Errorf("NewSeqScanWithStore: NeedsStableKey() = false, want true (default safe)")
	}
}

// REQ001667: with needsStableKey=false (read-only SELECT path), the
// StoreKey aliases SeqScan's internal keyBuf — no independent per-row
// copy. Reading a second row overwrites the first row's StoreKey content.
func TestSeqScan_NeedsStableKey_False_AliasesKey(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	ex.RegisterTableWithPK("kv", []string{"k", "v"}, "k")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO kv VALUES (1, 'a')",
		"INSERT INTO kv VALUES (2, 'b')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	ss, err := OP.NewSeqScanWithStore(&engineStore{eng: eng}, "kv")
	if err != nil {
		t.Fatalf("NewSeqScanWithStore: %v", err)
	}
	ss.SetNeedsStableKey(false) // read-only SELECT optimization

	row1, err := ss.Next(ctx)
	if err != nil {
		t.Fatalf("Next row1: %v", err)
	}
	if len(row1.StoreKey) == 0 {
		t.Fatal("row1.StoreKey is empty")
	}
	// Save the content of row1.StoreKey before reading row2.
	saved := append([]byte(nil), row1.StoreKey...)

	row2, err := ss.Next(ctx)
	if err != nil {
		t.Fatalf("Next row2: %v", err)
	}
	if len(row2.StoreKey) == 0 {
		t.Fatal("row2.StoreKey is empty")
	}

	// With needsStableKey=false, row1.StoreKey aliases the shared keyBuf,
	// which now holds row2's key. So row1.StoreKey content was overwritten
	// and no longer matches the saved copy.
	if equalBytes(row1.StoreKey, saved) {
		t.Errorf("needsStableKey=false: row1.StoreKey unchanged after reading row2; "+
			"expected aliasing (keyBuf reused). got=%v saved=%v", row1.StoreKey, saved)
	}
	// row1.StoreKey should now reflect row2's key (aliased buffer).
	if len(row1.StoreKey) == len(row2.StoreKey) && !equalBytes(row1.StoreKey, row2.StoreKey) {
		// Only assert equality when lengths match (same key length → same buffer).
		t.Errorf("needsStableKey=false: row1.StoreKey (%v) should alias row2.StoreKey (%v)",
			row1.StoreKey, row2.StoreKey)
	}
}

// REQ001667: with needsStableKey=true (UPDATE/DELETE path), each row's
// StoreKey is an independent copy. Reading a second row does NOT corrupt
// the first row's StoreKey.
func TestSeqScan_NeedsStableKey_True_IndependentCopy(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	ex.RegisterTableWithPK("kv", []string{"k", "v"}, "k")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO kv VALUES (1, 'a')",
		"INSERT INTO kv VALUES (2, 'b')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	ss, err := OP.NewSeqScanWithStore(&engineStore{eng: eng}, "kv")
	if err != nil {
		t.Fatalf("NewSeqScanWithStore: %v", err)
	}
	// needsStableKey stays true (default) — independent copy per row.

	row1, err := ss.Next(ctx)
	if err != nil {
		t.Fatalf("Next row1: %v", err)
	}
	if len(row1.StoreKey) == 0 {
		t.Fatal("row1.StoreKey is empty")
	}
	saved := append([]byte(nil), row1.StoreKey...)

	row2, err := ss.Next(ctx)
	if err != nil {
		t.Fatalf("Next row2: %v", err)
	}
	if len(row2.StoreKey) == 0 {
		t.Fatal("row2.StoreKey is empty")
	}

	// With needsStableKey=true, row1.StoreKey is an independent copy and
	// must NOT be corrupted by reading row2.
	if !equalBytes(row1.StoreKey, saved) {
		t.Errorf("needsStableKey=true: row1.StoreKey corrupted after reading row2; "+
			"expected independent copy. got=%v saved=%v", row1.StoreKey, saved)
	}
	// row1 and row2 must be distinct keys.
	if equalBytes(row1.StoreKey, row2.StoreKey) {
		t.Errorf("needsStableKey=true: row1.StoreKey == row2.StoreKey (%v); "+
			"expected distinct keys", row1.StoreKey)
	}
}

// equalBytes is a local helper to keep the test self-contained.
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
