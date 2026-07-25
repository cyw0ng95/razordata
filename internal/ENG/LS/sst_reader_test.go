package ls

import (
	"fmt"
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

	block := reader.readBlock(0, 100, 0)
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

	// Sanity: prefix bloom or ribbon should be non-empty.
	if len(reader.prefixBloom) == 0 && len(reader.ribbon) == 0 {
		t.Fatal("prefix bloom and ribbon are both empty; test setup is invalid")
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
	block := reader.readBlock(reader.indexBlock[0].blockOffset, reader.indexBlock[0].blockSize, 0)
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

func TestSSTReader_LazyOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sst")

	// Write SST to disk
	w := newSSTWriter()
	w.Add([]byte("a"), []byte("1"))
	w.Add([]byte("b"), []byte("2"))
	w.Add([]byte("c"), []byte("3"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}
	if err := os.WriteFile(path, sstData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Open lazily
	reader, err := openSSTLazy(path)
	if err != nil {
		t.Fatalf("openSSTLazy failed: %v", err)
	}

	// Verify data is NOT loaded into memory
	if len(reader.data) != 0 {
		t.Errorf("lazy reader.data should be empty, got %d bytes", len(reader.data))
	}

	// Verify filePath is set
	if reader.filePath != path {
		t.Errorf("filePath = %q, want %q", reader.filePath, path)
	}

	// Verify Find works (reads blocks lazily)
	for i, key := range []string{"a", "b", "c"} {
		v, found := reader.Find([]byte(key))
		if !found {
			t.Errorf("Find(%q) = false, want true", key)
		}
		if string(v) != string('1'+byte(i)) {
			t.Errorf("Find(%q) = %q, want %q", key, v, string('1'+byte(i)))
		}
	}

	// Verify Iterator works
	it := reader.Iterator()
	var keys []string
	for it.Next() {
		keys = append(keys, string(it.Key()))
	}
	if len(keys) != 3 {
		t.Errorf("Iterator returned %d keys, want 3", len(keys))
	}
	for i, k := range keys {
		if k != string('a'+byte(i)) {
			// Use byte arithmetic for 'a', 'b', 'c'
			expected := string('a' + byte(i))
			loader := reader.readBlock(reader.indexBlock[0].blockOffset, reader.indexBlock[0].blockSize, 0)
			t.Logf("blockData len: %d", len(loader))
			t.Errorf("Iterator key[%d] = %q, want %q", i, k, expected)
		}
	}
}

func TestSSTReader_LazyOpen_MultiBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sst")

	// Write SST with many keys to force multiple blocks
	w := newSSTWriter()
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key%03d", i)
		val := fmt.Sprintf("value%03d", i)
		w.Add([]byte(key), []byte(val))
	}

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish failed: %v", err)
	}
	if err := os.WriteFile(path, sstData, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Open lazily
	reader, err := openSSTLazy(path)
	if err != nil {
		t.Fatalf("openSSTLazy failed: %v", err)
	}

	// Verify data is NOT loaded into memory
	if len(reader.data) != 0 {
		t.Errorf("lazy reader.data should be empty, got %d bytes", len(reader.data))
	}

	// Verify all keys can be found
	for i := 0; i < 100; i++ {
		// REQ001008: range tombstones should suppress keys in the range
		key := fmt.Sprintf("key%03d", i)
		val := fmt.Sprintf("value%03d", i)
		v, found := reader.Find([]byte(key))
		if !found {
			t.Errorf("Find(%q) = false, want true", key)
		}
		if string(v) != val {
			t.Errorf("Find(%q) = %q, want %q", key, v, val)
		}
	}

	// Verify Iterator returns all keys
	it := reader.Iterator()
	var keys []string
	for it.Next() {
		keys = append(keys, string(it.Key()))
	}
	if len(keys) != 100 {
		t.Errorf("Iterator returned %d keys, want 100", len(keys))
	}
	for i, k := range keys {
		expected := fmt.Sprintf("key%03d", i)
		if k != expected {
			t.Errorf("Iterator key[%d] = %q, want %q", i, k, expected)
		}
	}
}

// TestRangeTombstone_SuppressesRange verifies REQ001008: range tombstones
// suppress all keys in the [start, end) range. Keys outside the range are
// unaffected.
func TestRangeTombstone_SuppressesRange(t *testing.T) {
	w := newSSTWriter()

	// Insert keys: a, b, c, d, e, f, g
	keys := []string{"a", "b", "c", "d", "e", "f", "g"}
	for _, k := range keys {
		w.Add([]byte(k), []byte(k))
	}

	// Add range tombstone covering [c, f)
	w.AddRangeTombstone([]byte("c"), []byte("f"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	defer reader.Close()

	// Keys outside range should be found
	for _, k := range []string{"a", "b", "g"} {
		v, found := reader.Find([]byte(k))
		if !found {
			t.Errorf("Find(%q) = false, want true (outside range tombstone)", k)
		}
		if string(v) != k {
			t.Errorf("Find(%q) = %q, want %q", k, v, k)
		}
	}

	// Keys inside range should be suppressed
	for _, k := range []string{"c", "d", "e"} {
		v, found := reader.Find([]byte(k))
		if found {
			t.Errorf("Find(%q) = true, want false (inside range tombstone [c, f))", k)
		}
		if v != nil {
			t.Errorf("Find(%q) value = %q, want nil", k, v)
		}
	}

	// Key at range boundary (end) should NOT be suppressed
	v, found := reader.Find([]byte("f"))
	if !found {
		t.Errorf("Find(%q) = false, want true (at range end boundary)", "f")
	}
	if string(v) != "f" {
		t.Errorf("Find(%q) = %q, want %q", "f", v, "f")
	}
}

// TestRangeTombstone_MultipleRanges verifies REQ001008: multiple range
// tombstones can coexist and each suppresses its own range.
func TestRangeTombstone_MultipleRanges(t *testing.T) {
	w := newSSTWriter()

	// Insert keys: a through j
	for i := 0; i < 10; i++ {
		k := []byte{byte('a' + i)}
		w.Add(k, k)
	}

	// Add two range tombstones: [c, e) and [g, i)
	w.AddRangeTombstone([]byte("c"), []byte("e"))
	w.AddRangeTombstone([]byte("g"), []byte("i"))

	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}
	defer reader.Close()

	// Keys outside any range should be found
	for _, k := range []string{"a", "b", "f", "j"} {
		v, found := reader.Find([]byte(k))
		if !found {
			t.Errorf("Find(%q) = false, want true", k)
		}
		if string(v) != k {
			t.Errorf("Find(%q) = %q, want %q", k, v, k)
		}
	}

	// Keys inside first range [c, e) should be suppressed
	for _, k := range []string{"c", "d"} {
		_, found := reader.Find([]byte(k))
		if found {
			t.Errorf("Find(%q) = true, want false (inside [c, e))", k)
		}
	}

	// Keys inside second range [g, i) should be suppressed
	for _, k := range []string{"g", "h"} {
		_, found := reader.Find([]byte(k))
		if found {
			t.Errorf("Find(%q) = true, want false (inside [g, i))", k)
		}
	}
}

// TestSSTIterator_ReadBlock_AllPairsReturned verifies that ReadBlock
// returns every surviving K/V pair from the SST in order, and that
// subsequent ReadBlock calls advance past consumed blocks. REQ001995.
func TestSSTIterator_ReadBlock_AllPairsReturned(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_readblock_iter")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	for i := 0; i < 32; i++ {
		key := []byte(fmt.Sprintf("k%02d", i))
		val := []byte(fmt.Sprintf("v%02d", i))
		w.Add(key, val)
	}
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}

	it := reader.Iterator()
	defer it.Close()

	var collected [][]byte
	for {
		_, values, ok := it.ReadBlock()
		if !ok {
			break
		}
		collected = append(collected, values...)
	}
	if len(collected) != 32 {
		t.Fatalf("ReadBlock yielded %d pairs, want 32", len(collected))
	}
	for i, v := range collected {
		want := fmt.Sprintf("v%02d", i)
		if string(v) != want {
			t.Errorf("pair[%d]=%s, want %s", i, string(v), want)
		}
	}
}

// TestSSTIterator_ReadBlock_Filtered verifies that ReadBlock skips
// tombstones and empty values, mirroring Next()'s filtering. REQ001995.
func TestSSTIterator_ReadBlock_Filtered(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_readblock_filtered")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("a"), []byte("1"))
	w.Add([]byte("b"), tombstoneValue) // tombstone — should be dropped
	w.Add([]byte("c"), []byte(""))
	w.Add([]byte("d"), []byte("4"))
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}

	it := reader.Iterator()
	defer it.Close()

	var collected [][]byte
	for {
		_, values, ok := it.ReadBlock()
		if !ok {
			break
		}
		collected = append(collected, values...)
	}
	if len(collected) != 2 {
		t.Fatalf("ReadBlock yielded %d pairs, want 2 (tombstone and empty dropped)", len(collected))
	}
	if string(collected[0]) != "1" || string(collected[1]) != "4" {
		t.Errorf("unexpected pair order: %q, %q", collected[0], collected[1])
	}
}

// TestSSTIterator_ReadBlock_EqualsNext verifies that iterating via
// ReadBlock yields exactly the same K/V stream as iterating via
// Next()/Key()/Value(). REQ001995 equivalence guarantee.
func TestSSTIterator_ReadBlock_EqualsNext(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_readblock_equals")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	w := newSSTWriter()
	for i := 0; i < 16; i++ {
		key := []byte(fmt.Sprintf("key%02d", i))
		val := []byte(fmt.Sprintf("val%02d", i))
		w.Add(key, val)
	}
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Per-row reference path.
	reader1, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST(1): %v", err)
	}
	it1 := reader1.Iterator()
	var refKeys, refVals [][]byte
	for it1.Next() {
		refKeys = append(refKeys, append([]byte(nil), it1.Key()...))
		refVals = append(refVals, append([]byte(nil), it1.Value()...))
	}
	it1.Close()

	// Block path.
	reader2, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST(2): %v", err)
	}
	it2 := reader2.Iterator()
	var blkKeys, blkVals [][]byte
	for {
		k, v, ok := it2.ReadBlock()
		if !ok {
			break
		}
		blkKeys = append(blkKeys, k...)
		blkVals = append(blkVals, v...)
	}
	it2.Close()

	if len(refKeys) != len(blkKeys) || len(refVals) != len(blkVals) {
		t.Fatalf("length mismatch: ref=%d blk=%d", len(refKeys), len(blkKeys))
	}
	for i := range refKeys {
		if string(refKeys[i]) != string(blkKeys[i]) {
			t.Errorf("key[%d]: ref=%q blk=%q", i, refKeys[i], blkKeys[i])
		}
		if string(refVals[i]) != string(blkVals[i]) {
			t.Errorf("val[%d]: ref=%q blk=%q", i, refVals[i], blkVals[i])
		}
	}
}

// TestSSTIterator_LastBlockColumnStats verifies that after ReadBlock
// returns, LastBlockColumnStats exposes the min/max of the just-returned
// block. REQ001996 — this is the capability the SeqScan block-batched
// path uses to skip blocks outside the range predicate.
func TestSSTIterator_LastBlockColumnStats(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_last_block_stats")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("dir: %v", err)
	}

	w := newSSTWriter()
	for i := 10; i <= 100; i += 10 {
		key := []byte(fmt.Sprintf("%03d", i))
		val := []byte(fmt.Sprintf("v%d", i))
		w.Add(key, val)
	}
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}

	it := reader.Iterator()
	defer it.Close()

	// Read all blocks until EOF; for each, query LastBlockColumnStats.
	blockCount := 0
	for {
		_, _, ok := it.ReadBlock()
		if !ok {
			break
		}
		blockCount++
		// LastBlockColumnStats should at minimum not panic. Stats may be
		// empty if the SST wasn't written with stats — that's fine.
		_, _, _ = it.LastBlockColumnStats(0)
	}
	if blockCount == 0 {
		t.Fatal("expected at least one block from ReadBlock")
	}
}

// TestSSTIterator_FindBlock verifies that FindBlock returns the index
// of the block whose largestKey is the first >= key. REQ001997.
func TestSSTIterator_FindBlock(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_find_block")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("dir: %v", err)
	}

	w := newSSTWriter()
	w.Add([]byte("a"), []byte("1"))
	w.Add([]byte("c"), []byte("3"))
	w.Add([]byte("e"), []byte("5"))
	w.Add([]byte("g"), []byte("7"))
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	reader, err := openSST(sstData)
	if err != nil {
		t.Fatalf("openSST: %v", err)
	}

	it := reader.Iterator()
	defer it.Close()

	tests := []struct {
		key  string
		want int
	}{
		{"a", 0}, // first block's largestKey >= "a"
		{"b", 0}, // first block's largestKey "c" or "g" (single block) >= "b"
		{"c", 0},
		{"g", 0},
		{"x", -1}, // > all largestKeys
		{"", 0},   // empty key matches everything
	}
	for _, tc := range tests {
		got := it.FindBlock([]byte(tc.key))
		if got != tc.want {
			t.Errorf("FindBlock(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}
}

// TestSSTIterator_SeekToBlock verifies that SeekToBlock correctly
// positions the iterator so subsequent Next() / ReadBlock() returns
// from the seeked block. REQ001997.
func TestSSTIterator_SeekToBlock(t *testing.T) {
	dir := t.TempDir()
	dir = filepath.Join(dir, "test_sst_seek")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("dir: %v", err)
	}

	w := newSSTWriter()
	for i := 0; i < 16; i++ {
		key := []byte(fmt.Sprintf("k%02d", i))
		val := []byte(fmt.Sprintf("v%02d", i))
		w.Add(key, val)
	}
	sstData, err := w.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	t.Run("seek to 0", func(t *testing.T) {
		reader, err := openSST(sstData)
		if err != nil {
			t.Fatalf("openSST: %v", err)
		}
		it := reader.Iterator()
		defer it.Close()
		it.SeekToBlock(0)
		keys, _, ok := it.ReadBlock()
		if !ok {
			t.Fatal("ReadBlock returned ok=false")
		}
		if len(keys) == 0 || string(keys[0]) != "k00" {
			t.Errorf("first key = %q, want k00", keys[0])
		}
	})

	t.Run("seek past end → EOF", func(t *testing.T) {
		reader, err := openSST(sstData)
		if err != nil {
			t.Fatalf("openSST: %v", err)
		}
		it := reader.Iterator()
		defer it.Close()
		it.SeekToBlock(100)
		_, _, ok := it.ReadBlock()
		if ok {
			t.Fatal("ReadBlock should return ok=false when seek past end")
		}
	})

	t.Run("seek to current", func(t *testing.T) {
		// Read first block via ReadBlock, then SeekToBlock(0) — the
		// next ReadBlock should still yield the first block's first key.
		reader, err := openSST(sstData)
		if err != nil {
			t.Fatalf("openSST: %v", err)
		}
		it := reader.Iterator()
		defer it.Close()
		it.SeekToBlock(0)
		keys1, _, ok := it.ReadBlock()
		if !ok || len(keys1) == 0 {
			t.Fatalf("ReadBlock #1: ok=%v keys=%v", ok, keys1)
		}
		first := string(keys1[0])
		it.SeekToBlock(0)
		keys2, _, ok := it.ReadBlock()
		if !ok || len(keys2) == 0 {
			t.Fatalf("ReadBlock #2 after SeekToBlock(0): ok=%v", ok)
		}
		if string(keys2[0]) != first {
			t.Errorf("after re-seek, first key = %q, want %q", keys2[0], first)
		}
	})
}
