package ls

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVFS_InMemPutGet(t *testing.T) {
	fs := newInMemFS()
	dir := "/testdir"
	if err := fs.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create and write
	f, err := fs.Create(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := f.Write([]byte("hello vfs"))
	if err != nil || n != 9 {
		t.Fatalf("Write(%d, %v)", n, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Read back
	f2, err := fs.Open(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	data, err := f2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello vfs" {
		t.Fatalf("got %q, want %q", data, "hello vfs")
	}

	// Stat
	fi, err := fs.Stat(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 9 {
		t.Fatalf("Stat.Size = %d, want 9", fi.Size())
	}

	// Remove
	if err := fs.Remove(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Open(filepath.Join(dir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist, got %v", err)
	}
}

func TestVFS_CrashRecovery(t *testing.T) {
	fs := newInMemFS()
	fs.InjectFailure(os.ErrNotExist, 0.5) // fail 50% of Opens

	// Write should succeed
	f, err := fs.Create("/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("data"))
	f.Close()

	// Read might fail due to injection
	f2, err := fs.Open("/test.txt")
	if err == nil {
		f2.Close()
		t.Log("read succeeded despite injection")
	} else {
		t.Logf("read correctly failed: %v", err)
	}

	// Reset injection
	fs.InjectFailure(nil, 0)
	f3, err := fs.Open("/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := f3.ReadAll()
	if string(data) != "data" {
		t.Fatalf("got %q", data)
	}
}

func TestVFS_OsFS_OpenCreate(t *testing.T) {
	fs := DefaultFS()
	tmpDir := t.TempDir()

	path := filepath.Join(tmpDir, "test.txt")
	f, err := fs.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := f.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write(%d, %v)", n, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	f2, err := fs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	data, err := f2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("got %q, want %q", data, "hello")
	}
}

func TestVFS_OsFS_EdgeCases(t *testing.T) {
	fs := DefaultFS()

	// Open non-existent file returns os.ErrNotExist
	if _, err := fs.Open("/nonexistent/path/file.txt"); !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist, got %v", err)
	}

	// Stat non-existent file returns os.ErrNotExist
	if _, err := fs.Stat("/nonexistent/path/file.txt"); !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist, got %v", err)
	}

	// Remove non-existent file returns os.ErrNotExist
	if err := fs.Remove("/nonexistent/path/file.txt"); !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist, got %v", err)
	}
}

func TestVFS_InMemFS_ReadFileWriteFile(t *testing.T) {
	fs := newInMemFS()

	if err := fs.WriteFile("/data.txt", []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile("/data.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "content" {
		t.Fatalf("got %q, want %q", data, "content")
	}
}

func TestVFS_InMemFS_Rename(t *testing.T) {
	fs := newInMemFS()

	// Create source
	f, err := fs.Create("/src.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("renamed"))
	f.Close()

	// Rename
	if err := fs.Rename("/src.txt", "/dst.txt"); err != nil {
		t.Fatal(err)
	}

	// Source should be gone
	if _, err := fs.Open("/src.txt"); !os.IsNotExist(err) {
		t.Fatalf("source should not exist")
	}

	// Destination should exist with content
	f2, err := fs.Open("/dst.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	data, err := f2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "renamed" {
		t.Fatalf("got %q, want %q", data, "renamed")
	}
}

func TestVFS_InMemFS_SymlinkNotSupported(t *testing.T) {
	fs := newInMemFS()
	err := fs.Symlink("/target", "/link")
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestVFS_OsFS_MkdirAll(t *testing.T) {
	fs := DefaultFS()
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "a", "b", "c")
	if err := fs.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	fi, err := fs.Stat(subDir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestVFS_OsFS_ReadFileWriteFile(t *testing.T) {
	fs := DefaultFS()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "data.txt")

	if err := fs.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("got %q, want %q", data, "hello world")
	}
}

func TestVFS_InMemFS_WriteAt(t *testing.T) {
	fs := newInMemFS()
	f, err := fs.Create("/test.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte("ABCD"), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte("XYZ"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	f2, err := fs.Open("/test.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	data, err := f2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ABXYZ" {
		t.Fatalf("got %q, want %q", data, "ABXYZ")
	}
}
