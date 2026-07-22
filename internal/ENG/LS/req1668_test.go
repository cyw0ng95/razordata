package ls

import (
	"bytes"
	"testing"
)

// REQ001668: three sources all carrying the same key "a" must dedup
// to a single emission (the active/newest value wins), with the
// remaining per-source follow-up keys emitted in sorted order. This
// exercises the in-place peek+advance+siftDown loop iterating more
// than once (two duplicate sources advanced before the winner is
// returned).
func TestMergeIterator_Dedup_MultiSourceSameKey(t *testing.T) {
	mt1 := newMemtable(1 << 20)
	mt1.Insert([]byte("a"), []byte("v1"))
	mt1.Insert([]byte("d"), []byte("d1"))
	mt1.Freeze()

	mt2 := newMemtable(1 << 20)
	mt2.Insert([]byte("a"), []byte("v2"))
	mt2.Insert([]byte("e"), []byte("e1"))
	mt2.Freeze()

	mt3 := newMemtable(1 << 20)
	mt3.Insert([]byte("a"), []byte("v3"))
	mt3.Insert([]byte("f"), []byte("f1"))

	dir := t.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}

	// mt3 is active (last → sources[0]); it wins the dedup for "a".
	mi := newMergeIterator([]*memtable{mt1, mt2, mt3}, m, dir, DefaultFS(), nil, nil, 0, nil)
	defer mi.Close()

	var got []string
	for mi.Next() {
		got = append(got, string(mi.Key())+"="+string(mi.Value()))
	}
	if err := mi.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}

	want := []string{"a=v3", "d=d1", "e=e1", "f=f1"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// REQ001668: when a duplicate-key source is exhausted during the
// dedup advance (no further rows), the peek+advance loop must drop it
// via pop() and iteration must continue with the remaining sources.
func TestMergeIterator_Dedup_SourceExhausted(t *testing.T) {
	mt1 := newMemtable(1 << 20)
	mt1.Insert([]byte("a"), []byte("v1")) // only key — exhausted after dedup
	mt1.Freeze()

	mt2 := newMemtable(1 << 20)
	mt2.Insert([]byte("a"), []byte("v2"))
	mt2.Insert([]byte("b"), []byte("b1"))

	dir := t.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}

	// mt2 is active (sources[0]) and wins "a". mt1 (sources[1]) has no
	// further rows after "a", so the dedup loop's else-branch pops it.
	mi := newMergeIterator([]*memtable{mt1, mt2}, m, dir, DefaultFS(), nil, nil, 0, nil)
	defer mi.Close()

	var got []string
	for mi.Next() {
		got = append(got, string(mi.Key())+"="+string(mi.Value()))
	}
	if err := mi.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}

	want := []string{"a=v2", "b=b1"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// REQ001668: dedup must not lose or duplicate keys when many sources
// interleave. A regression guard for the in-place siftDown path under
// a denser overlap pattern.
func TestMergeIterator_Dedup_Interleaved(t *testing.T) {
	mt1 := newMemtable(1 << 20)
	mt1.Insert([]byte("k1"), []byte("a1"))
	mt1.Insert([]byte("k2"), []byte("a2"))
	mt1.Insert([]byte("k3"), []byte("a3"))
	mt1.Freeze()

	mt2 := newMemtable(1 << 20)
	mt2.Insert([]byte("k1"), []byte("b1"))
	mt2.Insert([]byte("k2"), []byte("b2"))
	mt2.Insert([]byte("k4"), []byte("b4"))

	dir := t.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}

	mi := newMergeIterator([]*memtable{mt1, mt2}, m, dir, DefaultFS(), nil, nil, 0, nil)
	defer mi.Close()

	var keys [][]byte
	for mi.Next() {
		// Duplicate the key slice — mi.Key() is owned and reused.
		keys = append(keys, append([]byte(nil), mi.Key()...))
	}
	if err := mi.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}

	want := [][]byte{[]byte("k1"), []byte("k2"), []byte("k3"), []byte("k4")}
	if len(keys) != len(want) {
		t.Fatalf("got %d keys %s, want %d %s", len(keys), bytes.Join(keys, []byte(",")), len(want), bytes.Join(want, []byte(",")))
	}
	for i := range want {
		if !bytes.Equal(keys[i], want[i]) {
			t.Errorf("key %d: got %q, want %q (all=%s)", i, keys[i], want[i], bytes.Join(keys, []byte(",")))
		}
	}
}
