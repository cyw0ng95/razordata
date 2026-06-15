package ls

import (
	"bytes"
	"testing"
)

// TestIndexStore_KeyEncoding verifies the key prefix format.
func TestIndexStore_KeyEncoding(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 7, "idx_email")
	prefix := store.indexPrefix()

	// Expected: "__idx__:" + 7 (8 bytes big-endian) + ":" + "idx_email" + ":"
	expected := []byte("__idx__:")
	expected = append(expected, 0, 0, 0, 0, 0, 0, 0, 7)
	expected = append(expected, ':')
	expected = append(expected, "idx_email"...)
	expected = append(expected, ':')

	if !bytes.Equal(prefix, expected) {
		t.Errorf("prefix = %q, want %q", prefix, expected)
	}
}

// TestIndexStore_InsertGet verifies basic CRUD.
func TestIndexStore_InsertGet(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx_email")
	if err := store.Insert([]byte("alice@example.com"), []byte("1001")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	pk, found, err := store.Get([]byte("alice@example.com"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("Get: not found")
	}
	if !bytes.Equal(pk, []byte("1001")) {
		t.Errorf("pk = %q, want 1001", pk)
	}
}

// TestIndexStore_Get_Missing returns false for non-existent key.
func TestIndexStore_Get_Missing(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx_email")
	pk, found, err := store.Get([]byte("nobody"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Errorf("expected not found, got pk=%q", pk)
	}
}

// TestIndexStore_Delete removes an entry.
func TestIndexStore_Delete(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx_email")
	_ = store.Insert([]byte("a"), []byte("1"))
	_ = store.Insert([]byte("b"), []byte("2"))

	if err := store.Delete([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, found, _ := store.Get([]byte("a"))
	if found {
		t.Errorf("after Delete, key still found")
	}
	// Other key still present
	_, found, _ = store.Get([]byte("b"))
	if !found {
		t.Errorf("Delete removed wrong key")
	}
}

// TestIndexStore_NamespaceIsolation verifies two indexes on the
// same table don't collide.
func TestIndexStore_NamespaceIsolation(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	idxA := NewIndexStore(eng, 1, "idx_a")
	idxB := NewIndexStore(eng, 1, "idx_b")

	_ = idxA.Insert([]byte("key"), []byte("a-val"))
	_ = idxB.Insert([]byte("key"), []byte("b-val"))

	pkA, _, _ := idxA.Get([]byte("key"))
	pkB, _, _ := idxB.Get([]byte("key"))
	if !bytes.Equal(pkA, []byte("a-val")) {
		t.Errorf("idxA pk = %q, want a-val", pkA)
	}
	if !bytes.Equal(pkB, []byte("b-val")) {
		t.Errorf("idxB pk = %q, want b-val", pkB)
	}
}

// TestIndexStore_TableIsolation verifies indexes on different
// tables don't collide.
func TestIndexStore_TableIsolation(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	idx1 := NewIndexStore(eng, 1, "idx_email")
	idx2 := NewIndexStore(eng, 2, "idx_email")

	_ = idx1.Insert([]byte("x"), []byte("1"))
	_ = idx2.Insert([]byte("x"), []byte("2"))

	pk1, _, _ := idx1.Get([]byte("x"))
	pk2, _, _ := idx2.Get([]byte("x"))
	if !bytes.Equal(pk1, []byte("1")) {
		t.Errorf("idx1 pk = %q, want 1", pk1)
	}
	if !bytes.Equal(pk2, []byte("2")) {
		t.Errorf("idx2 pk = %q, want 2", pk2)
	}
}

// TestIndexStore_Seek iterates all entries with a key prefix.
func TestIndexStore_Seek(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx_email")
	_ = store.Insert([]byte("alice@x.com"), []byte("1"))
	_ = store.Insert([]byte("bob@x.com"), []byte("2"))
	_ = store.Insert([]byte("carol@x.com"), []byte("3"))

	it := store.Seek([]byte(""))
	defer it.Close()
	count := 0
	for it.Next() {
		count++
	}
	if count != 3 {
		t.Errorf("Seek all: got %d, want 3", count)
	}
}

// TestIndexStore_Count returns the number of entries.
func TestIndexStore_Count(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	for i := 0; i < 5; i++ {
		_ = store.Insert([]byte{byte(i)}, []byte{byte(i + 100)})
	}
	n, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 5 {
		t.Errorf("Count = %d, want 5", n)
	}
}

// TestIndexStore_Range verifies range scan.
func TestIndexStore_Range(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	_ = store.Insert([]byte("a"), []byte("1"))
	_ = store.Insert([]byte("b"), []byte("2"))
	_ = store.Insert([]byte("c"), []byte("3"))
	_ = store.Insert([]byte("d"), []byte("4"))

	// Range [b, d) — should return b and c
	it, err := store.Range([]byte("b"), []byte("d"))
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	defer it.Close()
	got := []string{}
	for it.Next() {
		got = append(got, string(it.Value()))
	}
	if len(got) != 2 {
		t.Errorf("range got %d entries, want 2: %v", len(got), got)
	}
}

// TestIndexStore_StripPrefix verifies prefix removal.
func TestIndexStore_StripPrefix(t *testing.T) {
	dir := t.TempDir()
	eng, _ := Open(dir)
	t.Cleanup(func() { _ = eng.Close() })

	store := NewIndexStore(eng, 1, "idx")
	fullKey := store.encodeKey([]byte("hello"))
	stripped := store.StripPrefix(fullKey)
	if !bytes.Equal(stripped, []byte("hello")) {
		t.Errorf("stripped = %q, want hello", stripped)
	}

	// Non-matching prefix returns the key unchanged
	other := []byte("not_an_index_key")
	if !bytes.Equal(store.StripPrefix(other), other) {
		t.Errorf("StripPrefix(non-matching) should return input unchanged")
	}
}
