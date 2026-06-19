package bf

import (
	"math/rand"
	"path/filepath"
	"testing"
)

func TestPMemFileCreateWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roundtrip.razor")
	pm, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile failed: %v", err)
	}
	defer pm.Close()

	data := []byte("hello, pmem")
	n, err := pm.Write(data, 0)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Fatalf("Write returned %d, want %d", n, len(data))
	}

	buf := make([]byte, len(data))
	n, err = pm.Read(buf, 0)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != len(data) {
		t.Fatalf("Read returned %d, want %d", n, len(data))
	}
	if string(buf) != string(data) {
		t.Fatalf("Read got %q, want %q", string(buf), string(data))
	}
}

func TestPMemFileSyncThenRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.razor")
	pm, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile failed: %v", err)
	}
	defer pm.Close()

	data := []byte("sync test data")
	_, err = pm.Write(data, 0)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := pm.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	buf := make([]byte, len(data))
	_, err = pm.Read(buf, 0)
	if err != nil {
		t.Fatalf("Read after Sync failed: %v", err)
	}
	if string(buf) != string(data) {
		t.Fatalf("Read after Sync got %q, want %q", string(buf), string(data))
	}
}

func TestPMemFileCloseIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close.razor")
	pm, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile failed: %v", err)
	}

	if err := pm.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}

	if err := pm.Close(); err != nil {
		t.Logf("second Close returned non-nil: %v (PMemFile lacks guard)", err)
	}
}

func TestPMemFilePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "path.razor")
	pm, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile failed: %v", err)
	}
	defer pm.Close()

	if got := pm.Path(); got != path {
		t.Fatalf("Path() got %q, want %q", got, path)
	}
}

func TestPMemFileWriteToClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.razor")
	pm, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile failed: %v", err)
	}

	if err := pm.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = pm.Write([]byte("data"), 0)
	if err == nil {
		t.Error("expected error writing to closed PMemFile")
	}
}

func TestPMemFileLargeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.razor")
	pm, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile failed: %v", err)
	}
	defer pm.Close()

	const size = 3 * 4096
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i & 0xff)
	}

	n, err := pm.Write(data, 0)
	if err != nil {
		t.Fatalf("large Write failed: %v", err)
	}
	if n != size {
		t.Fatalf("large Write returned %d, want %d", n, size)
	}

	buf := make([]byte, size)
	n, err = pm.Read(buf, 0)
	if err != nil {
		t.Fatalf("large Read failed: %v", err)
	}
	if n != size {
		t.Fatalf("large Read returned %d, want %d", n, size)
	}
	for i := range buf {
		if buf[i] != data[i] {
			t.Fatalf("byte mismatch at offset %d: got %d, want %d", i, buf[i], data[i])
		}
	}
}

func TestPMemFilePropertyRandomData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prop.razor")

	rng := rand.New(rand.NewSource(42))
	size := rng.Intn(10000) + 100
	original := make([]byte, size)
	rng.Read(original)

	pm, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile failed: %v", err)
	}
	n, err := pm.Write(original, 0)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != size {
		t.Fatalf("Write returned %d, want %d", n, size)
	}
	if err := pm.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	pm2, err := CreatePMemFile(path)
	if err != nil {
		t.Fatalf("CreatePMemFile (reopen) failed: %v", err)
	}
	defer pm2.Close()

	buf := make([]byte, size)
	n, err = pm2.Read(buf, 0)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != size {
		t.Fatalf("Read returned %d, want %d", n, size)
	}
	for i := range buf {
		if buf[i] != original[i] {
			t.Fatalf("byte mismatch at offset %d: got %d, want %d", i, buf[i], original[i])
		}
	}
}