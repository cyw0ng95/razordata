package ls

import (
	"testing"
)

// TestEngineDeleteBatch_Basic verifies REQ001556: Engine.DeleteBatch
// inserts tombstones for a contiguous list of keys in a single
// amortised call, and subsequent reads return ErrNotFound.
func TestEngineDeleteBatch_Basic(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	const n = 100
	keys := make([][]byte, n)
	vals := make([][]byte, n)
	for i := 0; i < n; i++ {
		keys[i] = []byte("k" + itoa10(i))
		vals[i] = []byte("v" + itoa10(i))
	}
	if err := eng.WriteBatch(keys, vals); err != nil {
		t.Fatalf("WriteBatch (seed): %v", err)
	}

	// Sanity: reads work before delete.
	for i := 0; i < n; i++ {
		got, err := eng.Get(keys[i])
		if err != nil || string(got) != string(vals[i]) {
			t.Fatalf("seed Get[%d]: err=%v val=%q", i, err, got)
		}
	}

	if err := eng.DeleteBatch(keys); err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}

	// Reads after delete must report ErrNotFound for each key.
	for i := 0; i < n; i++ {
		got, err := eng.Get(keys[i])
		if err != ErrNotFound {
			t.Errorf("after DeleteBatch: Get(%q) err=%v val=%q, want ErrNotFound", keys[i], err, got)
		}
	}
}

// TestEngineDeleteBatch_EmptyNoop verifies that an empty key list is
// a clean no-op (no error, no flush triggered).
func TestEngineDeleteBatch_EmptyNoop(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := eng.DeleteBatch(nil); err != nil {
		t.Errorf("DeleteBatch(nil) = %v, want nil", err)
	}
	if err := eng.DeleteBatch([][]byte{}); err != nil {
		t.Errorf("DeleteBatch([]) = %v, want nil", err)
	}
}

// TestEngineDeleteBatch_ClosedEngine verifies DeleteBatch on a
// closed engine returns ErrClosed.
func TestEngineDeleteBatch_ClosedEngine(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := eng.DeleteBatch([][]byte{[]byte("k")}); err != ErrClosed {
		t.Errorf("DeleteBatch on closed engine = %v, want ErrClosed", err)
	}
}

// TestEngineDeleteBatch_IteratorSkipsTombstones verifies that keys
// deleted via DeleteBatch are not visible via NewIterator (REQ001556
// tombstone semantics — matches single Delete).
func TestEngineDeleteBatch_IteratorSkipsTombstones(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := eng.Insert([]byte("keep"), []byte("v")); err != nil {
		t.Fatalf("Insert keep: %v", err)
	}
	deleteKeys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	for _, k := range deleteKeys {
		if err := eng.Insert(k, []byte("v")); err != nil {
			t.Fatalf("Insert %s: %v", k, err)
		}
	}
	if err := eng.DeleteBatch(deleteKeys); err != nil {
		t.Fatalf("DeleteBatch: %v", err)
	}

	it := eng.NewIterator([]byte(""))
	defer it.Close()
	seen := map[string]bool{}
	for it.Next() {
		seen[string(it.Key())] = true
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator err: %v", err)
	}
	if !seen["keep"] {
		t.Errorf("keep key missing from iterator; got %v", seen)
	}
	for _, k := range deleteKeys {
		if seen[string(k)] {
			t.Errorf("deleted key %q still visible via iterator", k)
		}
	}
}

// itoa10 is a small helper that avoids importing strconv in this file.
func itoa10(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}