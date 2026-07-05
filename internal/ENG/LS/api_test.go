package ls

import (
	"bytes"
	"container/heap"
	"path/filepath"
	"slices"
	"testing"
)

func TestEngine_InsertGetDelete(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	if err := eng.Insert([]byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Insert k1: %v", err)
	}
	v, err := eng.Get([]byte("k1"))
	if err != nil {
		t.Fatalf("Get k1: %v", err)
	}
	if !bytes.Equal(v, []byte("v1")) {
		t.Errorf("Get k1 = %q, want v1", v)
	}

	if err := eng.Insert([]byte("k1"), []byte("v2")); err != nil {
		t.Fatalf("Update k1: %v", err)
	}
	v, err = eng.Get([]byte("k1"))
	if err != nil {
		t.Fatalf("Get k1 after update: %v", err)
	}
	if !bytes.Equal(v, []byte("v2")) {
		t.Errorf("Get k1 after update = %q, want v2", v)
	}

	if err := eng.Delete([]byte("k1")); err != nil {
		t.Fatalf("Delete k1: %v", err)
	}
	_, err = eng.Get([]byte("k1"))
	if err != ErrNotFound {
		t.Errorf("Get deleted k1: got %v, want ErrNotFound", err)
	}
}

func TestEngine_NewIterator_PrefixFilter(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	pairs := map[string][]byte{
		"u:1": []byte("alice"),
		"u:2": []byte("bob"),
		"o:1": []byte("widget"),
		"u:3": []byte("carol"),
	}
	for k, v := range pairs {
		if err := eng.Insert([]byte(k), v); err != nil {
			t.Fatalf("Insert %s: %v", k, err)
		}
	}

	it := eng.NewIterator([]byte("u:"))
	defer it.Close()
	var got []string
	for it.Next() {
		got = append(got, string(it.Key())+"="+string(it.Value()))
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}
	slices.Sort(got)
	want := []string{"u:1=alice", "u:2=bob", "u:3=carol"}
	if !equalStringSlices(got, want) {
		t.Errorf("iterator got %v, want %v", got, want)
	}
}

func TestEngine_NewIterator_SkipsTombstones(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	if err := eng.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Insert([]byte("b"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Insert([]byte("c"), []byte("3")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Delete([]byte("b")); err != nil {
		t.Fatal(err)
	}

	it := eng.NewIterator(nil)
	defer it.Close()
	var got []string
	for it.Next() {
		got = append(got, string(it.Key()))
	}
	slices.Sort(got)
	want := []string{"a", "c"}
	if !equalStringSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEngine_NewIterator_EmptyPrefix(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	for _, k := range []string{"x", "y", "z"} {
		if err := eng.Insert([]byte(k), []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	it := eng.NewIterator(nil)
	defer it.Close()
	var n int
	for it.Next() {
		n++
	}
	if n != 3 {
		t.Errorf("expected 3 entries, got %d", n)
	}
}

func TestEnginePublic_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestEngine_OperationsAfterClose(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := eng.Insert([]byte("k"), []byte("v")); err != ErrClosed {
		t.Errorf("Insert after Close: got %v, want ErrClosed", err)
	}
	if _, err := eng.Get([]byte("k")); err != ErrClosed {
		t.Errorf("Get after Close: got %v, want ErrClosed", err)
	}
	if err := eng.Delete([]byte("k")); err != ErrClosed {
		t.Errorf("Delete after Close: got %v, want ErrClosed", err)
	}
}

func TestPrefixUpperBound(t *testing.T) {
	cases := []struct {
		prefix string
		want   string
		nilOut bool
	}{
		{"abc", "abd", false},
		{"ab\xff", "ac", false},
		{"a\xff\xff", "b", false},
		{"\xff\xff\xff", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		t.Run(c.prefix, func(t *testing.T) {
			got := prefixUpperBound([]byte(c.prefix))
			if c.nilOut {
				if got != nil {
					t.Errorf("got %q, want nil", got)
				}
				return
			}
			if string(got) != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestFileOverlapsPrefix(t *testing.T) {
	cases := []struct {
		min, max, prefix string
		want             bool
	}{
		{"u:1", "u:9", "u:", true},
		{"o:1", "o:9", "u:", false},
		{"u:1", "u:5", "u:6", false},
		{"u:1", "u:6", "u:5", true},
		{"u:5", "u:9", "u:5", true},
	}
	for _, c := range cases {
		t.Run(c.min+"-"+c.max+"+"+c.prefix, func(t *testing.T) {
			upper := prefixUpperBound([]byte(c.prefix))
			got := fileOverlapsPrefix([]byte(c.min), []byte(c.max), []byte(c.prefix), upper)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
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

func TestMergeIterator_MemtableOnly(t *testing.T) {
	mt := newMemtable(1 << 20)
	mt.Insert([]byte("a"), []byte("1"))
	mt.Insert([]byte("b"), []byte("2"))
	mt.Insert([]byte("c"), []byte("3"))

	dir := t.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}

	mi := newMergeIterator([]*memtable{mt}, m, dir, DefaultFS(), nil, nil)
	defer mi.Close()

	var got []string
	for mi.Next() {
		got = append(got, string(mi.Key())+"="+string(mi.Value()))
	}
	if err := mi.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	want := []string{"a=1", "b=2", "c=3"}
	if !equalStringSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergeIterator_SkipsTombstones(t *testing.T) {
	mt := newMemtable(1 << 20)
	mt.Insert([]byte("a"), []byte("1"))
	mt.Insert([]byte("b"), tombstoneValue)
	mt.Insert([]byte("c"), []byte("3"))

	dir := t.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}

	mi := newMergeIterator([]*memtable{mt}, m, dir, DefaultFS(), nil, nil)
	defer mi.Close()

	var got []string
	for mi.Next() {
		got = append(got, string(mi.Key()))
	}
	want := []string{"a", "c"}
	if !equalStringSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergeIterator_Dedup(t *testing.T) {
	mt1 := newMemtable(1 << 20)
	mt1.Insert([]byte("a"), []byte("1"))
	mt1.Insert([]byte("b"), []byte("2_from_mt1"))
	mt1.Freeze()

	mt2 := newMemtable(1 << 20)
	mt2.Insert([]byte("b"), []byte("2_from_mt2"))
	mt2.Insert([]byte("c"), []byte("3"))

	dir := t.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}

	// mt2 is "active" (last in slice → sources[0]) and wins dedup
	mi := newMergeIterator([]*memtable{mt1, mt2}, m, dir, DefaultFS(), nil, nil)
	defer mi.Close()

	var got []string
	for mi.Next() {
		got = append(got, string(mi.Key())+"="+string(mi.Value()))
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries after dedup, got %d: %v", len(got), got)
	}
	if string(got[0]) != "a=1" {
		t.Errorf("got %q, want \"a=1\"", got[0])
	}
	// Key "b" must appear exactly once, with the active (mt2) value
	if string(got[1]) != "b=2_from_mt2" {
		t.Errorf("got %q, want \"b=2_from_mt2\"", got[1])
	}
	if string(got[2]) != "c=3" {
		t.Errorf("got %q, want \"c=3\"", got[2])
	}
}

func TestIsTombstoneNil(t *testing.T) {
	if isTombstone(nil) {
		t.Error("isTombstone(nil) = true, want false")
	}
}

func TestIterHeapPushPop(t *testing.T) {
	h := &iterHeap{}
	heap.Init(h)

	items := []iterHeapItem{
		{key: []byte("c"), value: []byte("3"), src: 2},
		{key: []byte("a"), value: []byte("1"), src: 0},
		{key: []byte("b"), value: []byte("2"), src: 1},
	}
	for _, it := range items {
		heap.Push(h, it)
	}

	if h.Len() != 3 {
		t.Fatalf("Len = %d, want 3", h.Len())
	}

	var keys []string
	for h.Len() > 0 {
		item := heap.Pop(h).(iterHeapItem)
		keys = append(keys, string(item.key))
	}
	want := []string{"a", "b", "c"}
	if !equalStringSlices(keys, want) {
		t.Errorf("pop order got %v, want %v", keys, want)
	}
}

func TestMergeIterator_NextAfterClose(t *testing.T) {
	mt := newMemtable(1 << 20)
	mt.Insert([]byte("a"), []byte("1"))

	dir := t.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}

	mi := newMergeIterator([]*memtable{mt}, m, dir, DefaultFS(), nil, nil)
	mi.Close()
	if mi.Next() {
		t.Error("Next() after Close returned true, want false")
	}
}
