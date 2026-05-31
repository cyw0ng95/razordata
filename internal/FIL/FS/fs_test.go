package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewOrCreate(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(filepath.Join(tmp, "db.razor"))
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()
}

func TestNewNonExistent(t *testing.T) {
	_, err := New("/nonexistent/path")
	if err != ErrDoesNotExist {
		t.Fatalf("expected ErrDoesNotExist, got %v", err)
	}
}

func TestPathValidator(t *testing.T) {
	tmp := t.TempDir()
	pv, err := newPathValidator(tmp)
	if err != nil {
		t.Fatalf("newPathValidator: %v", err)
	}

	abs, err := pv.Resolve("foo/bar")
	if err != nil {
		t.Fatalf("Resolve foo/bar: %v", err)
	}
	if abs != filepath.Join(tmp, "foo", "bar") {
		t.Fatalf("expected %s, got %s", filepath.Join(tmp, "foo", "bar"), abs)
	}

	if _, err := pv.Resolve("../etc/passwd"); err != ErrPathTraversal {
		t.Fatalf("expected ErrPathTraversal for ../etc/passwd, got %v", err)
	}
	if _, err := pv.Resolve("foo/../../etc/passwd"); err != ErrPathTraversal {
		t.Fatalf("expected ErrPathTraversal for foo/../../etc/passwd, got %v", err)
	}
}

func TestCreateAndOpen(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	h, err := fm.Create("testfile")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.Close()

	h, err = fm.Open("testfile")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if h.FD < 0 {
		t.Fatalf("FD should be non-negative")
	}
	h.Close()
}

func TestCreateAlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	h, err := fm.Create("afile")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.Close()

	_, err = fm.Create("afile")
	if err != ErrAlreadyExists {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestRemove(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	h, err := fm.Create("to-remove")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.Close()

	if err := fm.Remove("to-remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err = fm.Open("to-remove")
	if err != ErrDoesNotExist {
		t.Fatalf("expected ErrDoesNotExist after Remove, got %v", err)
	}
}

func TestMkdirAll(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	if err := fm.MkdirAll("a/b/c"); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	info, err := os.Stat(filepath.Join(tmp, "a", "b", "c"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("expected perm 0700, got %o", info.Mode().Perm())
	}
}

func TestList(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	for _, name := range []string{"aaa", "bbb", "ccc"} {
		h, err := fm.Create(name)
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		h.Close()
	}

	names, err := fm.List("*")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(names), names)
	}
}

func TestSyncDir(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	if err := fm.SyncDir("."); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}
}

func TestClose(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}

	h, err := fm.Create("f")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.Close()

	if err := fm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	cases := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"traversal up", "../foo", ErrPathTraversal},
		{"deep traversal", "a/b/../../../etc/passwd", ErrPathTraversal},
		{"sibling escape", "foo/../../bar", ErrPathTraversal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fm.Open(tc.path)
			if err != tc.wantErr {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestSymlinkBlocked(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	linkPath := filepath.Join(tmp, "link")
	if err := os.Symlink("/etc/passwd", linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err = fm.Open("link")
	if !errorsIs(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
}

func TestDoubleClose(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	h, err := fm.Create("f")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.Close()
	if err := h.Close(); err != nil {
		t.Fatalf("Second Close should be no-op, got %v", err)
	}
}

func errorsIs(got, want error) bool {
	if got == nil {
		return want == nil
	}
	return strings.Contains(got.Error(), want.Error())
}
