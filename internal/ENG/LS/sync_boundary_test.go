//go:build !ls_probe

package ls

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"
)

// TestSync_AllSizesRoundTrip is the regression test for
// REQ000347 (iter-26). Pre-fix, any row whose value was
// larger than the memtable's "first block" capacity was
// silently dropped between Insert and the first Sync. The
// table-driven sweep covers 0 bytes through 1 MiB.
func TestSync_AllSizesRoundTrip(t *testing.T) {
	sizes := []int{
		0, 1, 16, 256, 1024,
		4096, 8192, 16384, 65536,
		1 << 20, // 1 MiB
	}
	for _, n := range sizes {
		t.Run("size="+itoaSize(n), func(t *testing.T) {
			dir := t.TempDir()
			eng, err := Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			key := []byte("k")
			want := bytes.Repeat([]byte("x"), n)
			if err := eng.Insert(key, want); err != nil {
				t.Fatalf("Insert: %v", err)
			}
			if err := eng.Sync(); err != nil {
				t.Fatalf("Sync: %v", err)
			}
			got, err := eng.Get(key)
			if err != nil {
				t.Fatalf("Get after Sync: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("size=%d: got %d bytes, want %d", n, len(got), n)
			}
		})
	}
}

// TestSync_ConcurrentInserters stresses the fix under
// concurrent writes. 4 goroutines each insert 20 rows of
// mixed sizes, then a final Sync must surface every row.
// Sizes are bounded to keep the test under one second on
// developer hardware.
func TestSync_ConcurrentInserters(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	const goroutines = 4
	const rowsPerG = 20

	var wg sync.WaitGroup
	// sizeFor returns the byte length for row i. Pulled out
	// so the insert side and the verify side use exactly the
	// same formula — a divergence here produced an
	// off-by-size failure during the first revision of this
	// test.
	sizeFor := func(i int) int {
		switch {
		case i == rowsPerG-1:
			return 64 << 10 // 64 KiB
		case i%5 == 0:
			return 4096
		default:
			return 16
		}
	}

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < rowsPerG; i++ {
				key := []byte("g" + itoaSize(gid) + "-k" + itoaSize(i))
				val := bytes.Repeat([]byte("y"), sizeFor(i))
				if err := eng.Insert(key, val); err != nil {
					t.Errorf("Insert %s: %v", key, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if err := eng.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for g := 0; g < goroutines; g++ {
		for i := 0; i < rowsPerG; i++ {
			key := []byte("g" + itoaSize(g) + "-k" + itoaSize(i))
			want := bytes.Repeat([]byte("y"), sizeFor(i))
			got, err := eng.Get(key)
			if err != nil {
				t.Errorf("Get %s after Sync: %v", key, err)
				continue
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Get %s: got %d bytes, want %d", key, len(got), sizeFor(i))
			}
		}
	}
}

// TestSync_MultipleFlushesBoundaries verifies that the
// "always L0" manifest update (REQ000347, second part) does
// not regress across multiple flushes. We insert 5 keys,
// sync, then insert 5 more, sync, and assert all 10 are
// readable.
func TestSync_MultipleFlushesBoundaries(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	first := []string{"a", "b", "c", "d", "e"}
	second := []string{"f", "g", "h", "i", "j"}
	val := bytes.Repeat([]byte("v"), 1024)
	for _, k := range first {
		if err := eng.Insert([]byte(k), val); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.Sync(); err != nil {
		t.Fatal(err)
	}
	for _, k := range second {
		if err := eng.Insert([]byte(k), val); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.Sync(); err != nil {
		t.Fatal(err)
	}
	all := append(append([]string{}, first...), second...)
	for _, k := range all {
		got, err := eng.Get([]byte(k))
		if err != nil {
			t.Errorf("Get %s: %v", k, err)
			continue
		}
		if !bytes.Equal(got, val) {
			t.Errorf("Get %s: %d bytes, want %d", k, len(got), len(val))
		}
	}
}

func itoaSize(n int) string {
	if n == 0 {
		return "0"
	}
	const units = "KMG"
	div := 1
	suf := ""
	for _, u := range units {
		if n/div < 1024 {
			suf = string(u)
			break
		}
		div *= 1024
	}
	if suf == "" {
		return "L"
	}
	if n%div == 0 {
		return itoaSizeInt(n/div) + suf
	}
	return itoaSizeInt(n/div) + "." + itoaSizeInt((n%div)*10/div) + suf
}

func itoaSizeInt(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
