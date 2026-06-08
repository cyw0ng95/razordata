package lg

import (
	"bytes"
	"compress/gzip"
	"io"
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
	// Use a fresh temp dir, then point the sharedLogger at a
	// guaranteed-missing subdirectory. This is robust against
	// /this/path/... style hardcoded paths that prior test runs
	// may have left behind in the workspace.
	missing := t.TempDir() + "/does/not/exist"
	shared := &sharedLogger{
		dir:      missing,
		baseName: "x.log",
	}
	if _, err := shared.listRotatedFiles(); err == nil {
		t.Errorf("expected error for missing dir %q, got nil", missing)
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

// --- Round 2: more coverage cases ---

// TestSync_RotationWriter_SyncSuccess covers the rotationWriter.Sync
// fast path that delegates to f.Sync. The Sync method on the
// Logger must reach this path when the output is a rotationWriter.
func TestSync_RotationWriter_SyncSuccess(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "sync.log",
		MaxSize:  1 << 20,
	})
	// Force a write so the underlying file exists and is non-empty.
	log.Info("bootstrap")
	// Sync should call the underlying file Sync via the
	// rotationWriter, which delegates to f.Sync(). We can't
	// observe the call directly, but we verify Sync does not
	// return an error and is idempotent.
	if err := log.Sync(); err != nil {
		t.Errorf("first Sync: %v", err)
	}
	if err := log.Sync(); err != nil {
		t.Errorf("second Sync: %v", err)
	}
}

// TestSetOutput_JSON_DisablesRotation covers the SetOutput branch
// that switches to JSON format and disables rotation.
func TestSetOutput_JSON_DisablesRotation(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "json.log",
		MaxSize:  1 << 20,
	})
	shared := log.(*logger).shared
	if shared.rotationFn == nil {
		t.Fatal("expected rotationFn to be set after New with Dir")
	}
	log.SetOutput(&bytes.Buffer{})
	if shared.rotationFn != nil {
		t.Error("SetOutput must clear rotationFn")
	}
}

// TestSetOutput_Nil_DefaultsToStderr covers the nil-writer fallback
// in SetOutput.
func TestSetOutput_Nil_DefaultsToStderr(t *testing.T) {
	log := New(Options{Format: "text", Output: &bytes.Buffer{}})
	// SetOutput(nil) must not panic; it falls back to os.Stderr.
	log.SetOutput(nil)
	log.Info("after-nil")
	// No observable side-effect we can assert beyond no-panic.
}

// TestSetOutput_JSON_Format covers the SetOutput branch that
// rebuilds the handler as slog.NewJSONHandler when shared.format
// is "json". Constructing with Format:"json" and then calling
// SetOutput must rebuild a JSON handler, observable via output.
func TestSetOutput_JSON_Format(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{Format: "json", Output: &buf})
	var buf2 bytes.Buffer
	log.SetOutput(&buf2)
	log.Info("json-msg", "k", "v")
	// JSON output is observable as a non-empty buffer with '{'.
	if buf2.Len() == 0 {
		t.Error("expected non-empty JSON output")
	}
	if buf2.Bytes()[0] != '{' {
		t.Errorf("expected JSON object at start, got %q", buf2.String())
	}
}

// TestListRotatedFiles_IgnoresSubdir covers the IsDir continue
// branch — a subdirectory whose name happens to match the rotated
// prefix must be ignored.
func TestListRotatedFiles_IgnoresSubdir(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "sub.log",
		MaxSize:  1 << 20,
	})
	shared := log.(*logger).shared

	// Create a subdir that matches the rotated prefix.
	if err := os.Mkdir(filepath.Join(dir, "sub.20240101_000000.log"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create a real rotated file for the positive case.
	if err := os.WriteFile(filepath.Join(dir, "sub.20240102_000000.log"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := shared.listRotatedFiles()
	if err != nil {
		t.Fatalf("listRotatedFiles: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 rotated file (subdir ignored), got %d: %v", len(got), got)
	}
}

// TestRotateFile_FailedRename_ReopensCurrent covers the recovery
// branch in rotateFile when os.Rename fails: rotateFile should
// attempt to reopen the current file and update curSize.
func TestRotateFile_FailedRename_ReopensCurrent(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "recov.log",
		MaxSize:  64,
	})
	log.Info("bootstrap")
	// Delete the current file so os.Rename(src, dst) fails.
	if err := os.Remove(filepath.Join(dir, "recov.log")); err != nil {
		t.Fatal(err)
	}

	shared := log.(*logger).shared
	shared.curSize.Store(shared.maxSize)
	if err := shared.rotateFile(); err == nil {
		t.Error("expected error from rotateFile when source file is missing")
	}
	// After the failure, recovery must have created either the
	// original file (recov.log) or a rotated file. We just need
	// at least one entry to be present.
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Error("expected at least one file after recovery attempt")
	}
}

// TestNew_DefaultOptions_Applies covers the default-application
// branch in New: when Output, Format, baseName, maxSize, and
// maxFiles are all zero, sensible defaults must be applied.
func TestNew_DefaultOptions_Applies(t *testing.T) {
	log := New(Options{}) // everything zero / nil
	shared := log.(*logger).shared
	if shared.baseName != "razordata.log" {
		t.Errorf("default baseName: want razordata.log, got %q", shared.baseName)
	}
	if shared.maxSize != 100*1024*1024 {
		t.Errorf("default maxSize: want 100MB, got %d", shared.maxSize)
	}
	if shared.maxFiles != 10 {
		t.Errorf("default maxFiles: want 10, got %d", shared.maxFiles)
	}
	if shared.format != "text" {
		t.Errorf("default format: want text, got %q", shared.format)
	}
}

// TestNew_DirMkdirFails covers the path where Dir is set but
// MkdirAll fails (e.g., a regular file is at the Dir path).
// New must not crash; it falls back to the Output writer.
func TestNew_DirMkdirFails(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file where we'd expect a subdirectory.
	regular := filepath.Join(dir, "subdir")
	if err := os.WriteFile(regular, []byte("not a dir"), 0600); err != nil {
		t.Fatal(err)
	}
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      filepath.Join(regular, "logs"), // MkdirAll fails
		BaseName: "x.log",
		MaxSize:  1 << 20,
	})
	// Output is os.Stderr (default) since Dir failed to create.
	shared := log.(*logger).shared
	if shared.rotationFn != nil {
		t.Error("expected rotationFn to remain nil when Dir creation fails")
	}
	// Logger must still be usable.
	log.Info("after-fail")
}

// TestRotateFile_GzipProducesValidGz verifies that when CompressRotated
// is true (default), rotateFile produces a .log.gz whose contents
// decompress back to the original log lines. (R16-15)
func TestRotateFile_GzipProducesValidGz(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:           slog.LevelInfo,
		Dir:             dir,
		BaseName:        "app.log",
		MaxSize:         1024,
		CompressRotated: true,
	})
	defer func() { _ = log.Sync() }()

	log.Info("first line of log")
	log.Info("second line of log")
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Force rotation by pushing curSize over the limit.
	shared := log.(*logger).shared
	shared.curSize.Store(shared.maxSize + 1)
	if err := shared.rotateFile(); err != nil {
		t.Fatalf("rotateFile: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	var gzFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log.gz") {
			gzFiles = append(gzFiles, e.Name())
		}
	}
	if len(gzFiles) != 1 {
		t.Fatalf("expected exactly one .log.gz after rotation, got %d (entries=%v)", len(gzFiles), entries)
	}
	// The active app.log must still exist (re-opened by rotateFile).
	var foundActive bool
	for _, e := range entries {
		if e.Name() == "app.log" {
			foundActive = true
		}
	}
	if !foundActive {
		t.Errorf("expected app.log to be re-opened after rotation")
	}

	// Decompress and verify content is the two log lines we wrote.
	gzPath := filepath.Join(dir, gzFiles[0])
	gz, err := os.Open(gzPath)
	if err != nil {
		t.Fatalf("open gz: %v", err)
	}
	defer gz.Close()
	gr, err := gzip.NewReader(gz)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read gz: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "first line of log") || !strings.Contains(s, "second line of log") {
		t.Errorf("decompressed content missing expected lines:\n%s", s)
	}
}

// TestRotateFile_NoGzipWhenDisabled verifies CompressRotated=false
// keeps the plain .log rotated file (R16-15).
func TestRotateFile_NoGzipWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:           slog.LevelInfo,
		Dir:             dir,
		BaseName:        "plain.log",
		MaxSize:         1024,
		CompressRotated: false,
	})
	log.Info("a line")
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	shared := log.(*logger).shared
	shared.curSize.Store(shared.maxSize + 1)
	if err := shared.rotateFile(); err != nil {
		t.Fatalf("rotateFile: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	var hasPlainRotated, hasGz bool
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".log.gz"):
			hasGz = true
		case strings.HasPrefix(e.Name(), "plain.") && strings.HasSuffix(e.Name(), ".log"):
			hasPlainRotated = true
		}
	}
	if !hasPlainRotated {
		t.Errorf("expected plain rotated .log file, entries=%v", entries)
	}
	if hasGz {
		t.Errorf("unexpected .log.gz when CompressRotated=false, entries=%v", entries)
	}
}

// TestListRotatedFiles_AcceptsGzSuffix verifies listRotatedFiles
// recognizes both .log and .log.gz rotated files (R16-16).
func TestListRotatedFiles_AcceptsGzSuffix(t *testing.T) {
	dir := t.TempDir()
	// Create two rotated files in the two formats with valid
	// timestamps so the matcher accepts them.
	for _, name := range []string{
		"app.20240101_120000.log",
		"app.20240102_120000.log.gz",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("dummy"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Active log must NOT be matched.
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte("active"), 0o644); err != nil {
		t.Fatalf("write app.log: %v", err)
	}

	s := &sharedLogger{
		dir:      dir,
		baseName: "app.log",
	}
	files, err := s.listRotatedFiles()
	if err != nil {
		t.Fatalf("listRotatedFiles: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected2 rotated files, got %d: %v", len(files), files)
	}
	for _, f := range files {
		base := filepath.Base(f)
		if !strings.HasSuffix(base, ".log") && !strings.HasSuffix(base, ".log.gz") {
			t.Errorf("unexpected file in result: %s", base)
		}
	}
}
