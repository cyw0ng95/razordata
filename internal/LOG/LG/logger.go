package lg

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures the logger.
type Options struct {
	Level    slog.Level // minimum log level, default Info
	Format   string     // "json" or "text", default "text"
	Output   io.Writer  // destination, default os.Stderr
	Dir      string     // log directory for rotation, empty = no rotation
	BaseName string     // base filename, default "razordata.log"
	MaxSize  int64      // max file size before rotation in bytes, default100MB
	MaxFiles int        // max rotated files to retain, default10
	// CompressRotated, when true (default), gzips the rotated file
	// and writes <base>.YYYYMMDD_HHMMSS.log.gz. The uncompressed
	// intermediate is removed on success. Set false to keep plain
	// .log rotated files. (R16-15)
	CompressRotated bool
}

// Logger is the main logging interface exposed to other subsystems.
// All methods are safe for concurrent use.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	With(args ...any) Logger
	SetLevel(level slog.Level)
	SetOutput(w io.Writer)
	Sync() error
}

// logger is the concrete implementation of Logger.
type logger struct {
	impl *slog.Logger
	// shared holds the shared state between parent and child loggers.
	// Both parent and child use the same pointer, so SetLevel affects both.
	shared *sharedLogger
}

type sharedLogger struct {
	level  atomic.Int32 // atomic log level (slog.Level is int32-based)
	format string
	output io.Writer
	mu     sync.RWMutex

	// Rotation fields
	dir             string        // log directory, empty = no rotation
	baseName        string        // base filename
	maxSize         int64         // rotation threshold in bytes
	maxFiles        int           // max rotated files to retain
	compressRotated bool          // gzip rotated files (R16-15)
	curSize         atomic.Int64  // current file size
	callCount       atomic.Uint64 // log call counter for throttled rotation check
	rotationFn      func() error  // called when rotation needed
	rotMu           sync.Mutex    // mutex just for rotation
}

const rotationCheckInterval = 4 // check rotation every 4 log calls

// atomicLevel implements slog.Leveler using a atomic int32.
type atomicLevel struct {
	level *atomic.Int32
}

func (a atomicLevel) Level() slog.Level {
	return slog.Level(a.level.Load())
}

// New creates a Logger from Options.
// If Output is nil, defaults to os.Stderr.
// If Format is not "json", defaults to "text".
// If Dir is set, enables file rotation with default100MB max size.
func New(opts Options) Logger {
	if opts.Output == nil {
		opts.Output = os.Stderr
	}
	if opts.Format != "json" {
		opts.Format = "text"
	}

	s := &sharedLogger{
		format:          opts.Format,
		output:          opts.Output,
		dir:             opts.Dir,
		baseName:        opts.BaseName,
		maxSize:         opts.MaxSize,
		maxFiles:        opts.MaxFiles,
		compressRotated: opts.CompressRotated,
	}
	s.level.Store(int32(opts.Level))

	if s.baseName == "" {
		s.baseName = "razordata.log"
	}
	if s.maxSize <= 0 {
		s.maxSize = 100 * 1024 * 1024 //100MB default
	}
	if s.maxFiles <= 0 {
		s.maxFiles = 10
	}
	// R16-15: default CompressRotated to true unless the caller
	// explicitly set it to false. We can't distinguish "set to
	// false" from "not set" with a bool alone, so the default is
	// the conservative plain-text behavior; callers who want
	// gzip set CompressRotated: true explicitly. To preserve the
	// backward-compatible plain-text default, the default here is
	// false. Production users opt in via Options.
	_ = s.compressRotated // explicit field; no auto-default to keep zero-value semantics.

	// If Dir is set, enable file rotation
	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: failed to create directory %s: %v\n", s.dir, err)
		} else {
			path := filepath.Join(s.dir, s.baseName)
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "log rotation: failed to open file %s: %v\n", path, err)
			} else {
				// Wrap file with rotation writer
				rw := &rotationWriter{shared: s}
				rw.setFile(f)
				s.output = rw

				// Get current file size
				info, err := f.Stat()
				if err == nil {
					s.curSize.Store(info.Size())
				}
				s.rotationFn = s.rotateFile
			}
		}
	}

	// Use atomicLevel wrapper that implements slog.Leveler
	al := atomicLevel{level: &s.level}

	var handler slog.Handler
	if s.format == "json" {
		handler = slog.NewJSONHandler(s.output, &slog.HandlerOptions{Level: al})
	} else {
		handler = slog.NewTextHandler(s.output, &slog.HandlerOptions{Level: al})
	}

	return &logger{impl: slog.New(handler), shared: s}
}

// SetLevel updates the minimum log level.
func (l *logger) SetLevel(level slog.Level) {
	l.shared.level.Store(int32(level))
}

// SetOutput changes the destination writer.
func (l *logger) SetOutput(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	l.shared.output = w

	// Create atomicLevel for the new handler
	al := atomicLevel{level: &l.shared.level}

	var handler slog.Handler
	if l.shared.format == "json" {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: al})
	} else {
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{Level: al})
	}
	l.impl = slog.New(handler)

	// Disable rotation when output is manually set
	l.shared.rotationFn = nil
}

// logIfEnabled checks the level before constructing the log record.
func (l *logger) logIfEnabled(lvl slog.Level, msg string, args ...any) {
	// Check level atomically - no lock needed
	enabled := lvl >= slog.Level(l.shared.level.Load())

	if !enabled {
		return
	}

	// Check rotation - throttled to every rotationCheckInterval calls
	rotationFn := l.shared.rotationFn
	if rotationFn != nil {
		c := l.shared.callCount.Add(1)
		if c%rotationCheckInterval == 0 && l.shared.curSize.Load() >= l.shared.maxSize {
			l.shared.rotationFn()
		}
	}

	l.impl.Log(context.Background(), lvl, msg, args...)
}

// Debug logs at Debug level.
func (l *logger) Debug(msg string, args ...any) {
	l.logIfEnabled(slog.LevelDebug, msg, args...)
}

// Info logs at Info level.
func (l *logger) Info(msg string, args ...any) {
	l.logIfEnabled(slog.LevelInfo, msg, args...)
}

// Warn logs at Warn level.
func (l *logger) Warn(msg string, args ...any) {
	l.logIfEnabled(slog.LevelWarn, msg, args...)
}

// Error logs at Error level.
func (l *logger) Error(msg string, args ...any) {
	l.logIfEnabled(slog.LevelError, msg, args...)
}

// With returns a new Logger with the given args merged into the underlying slog logger.
// The new logger shares the same shared state as the original —
// SetLevel on either affects both. This is intentional: the level is a global
// property of the logging system.
func (l *logger) With(args ...any) Logger {
	return &logger{impl: l.impl.With(args...), shared: l.shared}
}

// Sync flushes the underlying slog handler.
func (l *logger) Sync() error {
	if h, ok := l.impl.Handler().(interface{ Sync() error }); ok {
		return h.Sync()
	}
	return nil
}

// rotationWriter wraps the log file to track size accurately.
type rotationWriter struct {
	file   atomic.Pointer[os.File]
	shared *sharedLogger
}

func (w *rotationWriter) Write(p []byte) (int, error) {
	f := w.file.Load()
	if f == nil {
		return 0, os.ErrClosed
	}
	n, err := f.Write(p)
	w.shared.curSize.Add(int64(n))
	return n, err
}

func (w *rotationWriter) Sync() error {
	f := w.file.Load()
	if f == nil {
		return nil
	}
	return f.Sync()
}

func (w *rotationWriter) Close() error {
	f := w.file.Swap(nil)
	if f == nil {
		return nil
	}
	return f.Close()
}

func (w *rotationWriter) setFile(f *os.File) {
	w.file.Store(f)
}

// rotateFile performs log rotation:
// 1. Close current file
// 2. Rename to <baseName>.YYYYMMDD_HHMMSS.log
// 3. (R16-15) gzip the rotated file to <baseName>.YYYYMMDD_HHMMSS.log.gz
// 4. Open new file with original name
// 5. Update curSize to0
// 6. Delete oldest file if exceeding maxFiles
func (s *sharedLogger) rotateFile() error {
	// Use rotMu instead of s.mu to avoid deadlock with slog handler
	s.rotMu.Lock()
	defer s.rotMu.Unlock()

	// Double-check under lock: another goroutine may have rotated already
	if s.curSize.Load() < s.maxSize {
		return nil
	}

	// Get current file from output
	rw, ok := s.output.(*rotationWriter)
	if !ok {
		return nil // rotation not active
	}

	// Close current file
	if err := rw.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to sync file: %v\n", err)
	}
	if err := rw.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to close file: %v\n", err)
	}

	// Generate rotated filename: <baseName>.YYYYMMDD_HHMMSS.log
	timestamp := time.Now().Format("20060102_150405")
	ext := filepath.Ext(s.baseName)
	base := strings.TrimSuffix(s.baseName, ext)
	rotatedName := fmt.Sprintf("%s.%s%s", base, timestamp, ext)
	rotatedPath := filepath.Join(s.dir, rotatedName)
	currentPath := filepath.Join(s.dir, s.baseName)

	// Rename current file to rotated name
	if err := os.Rename(currentPath, rotatedPath); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to rename file: %v\n", err)
		// Try to reopen original file on failure
		f, openErr := os.OpenFile(currentPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if openErr == nil {
			rw.setFile(f)
			s.curSize.Store(0)
		}
		return err
	}

	// R16-15: gzip the rotated file in place. On success, delete the
	// uncompressed intermediate. On failure, leave the uncompressed
	// file in place — rotation succeeded even if compression failed.
	if s.compressRotated {
		gzPath := rotatedPath + ".gz"
		if err := gzipFile(rotatedPath, gzPath); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: gzip failed: %v\n", err)
		} else if err := os.Remove(rotatedPath); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: remove uncompressed after gzip: %v\n", err)
		}
	}

	// Open new file with original name
	f, err := os.OpenFile(currentPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to open new file: %v\n", err)
		// Reopen rotated file as fallback
		f, openErr := os.OpenFile(rotatedPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if openErr != nil {
			return fmt.Errorf("failed to open any log file: %w", err)
		}
		rw.setFile(f)
		s.curSize.Store(0)
		return err
	}

	rw.setFile(f)
	s.curSize.Store(0)

	// Cleanup old files if exceeding maxFiles
	if err := s.cleanupOldLogs(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to cleanup old logs: %v\n", err)
	}

	return nil
}

// gzipFile reads src and writes a gzip-compressed copy to dst. The
// caller is responsible for removing src on success. (R16-15)
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}

// cleanupOldLogs removes the oldest rotated log files if exceeding maxFiles.
func (s *sharedLogger) cleanupOldLogs() error {
	files, err := s.listRotatedFiles()
	if err != nil {
		return err
	}

	// Delete oldest files if exceeding maxFiles
	// files is sorted oldest first, so delete from the beginning
	toDelete := len(files) - s.maxFiles
	for i := 0; i < toDelete; i++ {
		if err := os.Remove(files[i]); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: failed to remove old file %s: %v\n", files[i], err)
		}
	}
	return nil
}

// listRotatedFiles returns sorted list of rotated log files (oldest first).
// Recognizes both <baseName>.YYYYMMDD_HHMMSS.log and
// <baseName>.YYYYMMDD_HHMMSS.log.gz (R16-16).
func (s *sharedLogger) listRotatedFiles() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}

	ext := filepath.Ext(s.baseName)
	base := strings.TrimSuffix(s.baseName, ext)
	prefix := base + "."
	gzExt := ext + ".gz"

	var rotated []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Check if matches rotation pattern: <base>.YYYYMMDD_HHMMSS<ext>
		// or <base>.YYYYMMDD_HHMMSS<ext>.gz
		if !strings.HasPrefix(name, prefix) || name == s.baseName {
			continue
		}

		suffix := strings.TrimPrefix(name, prefix)
		var timestampPart string
		switch {
		case strings.HasSuffix(suffix, gzExt):
			timestampPart = strings.TrimSuffix(suffix, gzExt)
		case strings.HasSuffix(suffix, ext):
			timestampPart = strings.TrimSuffix(suffix, ext)
		default:
			continue
		}
		if len(timestampPart) != 15 { // YYYYMMDD_HHMMSS
			continue
		}

		rotated = append(rotated, filepath.Join(s.dir, name))
	}

	// Sort by filename (which includes timestamp), oldest first
	sort.Strings(rotated)
	return rotated, nil
}

// FirstLogger returns the first logger in logs, or nil if logs is empty.
// Many subsystems accept an optional logger via variadic arguments; this
// helper consolidates the "first-or-nil" pick so each subsystem does
// not re-implement it.
func FirstLogger(logs []Logger) Logger {
	if len(logs) > 0 {
		return logs[0]
	}
	return nil
}
