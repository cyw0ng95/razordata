package id

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func TestBTree_InsertSeek(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	entries := []struct {
		key, val []byte
	}{
		{[]byte("a"), []byte("1")},
		{[]byte("b"), []byte("2")},
		{[]byte("c"), []byte("3")},
	}
	for _, e := range entries {
		if err := bt.Insert(e.key, e.val); err != nil {
			t.Fatalf("Insert(%s): %v", e.key, err)
		}
	}
	for _, e := range entries {
		v, err := bt.Get(e.key)
		if err != nil {
			t.Fatalf("Get(%s): %v", e.key, err)
		}
		if !bytes.Equal(v, e.val) {
			t.Errorf("Get(%s) = %s, want %s", e.key, v, e.val)
		}
	}
}

func TestBTree_NotFound(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	_, err = bt.Get([]byte("missing"))
	if err != ErrNotFound {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestBTree_Delete(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	bt.Insert([]byte("x"), []byte("10"))
	bt.Insert([]byte("y"), []byte("20"))

	if err := bt.Delete([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_, err = bt.Get([]byte("x"))
	if err != ErrNotFound {
		t.Errorf("Get(x) after delete = %v, want ErrNotFound", err)
	}
	v, err := bt.Get([]byte("y"))
	if err != nil || !bytes.Equal(v, []byte("20")) {
		t.Errorf("Get(y) = %v, %v, want 20, nil", v, err)
	}
}

func TestBTree_Large(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	n := 5000
	for i := 0; i < n; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		val := []byte{byte(i)}
		if err := bt.Insert(key, val); err != nil {
			t.Fatalf("Insert(%d): %v", i, err)
		}
	}
	for i := 0; i < n; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		val := []byte{byte(i)}
		got, err := bt.Get(key)
		if err != nil {
			t.Fatalf("Get(%d): %v", i, err)
		}
		if !bytes.Equal(got, val) {
			t.Errorf("Get(%d) = %v, want %v", i, got, val)
		}
	}
}

func TestBTree_Cursor(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	keys := []string{"apple", "banana", "cherry", "date", "elderberry"}
	for _, k := range keys {
		bt.Insert([]byte(k), []byte("v_"+k))
	}

	c := bt.Cursor()
	if !c.Seek([]byte("banana")) {
		t.Fatal("Seek(banana) failed")
	}
	if !bytes.Equal(c.Key(), []byte("banana")) {
		t.Errorf("Key = %s, want banana", c.Key())
	}

	var scanned []string
	scanned = append(scanned, string(c.Key()))
	for c.Next() {
		scanned = append(scanned, string(c.Key()))
	}
	expected := []string{"banana", "cherry", "date", "elderberry"}
	if len(scanned) != len(expected) {
		t.Fatalf("scanned %d keys, want %d", len(scanned), len(expected))
	}
	for i, k := range scanned {
		if k != expected[i] {
			t.Errorf("scanned[%d] = %s, want %s", i, k, expected[i])
		}
	}
}

func TestBTree_Persistence(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		bt.Insert([]byte{byte(i)}, []byte{byte(i + 100)})
	}
	bt.Close()

	bt2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt2.Close()

	for i := 0; i < 100; i++ {
		v, err := bt2.Get([]byte{byte(i)})
		if err != nil {
			t.Fatalf("Get(%d) after reopen: %v", i, err)
		}
		if !bytes.Equal(v, []byte{byte(i + 100)}) {
			t.Errorf("Get(%d) = %v, want %v", i, v, byte(i+100))
		}
	}
}

func TestBTree_CloseIdempotent(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	bt.Insert([]byte("k"), []byte("v"))
	if err := bt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bt.Close(); err != ErrClosed {
		t.Errorf("second Close = %v, want ErrClosed", err)
	}
}

func TestBTree_Empty(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	_, err = bt.Get([]byte("x"))
	if err != ErrNotFound {
		t.Errorf("Get on empty = %v", err)
	}
	c := bt.Cursor()
	if c.Seek([]byte("x")) {
		t.Error("Seek on empty should return false")
	}
}

func TestBTree_Overwrite(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	bt.Insert([]byte("k"), []byte("v1"))
	bt.Insert([]byte("k"), []byte("v2"))

	v, err := bt.Get([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(v, []byte("v2")) {
		t.Errorf("Get(k) = %s, want v2", v)
	}
}

func TestBTree_FileExists(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	bt.Close()

	path := filepath.Join(dir, fileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("btree.razor file not created")
	}
}

func TestBTree_CursorCrossLeaf(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	n := 500
	for i := 0; i < n; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		val := []byte{byte(i)}
		bt.Insert(key, val)
	}

	c := bt.Cursor()
	if !c.Seek([]byte{0, 0}) {
		t.Fatal("Seek failed")
	}

	count := 0
	for c.Valid() {
		count++
		if !c.Next() {
			break
		}
	}
	if count != n {
		t.Errorf("cursor scanned %d keys, want %d", count, n)
	}
}

func TestBTree_DeleteNonExistent(t *testing.T) {
	dir := tmpDir(t)
	bt, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()

	bt.Insert([]byte("a"), []byte("1"))
	err = bt.Delete([]byte("z"))
	if err != ErrNotFound {
		t.Errorf("Delete(missing) = %v, want ErrNotFound", err)
	}
}
