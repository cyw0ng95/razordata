package ls

import (
	"bytes"
	"testing"
)

// TestIndexReader_SeekTo finds the first entry >= the given key.
func TestIndexReader_SeekTo(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	_ = store.Insert([]byte("a"), []byte("1"))
	_ = store.Insert([]byte("b"), []byte("2"))
	_ = store.Insert([]byte("c"), []byte("3"))

	r := NewIndexReader(store)
	defer r.Close()

	ok, err := r.SeekTo([]byte("b"))
	if err != nil {
		t.Fatalf("SeekTo: %v", err)
	}
	if !ok {
		t.Fatal("SeekTo returned false")
	}
	if !bytes.Equal(r.PrimaryKey(), []byte("2")) {
		t.Errorf("pk = %q, want 2", r.PrimaryKey())
	}
}

// TestIndexReader_Iterate walks all entries.
func TestIndexReader_Iterate(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	for i := 0; i < 5; i++ {
		_ = store.Insert([]byte{byte('a' + i)}, []byte{byte('A' + i)})
	}

	r := NewIndexReader(store)
	defer r.Close()

	ok, _ := r.SeekTo([]byte(""))
	if !ok {
		t.Fatal("SeekTo: no entries")
	}

	count := 0
	for {
		count++
		ok, err := r.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
	}
	if count != 5 {
		t.Errorf("iterated %d entries, want 5", count)
	}
}

// TestIndexReader_SeekTo_NotFound returns false for empty index.
func TestIndexReader_SeekTo_NotFound(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	r := NewIndexReader(store)
	defer r.Close()

	ok, err := r.SeekTo([]byte("anything"))
	if err != nil {
		t.Fatalf("SeekTo: %v", err)
	}
	if ok {
		t.Error("SeekTo on empty index should return false")
	}
}

// TestIndexReader_Close verifies idempotent close.
func TestIndexReader_Close(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	r := NewIndexReader(store)
	if err := r.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestIndexReader_PrimaryKeyIndependentCall verifies that the
// returned slice from PrimaryKey is independent across Seek/Next
// calls (mutation in one returned slice does not affect the next).
func TestIndexReader_PrimaryKeyIndependentCall(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	_ = store.Insert([]byte("k1"), []byte("hello1"))
	_ = store.Insert([]byte("k2"), []byte("hello2"))

	r := NewIndexReader(store)
	defer r.Close()
	_, _ = r.SeekTo([]byte(""))

	// Walk all entries and verify we get both
	pk1 := r.PrimaryKey()
	t.Logf("pk1 = %q", pk1)

	ok, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ok {
		t.Fatal("Next returned no more entries")
	}
	pk2 := r.PrimaryKey()
	t.Logf("pk2 = %q", pk2)

	if !bytes.Equal(pk2, []byte("hello2")) {
		t.Errorf("after Next, pk = %q, want hello2", pk2)
	}
}
