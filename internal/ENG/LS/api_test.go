package ls

import (
	"bytes"
	"path/filepath"
	"sort"
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
	sort.Strings(got)
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
	sort.Strings(got)
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
