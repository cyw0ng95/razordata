package SY

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// TestValidateOptions_Bounds covers every numeric field at its min,
// mid, and max acceptable values plus one value just outside each
// bound. Each row asserts the error wraps ErrInvalidOptions and
// mentions the field name.
func TestValidateOptions_Bounds(t *testing.T) {
	dir := t.TempDir()
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cases := []struct {
		name    string
		mutate  func(o *AP.Options)
		wantErr string
	}{
		{"all defaults", func(o *AP.Options) { o.Dir = absDir }, ""},
		{"Dir empty", func(o *AP.Options) { o.Dir = "" }, "Dir is required"},
		{"Dir relative resolves", func(o *AP.Options) {
			o.Dir = "."
			o.PageSize = 4096
		}, ""},
		{"PageSize too small", func(o *AP.Options) { o.PageSize = 512 }, "PageSize=512"},
		{"PageSize too large", func(o *AP.Options) { o.PageSize = 131072 }, "PageSize=131072"},
		{"PageSize non power of two", func(o *AP.Options) { o.PageSize = 3000 }, "PageSize=3000"},
		{"PageSize min OK", func(o *AP.Options) { o.PageSize = 1024 }, ""},
		{"PageSize max OK", func(o *AP.Options) { o.PageSize = 65536 }, ""},
		{"MemTableSize too small", func(o *AP.Options) { o.MemTableSize = 1 << 19 }, "MemTableSize="},
		{"MemTableSize too large", func(o *AP.Options) { o.MemTableSize = 1 << 31 }, "MemTableSize="},
		{"MemTableSize min OK", func(o *AP.Options) { o.MemTableSize = 1 << 20 }, ""},
		{"MemTableSize max OK", func(o *AP.Options) { o.MemTableSize = 1 << 30 }, ""},
		{"BufferPoolMB too small", func(o *AP.Options) { o.BufferPoolMB = 63 }, "BufferPoolMB=63"},
		{"BufferPoolMB too large", func(o *AP.Options) { o.BufferPoolMB = 4097 }, "BufferPoolMB=4097"},
		{"BufferPoolMB min OK", func(o *AP.Options) { o.BufferPoolMB = 64 }, ""},
		{"BufferPoolMB max OK", func(o *AP.Options) { o.BufferPoolMB = 4096 }, ""},
		{"WALSizeMB too small", func(o *AP.Options) { o.WALSizeMB = 15 }, "WALSizeMB=15"},
		{"WALSizeMB too large", func(o *AP.Options) { o.WALSizeMB = 257 }, "WALSizeMB=257"},
		{"WALSizeMB min OK", func(o *AP.Options) { o.WALSizeMB = 16 }, ""},
		{"WALSizeMB max OK", func(o *AP.Options) { o.WALSizeMB = 256 }, ""},
		{"MaxLevel too small", func(o *AP.Options) { o.MaxLevel = 2 }, "MaxLevel=2"},
		{"MaxLevel too large", func(o *AP.Options) { o.MaxLevel = 11 }, "MaxLevel=11"},
		{"MaxLevel min OK", func(o *AP.Options) { o.MaxLevel = 3 }, ""},
		{"MaxLevel max OK", func(o *AP.Options) { o.MaxLevel = 10 }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &AP.Options{
				PageSize:     AP.DefaultPageSize,
				MemTableSize: AP.DefaultMemTableSize,
				BufferPoolMB: AP.DefaultBufferPoolMB,
				WALSizeMB:    AP.DefaultWALSizeMB,
				MaxLevel:     AP.DefaultMaxLevel,
				Dir:          absDir,
			}
			tc.mutate(o)
			err := validateOptions(o)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateOptions: unexpected error: %v", err)
				}
				return
			}
			if !AP.IsKind(err, AP.KindInvalidOptions) {
				t.Fatalf("validateOptions: want ErrInvalidOptions, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateOptions: error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestValidateOptions_RelativeDirResolves verifies that a relative
// Dir is converted to an absolute path in o.Dir. We mutate first,
// then check.
func TestValidateOptions_RelativeDirResolves(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	// Make sure the cwd contains a directory we can refer to.
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	o := &AP.Options{
		Dir:          "sub",
		PageSize:     4096,
		MemTableSize: 1 << 20,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
	}
	if err := validateOptions(o); err != nil {
		t.Fatalf("validateOptions: %v", err)
	}
	if !filepath.IsAbs(o.Dir) {
		t.Fatalf("expected absolute Dir after validate, got %q", o.Dir)
	}
}

// TestValidateOptions_PathIsFile verifies that a Dir which points at
// a regular file (not a directory) is rejected.
func TestValidateOptions_PathIsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	o := &AP.Options{
		Dir:          file,
		PageSize:     4096,
		MemTableSize: 1 << 20,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
	}
	err := validateOptions(o)
	if !AP.IsKind(err, AP.KindInvalidOptions) {
		t.Fatalf("want ErrInvalidOptions, got %v", err)
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error %q does not mention 'not a directory'", err.Error())
	}
}

// TestValidateOptions_NilOptions covers the defensive nil check. This
// is a runtime guard for callers that pass a nil pointer through
// reflect/interface boundaries; the normal Open path does not.
func TestValidateOptions_NilOptions(t *testing.T) {
	err := validateOptions(nil)
	if !AP.IsKind(err, AP.KindInvalidOptions) {
		t.Fatalf("want ErrInvalidOptions, got %v", err)
	}
}

// TestValidateOptions_NonexistentDir is a sanity check: a Dir that
// does not exist is allowed (Open will create it via CreateIfMissing).
// validateOptions must not reject it on the Stat check.
func TestValidateOptions_NonexistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	o := &AP.Options{
		Dir:          dir,
		PageSize:     4096,
		MemTableSize: 1 << 20,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
	}
	if err := validateOptions(o); err != nil {
		t.Fatalf("validateOptions on nonexistent dir: %v", err)
	}
}

// TestIsPowerOfTwo sanity-checks the helper used by validateOptions.
func TestIsPowerOfTwo(t *testing.T) {
	cases := map[int]bool{
		0:      false,
		1:      true,
		2:      true,
		3:      false,
		4:      true,
		1024:   true,
		4096:   true,
		65536:  true,
		65537:  false,
		131072: true,
		131073: false,
		-1:     false,
		-2:     false,
	}
	for in, want := range cases {
		if got := isPowerOfTwo(in); got != want {
			t.Errorf("isPowerOfTwo(%d) = %v, want %v", in, got, want)
		}
	}
}
