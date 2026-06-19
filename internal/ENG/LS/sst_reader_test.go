package ls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSSTReader_searchIndex(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_search")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("c"), []byte("3"))
	w.Add([]byte("f"), []byte("6"))
	w.Add([]byte("i"), []byte("9"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	idx := reader.searchIndex([]byte("e"))
	if idx < 0 {
		t.Fatal("expected non-negative index")
	}
}

func TestSSTReader_readBlock(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_readblock")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	block := reader.readBlock(0, 100)
	if block == nil {
		t.Fatal("expected non-nil block data")
	}
}

func TestSSTReader_mayContain(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_maycontain")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	if !reader.mayContain([]byte("key1")) {
		t.Fatal("expected mayContain to return true for key1")
	}
}

func TestSSTReader_OpenInvalid(t *testing.T) {
	_, err := openSST([]byte("too short"))
	if err != ErrInvalidSSTFormat {
		t.Fatalf("expected ErrInvalidSSTFormat, got %v", err)
	}
}

func TestSSTReader_Close(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_close")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestSSTWriter_MultipleBlocks(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_multiblocks")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	for i := 0; i < 1000; i++ {
		key := []byte(string(rune('a'+i%26)) + string(rune('0'+i/26)))
		val := []byte(string(rune('0' + i%10)))
		w.Add(key, val)
	}

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	if len(sstData) == 0 {
		t.Fatal("expected non-empty SST data")
	}
}

func TestSSTReader_Iterator_CloseTwice(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_iter_close")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	iter := reader.Iterator()
	iter.Close()
	iter.Close()
}

func TestSSTReader_Err(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_err")

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("key1"), []byte("value1"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST failed: %v", err)
	}

	iter := reader.Iterator()
	if iter.Err() != nil {
		t.Fatalf("expected nil error, got %v", iter.Err())
	}
}

// TestSSTIterator_FirstBlockRead is the REQ000187 regression test:
// the first call to Next() on a fresh iterator must load block 0
// and return true with the correct key/value. Before the
// blockIdx/pairIdx split, the iterator's `current > 0` guard
// skipped the first block, so a single-key SST iterated as
// empty.
func TestSSTIterator_FirstBlockRead(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("alpha"), []byte("one"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	t.Logf("indexBlock len: %d", len(reader.indexBlock))
	for i, e := range reader.indexBlock {
		t.Logf("  [%d] blockOffset=%d blockSize=%d", i, e.blockOffset, e.blockSize)
	}
	defer reader.Close()

	iter := reader.Iterator()
	defer iter.Close()

	// First Next must return true and surface the key/value.
	if !iter.Next() {
		t.Fatalf("REQ000187: first Next() returned false; the first block was not loaded")
	}
	if got := string(iter.Key()); got != "alpha" {
		t.Errorf("Key: want %q, got %q", "alpha", got)
	}
	if got := string(iter.Value()); got != "one" {
		t.Errorf("Value: want %q, got %q", "one", got)
	}
	// Iterator now exhausted; the second Next returns false.
	if iter.Next() {
		t.Errorf("second Next() returned true; expected end-of-iteration")
	}
}

// TestSSTReader_MayContainPrefix_NoFalseNegatives verifies REQ000599:
// the writer (setPrefixBloomBit) uses byte count as the modulus,
// while the reader used bit count, causing 7/8 of prefix queries to
// produce false negatives (the filter reported "not present" for
// prefixes that actually existed in the SST). This test inserts
// keys and verifies MayContainPrefix returns true for every
// inserted prefix — never false negative.
func TestSSTReader_MayContainPrefix_NoFalseNegatives(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_prefix_bloom")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	// Insert 100 keys with distinct 4-byte prefixes; the prefix
	// bloom is small but the test exercises the modulus match.
	const keyCount = 100
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		// prefix varies in first 4 bytes, suffix in last 4
		k := []byte{
			byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i),
			's', 'u', 'f', 'x',
		}
		keys[i] = k
		w.Add(k, []byte{byte(i)})
	}

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	defer reader.Close()

	// Sanity: prefix bloom should be non-empty.
	if len(reader.prefixBloom) == 0 {
		t.Fatal("prefix bloom is empty; test setup is invalid")
	}

	// Every inserted key's 8-byte prefix (truncated from key)
	// must be reported as "may contain". False negatives are
	// forbidden.
	falseNegatives := 0
	for _, k := range keys {
		prefix := k
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		if !reader.MayContainPrefix(prefix) {
			falseNegatives++
		}
	}
	if falseNegatives > 0 {
		t.Errorf("MayContainPrefix returned false for %d/%d inserted prefixes (REQ000599: prefix bloom modulus mismatch)", falseNegatives, keyCount)
	}
}

func TestReadBlock_DictCompressed(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("k1"), []byte("v1"))
	w.Add([]byte("k2"), []byte("v2"))
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	defer reader.Close()

	if len(reader.indexBlock) == 0 {
		t.Fatal("no index entries")
	}
	block := reader.readBlock(reader.indexBlock[0].blockOffset, reader.indexBlock[0].blockSize)
	if block == nil {
		t.Fatal("readBlock returned nil")
	}
	pairs, err := decodeBlock(block)
	if err != nil {
		t.Fatalf("decodeBlock: %v", err)
	}
	if len(pairs) != 2 {
		t.Errorf("expected 2 kv pairs, got %d", len(pairs))
	}
	if string(pairs[0].key) != "k1" || string(pairs[0].value) != "v1" {
		t.Errorf("first pair = (%q, %q), want (k1, v1)", pairs[0].key, pairs[0].value)
	}
}

func TestSSTReader_Find_NotFoundByBloom(t *testing.T) {
	w := newSSTWriter()
	w.Add([]byte("a"), []byte("1"))
	w.Add([]byte("b"), []byte("2"))
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	defer reader.Close()

	inserted := []string{"a", "b"}
	// All inserted keys must be found
	for _, key := range inserted {
		v, found := reader.Find([]byte(key))
		if !found {
			t.Errorf("Find(%q) = false, want true", key)
		}
		if v == nil {
			t.Errorf("Find(%q) value is nil", key)
		}
	}
	// Try many candidates to find one the bloom filter rejects
	foundBloomFalse := false
	for _, key := range []string{"zzz", "none", "xxxx", "test", "key99", "hello", "world", "abcd", "efgh", "ijkl", "mnop", "qrst", "uvwx", "y123", "z890", "foo", "bar", "baz"} {
		if !reader.mayContain([]byte(key)) {
			foundBloomFalse = true
			v, found := reader.Find([]byte(key))
			if found {
				t.Errorf("Find(%q) = (_, true) when bloom rejects; want (nil, false)", key)
			}
			if v != nil {
				t.Errorf("Find(%q) value = %q, want nil", key, v)
			}
			break
		}
	}
	if !foundBloomFalse {
		t.Log("no candidate key was rejected by bloom filter (all false positives)")
	}
}
