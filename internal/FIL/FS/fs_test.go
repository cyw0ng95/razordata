package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/LOG/LG"
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

func TestNewFileNotDir(t *testing.T) {
	tmp := t.TempDir()
	file := tmp + "/afile"
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := New(file)
	if err == nil {
		t.Fatalf("expected error for file-as-root")
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

func TestValidate(t *testing.T) {
	tmp := t.TempDir()
	pv, err := newPathValidator(tmp)
	if err != nil {
		t.Fatalf("newPathValidator: %v", err)
	}

	cases := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"absolute path", "/etc/passwd", ErrNotAbsolute},
		{"traversal", "../foo", ErrPathTraversal},
		{"deep traversal", "a/b/../../../x", ErrPathTraversal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pv.Validate(tc.path)
			if err != tc.wantErr {
				t.Fatalf("Validate(%q): got %v, want %v", tc.path, err, tc.wantErr)
			}
		})
	}

	// Valid path: no error.
	if err := pv.Validate("foo/bar"); err != nil {
		t.Fatalf("Validate(valid): unexpected error %v", err)
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

func TestRemoveMissing(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	err = fm.Remove("nonexistent")
	if err != ErrDoesNotExist {
		t.Fatalf("expected ErrDoesNotExist, got %v", err)
	}
}

func TestRemovePathTraversal(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	err = fm.Remove("../foo")
	if err != ErrPathTraversal {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
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

func TestSyncDirTwice(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	// Sync same directory twice to exercise cached FD path.
	for i := 0; i < 2; i++ {
		if err := fm.SyncDir("."); err != nil {
			t.Fatalf("SyncDir round %d: %v", i, err)
		}
	}
}

func TestSyncDirPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	err = fm.SyncDir("../foo")
	if err != ErrPathTraversal {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}
}

func TestOpenMissingFile(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	_, err = fm.Open("nonexistent")
	if err != ErrDoesNotExist {
		t.Fatalf("expected ErrDoesNotExist, got %v", err)
	}
}

func TestOpenPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	_, err = fm.Open("../foo")
	if err != ErrPathTraversal {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}
}

func TestOpenSymlinkInPath(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	linkPath := filepath.Join(tmp, "link")
	if err := os.Symlink("/nonexistent", linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err = fm.Open("link")
	if err != ErrSymlink {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
}

func TestCreatePathTraversal(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	_, err = fm.Create("../foo")
	if err != ErrPathTraversal {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}
}

func TestListNoMatch(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	names, err := fm.List("nothing_*")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(names))
	}
}

func TestListSubDir(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	fm.MkdirAll("sub")
	for _, name := range []string{"sub/a", "sub/b"} {
		h, err := fm.Create(name)
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		h.Close()
	}

	names, err := fm.List("sub/*")
	if err != nil {
		t.Fatalf("List sub/*: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(names), names)
	}
}

func TestMkdirAllPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	defer fm.Close()

	err = fm.MkdirAll("../foo")
	if err != ErrPathTraversal {
		t.Fatalf("expected ErrPathTraversal, got %v", err)
	}
}

func TestCloseWithHandles(t *testing.T) {
	tmp := t.TempDir()
	fm, err := NewOrCreate(tmp)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}

	h1, err := fm.Create("f1")
	if err != nil {
		t.Fatalf("Create f1: %v", err)
	}
	h2, err := fm.Create("f2")
	if err != nil {
		t.Fatalf("Create f2: %v", err)
	}

	h1.Close()
	h2.Close()

	if err := fm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
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

// TestSyncDirCached tests SyncDir when dir FD is cached.
func TestSyncDirCached(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer fm.Close()

	if err := os.MkdirAll(filepath.Join(tmp, "subdir"), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// SyncDir expects relative path
	if err := fm.SyncDir("subdir"); err != nil {
		t.Errorf("SyncDir: %v", err)
	}

	// Second call uses cached FD
	if err := fm.SyncDir("subdir"); err != nil {
		t.Errorf("SyncDir cached: %v", err)
	}
}

// TestSyncDirNewFD tests SyncDir when dir FD is not cached.
func TestSyncDirNewFD(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer fm.Close()

	if err := os.MkdirAll(filepath.Join(tmp, "newdir"), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := fm.SyncDir("newdir"); err != nil {
		t.Errorf("SyncDir: %v", err)
	}
}

// TestSyncDirError tests SyncDir error path.
func TestSyncDirError(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer fm.Close()

	invalidPath := filepath.Join(tmp, "nonexistent")
	if err := fm.SyncDir(invalidPath); err == nil {
		t.Error("expected error for non-existent path")
	}
}

// TestNewValidDir tests New with existing directory.
func TestNewValidDir(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fm.Close()
}

// TestNewOrCreateReuse tests NewOrCreate when directory exists.
func TestNewOrCreateReuse(t *testing.T) {
	tmp := t.TempDir()

	fm, err := NewOrCreate(tmp, nil)
	if err != nil {
		t.Fatalf("NewOrCreate: %v", err)
	}
	fm.Close()

	fm2, err := NewOrCreate(tmp, nil)
	if err != nil {
		t.Fatalf("NewOrCreate reuse: %v", err)
	}
	fm2.Close()
}

// TestOpenMissing tests Open with non-existent file.
func TestOpenMissing(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer fm.Close()

	_, err = fm.Open("nonexistent.txt")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestCreateThenOpen tests Create followed by Open.
func TestCreateThenOpen(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer fm.Close()

	handle, err := fm.Create("created.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	handle.Close()

	path := filepath.Join(tmp, "created.txt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("file not created")
	}

	handle2, err := fm.Open("created.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	handle2.Close()
}

// TestRemoveFile tests Remove with existing file.
func TestRemoveFile(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer fm.Close()

	path := filepath.Join(tmp, "to_remove.txt")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fm.Remove("to_remove.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still exists after Remove")
	}
}

// TestListPattern tests List with glob pattern.
func TestListPattern(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer fm.Close()

	for i := 1; i <= 3; i++ {
		path := filepath.Join(tmp, fmt.Sprintf("file%d.txt", i))
		os.WriteFile(path, []byte("content"), 0600)
	}

	files, err := fm.List("*.txt")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) < 3 {
		t.Errorf("expected at least 3 files, got %d", len(files))
	}
}

// TestFirstLoggerNilFS tests firstLogger with nil/empty input.
func TestFirstLoggerNilFS(t *testing.T) {
	result := firstLogger(nil)
	if result != nil {
		t.Error("expected nil for nil input")
	}

	result = firstLogger([]lg.Logger{})
	if result != nil {
		t.Error("expected nil for empty slice")
	}
}

// TestPathValidatorOK tests path validation with valid paths.
func TestPathValidatorOK(t *testing.T) {
	pv, err := newPathValidator("/tmp")
	if err != nil {
		t.Fatalf("newPathValidator: %v", err)
	}

	_, err = pv.Resolve("subdir/file.txt")
	if err != nil {
		t.Errorf("expected valid, got %v", err)
	}
}

// TestPathValidatorBad tests path validation with invalid paths.
func TestPathValidatorBad(t *testing.T) {
	pv, err := newPathValidator("/tmp")
	if err != nil {
		t.Fatalf("newPathValidator: %v", err)
	}

	_, err = pv.Resolve("/etc/passwd")
	if err == nil {
		t.Error("expected error for path outside root")
	}
}

// TestCloseSafe tests that Close can be called multiple times.
func TestCloseSafe(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := fm.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	if err := fm.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestCloseWithOpenHandles tests Close when handles exist.
func TestCloseWithOpenHandles(t *testing.T) {
	tmp := t.TempDir()
	fm, err := New(tmp, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	fm.Create("test.txt")

	if err := fm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
