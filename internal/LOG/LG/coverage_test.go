package lg

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSync_DefaultHandler_NoPanic(t *testing.T) {
	// slog.NewTextHandler does not implement the Sync optional interface;
	// Sync must return nil, not panic.
	log := New(Options{Format: "text", Output: &bytes.Buffer{}})
	if err := log.Sync(); err != nil {
		t.Errorf("Sync on text handler should return nil, got %v", err)
	}
}

func TestSync_Idempotent(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "text", Output: &buf})
	for i := 0; i < 5; i++ {
		if err := log.Sync(); err != nil {
			t.Errorf("Sync #%d: %v", i, err)
		}
	}
}

func TestRotateFile_NotActive_NoOp(t *testing.T) {
	// Output is a plain bytes.Buffer (not a *rotationWriter), so rotateFile
	// must return nil without touching any files.
	log := New(Options{Format: "text", Output: &bytes.Buffer{}})
	// Access the private shared to call rotateFile directly.
	shared := log.(*logger).shared
	if err := shared.rotateFile(); err != nil {
		t.Errorf("rotateFile on non-rotation output: want nil, got %v", err)
	}
}

func TestRotateFile_BelowMaxSize_NoOp(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "no-rotate.log",
		MaxSize:  1 << 20, // 1 MB
	})
	log.Info("tiny")

	shared := log.(*logger).shared
	// Size is well below maxSize — rotation must be a no-op.
	if err := shared.rotateFile(); err != nil {
		t.Errorf("rotateFile below maxSize: want nil, got %v", err)
	}

	// Only the current log file should exist; no rotated files.
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if f.Name() != "no-rotate.log" {
			t.Errorf("unexpected file from no-op rotation: %s", f.Name())
		}
	}
}

func TestRotateFile_RenameTargetExists(t *testing.T) {
	// Pre-create a file at the rotated target path so os.Rename fails.
	// rotateFile should attempt to recover by reopening the current path.
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "rename.log",
		MaxSize:  64, // tiny — guarantees rotation on first write
	})
	log.Info("first")

	// Pre-create a "rotated" file with the same name pattern that rotation
	// would use, so rename collides. We can't predict the timestamp, so we
	// instead create an unrelated file in the dir and rely on the failure
	// path attempting to reopen currentPath.
	// Simulate by removing the current file so rename fails.
	if err := os.Remove(filepath.Join(dir, "rename.log")); err != nil {
		t.Fatal(err)
	}

	shared := log.(*logger).shared
	// Force curSize over the limit so rotateFile enters the rename branch.
	shared.curSize.Store(shared.maxSize)
	if err := shared.rotateFile(); err == nil {
		t.Errorf("expected error from rotateFile when source is missing")
	}

	// After recovery attempt, either the original file or the rotated file
	// must exist (both acceptable outcomes from the recovery path).
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Errorf("expected at least one log file after rotation attempt")
	}
}

func TestRotateFile_TriggersOnSize(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "trigger.log",
		MaxSize:  128,
		MaxFiles: 5,
	})
	// Write enough to force rotation (each Info line ~30-40 bytes).
	for i := 0; i < 20; i++ {
		log.Info("line to push us over the size limit")
	}

	files, _ := os.ReadDir(dir)
	hasRotated := false
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "trigger.") && f.Name() != "trigger.log" {
			hasRotated = true
		}
	}
	if !hasRotated {
		t.Errorf("expected at least one rotated file in %v", files)
	}
}

func TestCleanupOldLogs_AtMaxFiles_NoDelete(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "atmax.log",
		MaxSize:  1 << 20,
		MaxFiles: 2,
	})
	shared := log.(*logger).shared
	ext := filepath.Ext(shared.baseName)
	base := strings.TrimSuffix(shared.baseName, ext)

	// Create exactly MaxFiles rotated files (15-char timestamp).
	for i := 0; i < 2; i++ {
		ts := "20240101_00000" + string(rune('0'+i))
		path := filepath.Join(dir, base+"."+ts+ext)
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := shared.cleanupOldLogs(); err != nil {
		t.Errorf("cleanupOldLogs: %v", err)
	}
	// Expect 2 rotated + 1 current = 3 (the logger created BaseName on init).
	files, _ := os.ReadDir(dir)
	if len(files) != 3 {
		t.Errorf("expected 3 files (2 rotated at max + 1 current), got %d: %v", len(files), files)
	}
}

func TestCleanupOldLogs_ExceedsMaxFiles_Deletes(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "over.log",
		MaxSize:  1 << 20,
		MaxFiles: 2,
	})
	shared := log.(*logger).shared
	ext := filepath.Ext(shared.baseName)
	base := strings.TrimSuffix(shared.baseName, ext)

	// Create 4 rotated files; cleanup should leave only MaxFiles=2.
	for i := 0; i < 4; i++ {
		ts := "2024010" + string(rune('1'+i)) + "_000000"
		path := filepath.Join(dir, base+"."+ts+ext)
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := shared.cleanupOldLogs(); err != nil {
		t.Errorf("cleanupOldLogs: %v", err)
	}

	// 2 rotated + 1 current = 3 after cleanup.
	files, _ := os.ReadDir(dir)
	if len(files) != 3 {
		t.Errorf("expected 3 files after cleanup (2 rotated + 1 current), got %d: %v", len(files), files)
	}
}

func TestListRotatedFiles_IgnoresUnrelated(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "app.log",
		MaxSize:  1 << 20,
	})
	shared := log.(*logger).shared

	// Mix: current, rotated matching pattern, unrelated files.
	files := []string{
		"app.log",                 // current — must be ignored
		"app.20240101_000000.log", // valid rotated (15-char timestamp)
		"other.log",               // wrong base
		"app.bogus.log",           // wrong timestamp
		"app.20240102_000000.txt", // wrong extension
		"app.2024.log",            // wrong length
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := shared.listRotatedFiles()
	if err != nil {
		t.Fatalf("listRotatedFiles: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected only 1 rotated file, got %d: %v", len(got), got)
	}
}

func TestListRotatedFiles_DirMissing(t *testing.T) {
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      "/this/path/does/not/exist/anywhere",
		BaseName: "x.log",
		MaxSize:  1 << 20,
	})
	shared := log.(*logger).shared
	if _, err := shared.listRotatedFiles(); err == nil {
		t.Errorf("expected error for missing dir, got nil")
	}
}

func TestRotationWriter_SyncOnNilFile(t *testing.T) {
	rw := &rotationWriter{}
	// No file set — Sync must return nil, not panic.
	if err := rw.Sync(); err != nil {
		t.Errorf("Sync on nil file: want nil, got %v", err)
	}
}

func TestRotationWriter_WriteOnNilFile(t *testing.T) {
	rw := &rotationWriter{}
	n, err := rw.Write([]byte("data"))
	if err == nil {
		t.Errorf("Write on nil file: want error, got nil")
	}
	if n != 0 {
		t.Errorf("Write on nil file: want 0 bytes written, got %d", n)
	}
}

func TestRotationWriter_CloseIdempotent(t *testing.T) {
	rw := &rotationWriter{}
	// Close on never-opened file should be safe.
	if err := rw.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
