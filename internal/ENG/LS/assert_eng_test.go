//go:build debug

package ls

import (
	"os"
	"os/exec"
	"testing"
)

func TestENG_Assert_FrozenMemtableInsert(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		m := newMemtable(1024)
		m.Freeze()
		_ = m.Insert([]byte("key"), []byte("value"))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestENG_Assert_FrozenMemtableInsert")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestENG_Assert_FrozenMemtableInsert_HappyPath(t *testing.T) {
	m := newMemtable(1024)
	if err := m.Insert([]byte("key"), []byte("value")); err != nil {
		t.Fatalf("Insert on active memtable: %v", err)
	}
	val, ok := m.Get([]byte("key"))
	if !ok {
		t.Fatal("expected to find key")
	}
	if string(val) != "value" {
		t.Fatalf("got %q, want %q", string(val), "value")
	}
}

func TestENG_Assert_BtreeDuplicateKey(t *testing.T) {
	m := newMemtable(1024)
	if err := m.Insert([]byte("key"), []byte("value1")); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if err := m.Insert([]byte("key"), []byte("value2")); err != nil {
		t.Fatalf("second Insert (duplicate): %v", err)
	}
	val, ok := m.Get([]byte("key"))
	if !ok {
		t.Fatal("expected to find key")
	}
	if string(val) != "value2" {
		t.Fatalf("got %q, want %q (last write wins)", string(val), "value2")
	}
}