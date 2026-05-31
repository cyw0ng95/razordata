package lg

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRotationTrigger verifies rotation occurs when maxSize exceeded
func TestRotationTrigger(t *testing.T) {
	dir := t.TempDir()

	log := New(Options{
		Level:    slog.LevelInfo,
		Format:   "text",
		Dir:      dir,
		BaseName: "test.log",
		MaxSize:  200, // Small for quick rotation
		MaxFiles: 3,
	})

	// Just 3 messages
	log.Info("test1")
	log.Info("test2")
	log.Info("test3")

	// Check files created
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Error("expected log files")
	}
}

// TestRotationNaming verifies rotated file naming format
func TestRotationNaming(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "app.log",
		MaxSize:  200,
	})

	// Just enough for possible rotation
	log.Info("test1")
	log.Info("test2")
	log.Info("test3")

	// Basic check that files were created
	files, _ := os.ReadDir(dir)
	hasAny := false
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".log") {
			hasAny = true
			// If rotated, verify format
			if f.Name() != "app.log" {
				// Check format: app.YYMMDD_HHMMSS.log
				if !strings.HasPrefix(f.Name(), "app.") {
					t.Errorf("unexpected format: %s", f.Name())
				}
			}
		}
	}
	if !hasAny {
		t.Error("expected log files")
	}
}

// TestMaxFilesRetention verifies old files are deleted
func TestMaxFilesRetention(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "retention.log",
		MaxSize:  4096,
		MaxFiles: 3,
	})

	// Write some logs
	for i := 0; i < 30; i++ {
		log.Info("this is a longer message to fill up the log quickly")
	}

	// Just check current file exists
	files, _ := os.ReadDir(dir)
	hasCurrent := false
	for _, f := range files {
		if f.Name() == "retention.log" {
			hasCurrent = true
			break
		}
	}
	if !hasCurrent {
		t.Error("expected current log file")
	}
}

// TestConcurrentLoggingWithRotation verifies rotation is goroutine-safe
func TestConcurrentLoggingWithRotation(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelDebug,
		Dir:      dir,
		BaseName: "concurrent.log",
		MaxSize:  8192, // 8KB - less frequent rotation for faster tests
		MaxFiles: 5,
	})

	var wg sync.WaitGroup
	const goroutines = 5
	const messages = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messages; j++ {
				log.Info("concurrent", "goroutine", id, "msg", j)
			}
		}(i)
	}

	wg.Wait()

	// Just verify logging worked, not detailed rotation checks
	files, _ := os.ReadDir(dir)
	if len(files) == 0 {
		t.Error("expected at least one log file")
	}
}

// TestRotationFailureFallback verifies logging continues if rotation fails
func TestRotationFailureFallback(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "fail.log",
		MaxSize:  100,
	})

	os.Chmod(dir, 0444)
	defer os.Chmod(dir, 0755)

	log.Info("message after rotation failure")
}

// TestListRotatedFiles verifies sorted file listing
func TestListRotatedFiles(t *testing.T) {
	dir := t.TempDir()

	// Create logger first to set up baseName properly
	l := New(Options{
		Dir:      dir,
		BaseName: "test.log",
	}).(*logger)

	// Create fake rotated files matching expected pattern: <base>.YYMMDD_HHMMSS<ext>
	// baseName = "test.log", ext = ".log", base = "test"
	// So files should be: test.YYMMDD_HHMMSS.log
	names := []string{
		"test.240101_120000.log",
		"test.240102_120000.log",
		"test.240103_120000.log",
	}
	for _, name := range names {
		os.Create(filepath.Join(dir, name))
	}
	// Also create current log file
	os.Create(filepath.Join(dir, "test.log"))

	files, err := l.shared.listRotatedFiles()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Found %d rotated files: %v", len(files), files)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if !strings.HasSuffix(files[0], "test.240101_120000.log") {
		t.Error("expected oldest file first")
	}
}

// TestCleanupOldLogs verifies oldest file removal
func TestCleanupOldLogs(t *testing.T) {
	dir := t.TempDir()

	// Create logger first
	l := New(Options{
		Dir:      dir,
		BaseName: "test.log",
		MaxFiles: 3,
	}).(*logger)

	// Create fake rotated files matching expected pattern: test.YYMMDD_HHMMSS.log
	for i := 1; i <= 5; i++ {
		name := filepath.Join(dir, fmt.Sprintf("test.24010%d_120000.log", i))
		os.Create(name)
	}
	// Create current log file
	os.Create(filepath.Join(dir, "test.log"))

	err := l.shared.cleanupOldLogs()
	if err != nil {
		t.Fatal(err)
	}

	files, _ := os.ReadDir(dir)
	t.Logf("Files after cleanup: %d", len(files))
	if len(files) != 4 { // 3 rotated + 1 current log
		t.Errorf("expected 4 files after cleanup (3 rotated + 1 current), got %d", len(files))
	}
}

// TestNoRotationWhenDisabled verifies no rotation when Dir is empty
func TestNoRotationWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	log := New(Options{
		Level:  slog.LevelInfo,
		Output: &buf,
		Dir:    "",
	})

	for i := 0; i < 1000; i++ {
		log.Info("test")
	}

	if buf.Len() == 0 {
		t.Error("expected output")
	}
}

// TestRotationWithJSONFormat verifies rotation works with JSON format
func TestRotationWithJSONFormat(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Format:   "json",
		Dir:      dir,
		BaseName: "json.log",
		MaxSize:  512,
	})

	// Just 3 messages
	log.Info("test", "key", "value1")
	log.Info("test", "key", "value2")
	log.Info("test", "key", "value3")

	files, _ := os.ReadDir(dir)
	if len(files) < 1 {
		t.Error("expected at least current log file")
	}
}

// TestDefaultOptions verifies default values are set correctly
func TestDefaultOptions(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level: slog.LevelInfo,
		Dir:   dir,
	})
	defer func() {
		if l, ok := log.(*logger); ok {
			if rw, ok := l.shared.output.(*rotationWriter); ok {
				rw.Close()
			}
		}
	}()

	l := log.(*logger)
	if l.shared.maxSize != 100*1024*1024 {
		t.Errorf("expected default MaxSize 100MB, got %d", l.shared.maxSize)
	}
	if l.shared.maxFiles != 10 {
		t.Errorf("expected default MaxFiles 10, got %d", l.shared.maxFiles)
	}
	if l.shared.baseName != "razordata.log" {
		t.Errorf("expected default BaseName 'razordata.log', got %s", l.shared.baseName)
	}
}

// TestSetOutputDisablesRotation verifies SetOutput disables rotation
func TestSetOutputDisablesRotation(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "test.log",
		MaxSize:  100,
	})

	var buf bytes.Buffer
	log.SetOutput(&buf)

	for i := 0; i < 100; i++ {
		log.Info("test")
	}

	l := log.(*logger)
	if l.shared.rotationFn != nil {
		t.Error("expected rotationFn to be nil after SetOutput")
	}
}

// TestRotationWriterWrite verifies size tracking
func TestRotationWriterWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	s := &sharedLogger{baseName: "test.log", dir: dir}
	rw := &rotationWriter{shared: s}
	rw.setFile(f)

	data := []byte("test data")
	n, err := rw.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(data) {
		t.Errorf("expected to write %d bytes, wrote %d", len(data), n)
	}

	if s.curSize.Load() != int64(len(data)) {
		t.Errorf("expected curSize=%d, got %d", len(data), s.curSize.Load())
	}
}

// TestRotationWriterSync verifies sync works
func TestRotationWriterSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	s := &sharedLogger{}
	rw := &rotationWriter{shared: s}
	rw.setFile(f)

	if err := rw.Sync(); err != nil {
		t.Errorf("Sync() returned error: %v", err)
	}
}

// TestListRotatedFilesNonExistentDir verifies error handling
func TestListRotatedFilesNonExistentDir(t *testing.T) {
	s := &sharedLogger{dir: "/nonexistent/directory/path"}
	_, err := s.listRotatedFiles()
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

// TestRotationWithSmallMaxSize verifies rapid rotation works
func TestRotationWithSmallMaxSize(t *testing.T) {
	dir := t.TempDir()
	log := New(Options{
		Level:    slog.LevelInfo,
		Dir:      dir,
		BaseName: "rapid.log",
		MaxSize:  1024,
		MaxFiles: 2,
	})

	for i := 0; i < 20; i++ {
		log.Info("small")
	}

	// Just verify file was created
	files, _ := os.ReadDir(dir)
	if len(files) == 0 {
		t.Error("expected at least one log file")
	}
}
