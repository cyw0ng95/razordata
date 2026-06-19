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
	Level           slog.Level // minimum log level, default Info
	Format          string     // "json" or "text", default "text"
	Output          io.Writer  // destination, default os.Stderr
	Dir             string     // log directory for rotation, empty = no rotation
	BaseName        string     // base filename, default "razordata.log"
	MaxSize         int64      // max file size before rotation in bytes, default100MB
	MaxFiles        int        // max rotated files to retain, default10
	CompressRotated bool       // gzip rotated files (R16-15)
}

// Logger is the main logging interface.
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

type logger struct {
	impl   *slog.Logger
	shared *sharedLogger
}

type sharedLogger struct {
	level  atomic.Int32 // atomic log level (slog.Level is int32-based)
	format string
	output io.Writer
	mu     sync.RWMutex

	// Rotation fields
	dir             string        // log directory, empty = no rotation
	baseName        string
	maxSize         int64         // rotation threshold in bytes
	maxFiles        int           // max rotated files to retain
	compressRotated bool          // gzip rotated files (R16-15)
	curSize         atomic.Int64
	callCount       atomic.Uint64 // log call counter for throttled rotation check
	rotationFn      func() error
	rotMu           sync.Mutex    // mutex just for rotation
}

const rotationCheckInterval = 4 // check rotation every 4 log calls

type atomicLevel struct {
	level *atomic.Int32
}

func (a atomicLevel) Level() slog.Level {
	return slog.Level(a.level.Load())
}

// New creates a Logger from Options.
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
	_ = s.compressRotated

	if s.dir != "" {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: failed to create directory %s: %v\n", s.dir, err)
		} else {
			path := filepath.Join(s.dir, s.baseName)
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "log rotation: failed to open file %s: %v\n", path, err)
			} else {
				rw := &rotationWriter{shared: s}
				rw.setFile(f)
				s.output = rw

				info, err := f.Stat()
				if err == nil {
					s.curSize.Store(info.Size())
				}
				s.rotationFn = s.rotateFile
			}
		}
	}

	al := atomicLevel{level: &s.level}

	var handler slog.Handler
	if s.format == "json" {
		handler = slog.NewJSONHandler(s.output, &slog.HandlerOptions{Level: al})
	} else {
		handler = slog.NewTextHandler(s.output, &slog.HandlerOptions{Level: al})
	}

	return &logger{impl: slog.New(handler), shared: s}
}

func (l *logger) SetLevel(level slog.Level) {
	l.shared.level.Store(int32(level))
}

func (l *logger) SetOutput(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	l.shared.output = w

	al := atomicLevel{level: &l.shared.level}

	var handler slog.Handler
	if l.shared.format == "json" {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: al})
	} else {
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{Level: al})
	}
	l.impl = slog.New(handler)

	l.shared.rotationFn = nil
}

func (l *logger) logIfEnabled(lvl slog.Level, msg string, args ...any) {
	enabled := lvl >= slog.Level(l.shared.level.Load())

	if !enabled {
		return
	}

	rotationFn := l.shared.rotationFn
	if rotationFn != nil {
		c := l.shared.callCount.Add(1)
		if c%rotationCheckInterval == 0 && l.shared.curSize.Load() >= l.shared.maxSize {
			l.shared.rotationFn()
		}
	}

	l.impl.Log(context.Background(), lvl, msg, args...)
}

func (l *logger) Debug(msg string, args ...any) {
	l.logIfEnabled(slog.LevelDebug, msg, args...)
}

func (l *logger) Info(msg string, args ...any) {
	l.logIfEnabled(slog.LevelInfo, msg, args...)
}

func (l *logger) Warn(msg string, args ...any) {
	l.logIfEnabled(slog.LevelWarn, msg, args...)
}

func (l *logger) Error(msg string, args ...any) {
	l.logIfEnabled(slog.LevelError, msg, args...)
}

// With returns a new Logger with the given args merged.
func (l *logger) With(args ...any) Logger {
	return &logger{impl: l.impl.With(args...), shared: l.shared}
}

func (l *logger) Sync() error {
	if h, ok := l.impl.Handler().(interface{ Sync() error }); ok {
		return h.Sync()
	}
	return nil
}

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

// rotateFile performs log rotation.
func (s *sharedLogger) rotateFile() error {
	s.rotMu.Lock()
	defer s.rotMu.Unlock()

	if s.curSize.Load() < s.maxSize {
		return nil
	}

	rw, ok := s.output.(*rotationWriter)
	if !ok {
		return nil // rotation not active
	}

	if err := rw.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to sync file: %v\n", err)
	}
	if err := rw.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to close file: %v\n", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	ext := filepath.Ext(s.baseName)
	base := strings.TrimSuffix(s.baseName, ext)
	rotatedName := fmt.Sprintf("%s.%s%s", base, timestamp, ext)
	rotatedPath := filepath.Join(s.dir, rotatedName)
	currentPath := filepath.Join(s.dir, s.baseName)

	if err := os.Rename(currentPath, rotatedPath); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to rename file: %v\n", err)
		f, openErr := os.OpenFile(currentPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if openErr == nil {
			rw.setFile(f)
			s.curSize.Store(0)
		}
		return err
	}

	if s.compressRotated {
		gzPath := rotatedPath + ".gz"
		if err := gzipFile(rotatedPath, gzPath); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: gzip failed: %v\n", err)
		} else if err := os.Remove(rotatedPath); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: remove uncompressed after gzip: %v\n", err)
		}
	}

	f, err := os.OpenFile(currentPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to open new file: %v\n", err)
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

	if err := s.cleanupOldLogs(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: failed to cleanup old logs: %v\n", err)
	}

	return nil
}

// gzipFile reads src and writes a gzip-compressed copy to dst.
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

func (s *sharedLogger) cleanupOldLogs() error {
	files, err := s.listRotatedFiles()
	if err != nil {
		return err
	}

	toDelete := len(files) - s.maxFiles
	for i := 0; i < toDelete; i++ {
		if err := os.Remove(files[i]); err != nil {
			fmt.Fprintf(os.Stderr, "log rotation: failed to remove old file %s: %v\n", files[i], err)
		}
	}
	return nil
}

// listRotatedFiles returns sorted list of rotated log files.
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

	sort.Strings(rotated)
	return rotated, nil
}

// FirstLogger returns the first logger in logs, or nil.
func FirstLogger(logs []Logger) Logger {
	if len(logs) > 0 {
		return logs[0]
	}
	return nil
}
