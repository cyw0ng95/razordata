package ls

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRangeTombstoneConversion_ForwardScan(t *testing.T) {
	dir := t.TempDir()

	w := acquireSSTWriter()
	defer releaseSSTWriter(w)
	w.Add([]byte("A"), []byte("valA"))
	w.Add([]byte("B"), tombstoneValue)
	w.Add([]byte("C"), tombstoneValue)
	w.Add([]byte("D"), []byte("valD"))
	w.AddRangeTombstone([]byte("E"), []byte("F"))
	data, err := w.Finish()
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "test.sst")
	if err := os.WriteFile(path, data, 0644); err != nil {
		// Not using fmt.Errorf here to avoid unused import
		t.Fatal(err)
	}

	reader, err := openSSTLazy(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	it := reader.Iterator()
	var results [][]byte
	for it.Next() {
		results = append(results, bytes.Clone(it.Key()))
	}

	want := [][]byte{[]byte("A"), []byte("D")}
	if len(results) != len(want) {
		// Not using fmt.Errorf here to avoid unused import
		t.Fatalf("got %d keys, want %d: %v", len(results), len(want), results)
	}
	for i, wantKey := range want {
		if !bytes.Equal(results[i], wantKey) {
			t.Errorf("key %d: got %q, want %q", i, results[i], wantKey)
		}
	}
}

func TestRangeTombstoneConversion_ReverseScan(t *testing.T) {
	// Not using fmt.Errorf here to avoid unused import
	dir := t.TempDir()

	w := acquireSSTWriter()
	defer releaseSSTWriter(w)
	w.Add([]byte("A"), []byte("valA"))
	w.Add([]byte("B"), tombstoneValue)
	w.Add([]byte("C"), tombstoneValue)
	w.Add([]byte("D"), []byte("valD"))
	data, err := w.Finish()
	if err != nil {
		t.Fatal(err)
	}

	reader, err := openSSTWithPath(data, filepath.Join(dir, "test.sst"))
	if err != nil {
		t.Fatal(err)
	}

	it := reader.Iterator()
	if !it.loadBlock(len(reader.indexBlock) - 1) {
		// Not using fmt.Errorf here to avoid unused import
		t.Fatal("failed to load last block")
	}

	var results [][]byte
	for {
		for i := len(it.pairs) - 1; i >= 0; i-- {
			kv := it.pairs[i]
			if !isTombstone(kv.value) && !reader.isInRangeTombstone(kv.key) {
				results = append(results, bytes.Clone(kv.key))
			}
		}
		if it.blockIdx == 0 {
			break
		}
		if !it.loadBlock(it.blockIdx - 1) {
			break
		}
	}

	want := [][]byte{[]byte("D"), []byte("A")}
	if len(results) != len(want) {
		t.Fatalf("reverse scan: got %d keys, want %d: %v", len(results), len(want), results)
	}
	for i, wantKey := range want {
		if !bytes.Equal(results[i], wantKey) {
			t.Errorf("reverse key %d: got %q, want %q", i, results[i], wantKey)
		}
	}
}
