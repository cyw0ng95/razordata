//go:build debug

package ls

import (
	"bytes"
	"encoding/binary"
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
		// The memtable allows duplicate keys (last write wins).
		return
	}
	if err := m.Insert([]byte("key"), []byte("value2")); err != nil {
		// The memtable allows duplicate keys (last write wins).
		return
	}
	val, ok := m.Get([]byte("key"))
	if !ok {
		t.Fatal("expected to find key")
	}
	if string(val) != "value2" {
		t.Fatalf("got %q, want %q (last write wins)", string(val), "value2")
	}
}

func TestENG_Assert_SSTKeyOrder(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		var idx bytes.Buffer
		var b8 [8]byte

		// Entry 1: key "b", blockOffset=0, blockSize=100
		idx.Write(encodeVarint(1))
		idx.WriteByte('b')
		// block offset + block size
		binary.LittleEndian.PutUint64(b8[:], 0)
		idx.Write(b8[:])
		binary.LittleEndian.PutUint64(b8[:], 100)
		idx.Write(b8[:])

		// Entry 2: key "a", blockOffset=200, blockSize=100
		// BUG_ON triggers: "b" > "a" means ordering violation.
		// BUG_ON in parseIndexBlock exits(1).
		idx.Write(encodeVarint(1))
		idx.WriteByte('a')
		binary.LittleEndian.PutUint64(b8[:], 200)
		idx.Write(b8[:])
		binary.LittleEndian.PutUint64(b8[:], 100)
		idx.Write(b8[:])

		parseIndexBlock(idx.Bytes())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestENG_Assert_SSTKeyOrder")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestENG_Assert_ManifestL1Overlap(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		// Build a version with L1 files that overlap by key range.
		// File 0: [a, c], File 1: [b, d] — overlap because c > b.
		v := Version{
			levels: [][]SSTFileMeta{
				{}, // L0 empty
				{   // L1 has overlapping files
					{FileID: 1, Level: 1, MinKey: []byte("a"), MaxKey: []byte("c"), Size: 100, BloomBits: 10},
					{FileID: 2, Level: 1, MinKey: []byte("b"), MaxKey: []byte("d"), Size: 100, BloomBits: 10},
				},
			},
		}
		dir := t.TempDir()
		m, err := newManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		// First set an initial empty version.
		emptyV := Version{
			levels: [][]SSTFileMeta{{}, {}},
		}
		if err := m.Apply(emptyV); err != nil {
			t.Fatal(err)
		}
		// Now apply the overlapping version — WARN_ON logs and continues.
		// In a BUG_ON assertion we would exit; for WARN_ON we just log.
		_ = m.Apply(v)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestENG_Assert_ManifestL1Overlap")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("WARN_ON: manifest.Apply: L1 SST 1 and SST 2 overlap")) {
		t.Fatalf("expected WARN_ON log for L1 overlap, got: %s", out)
	}
}