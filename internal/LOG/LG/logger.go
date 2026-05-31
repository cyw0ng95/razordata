package lg

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
)

// Options configures the logger.
type Options struct {
	Level  slog.Level // minimum log level, default Info
	Format string     // "json" or "text", default "text"
	Output io.Writer  // destination, default os.Stderr
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
	level  slog.Level
	format string
	output io.Writer
	mu     sync.RWMutex
}

// New creates a Logger from Options.
// If Output is nil, defaults to os.Stderr.
// If Format is not "json", defaults to "text".
func New(opts Options) Logger {
	if opts.Output == nil {
		opts.Output = os.Stderr
	}
	if opts.Format != "json" {
		opts.Format = "text"
	}

	s := &sharedLogger{level: opts.Level, format: opts.Format, output: opts.Output}
	var handler slog.Handler
	if opts.Format == "json" {
		handler = slog.NewJSONHandler(opts.Output, &slog.HandlerOptions{Level: &s.level})
	} else {
		handler = slog.NewTextHandler(opts.Output, &slog.HandlerOptions{Level: &s.level})
	}

	return &logger{impl: slog.New(handler), shared: s}
}

// SetLevel updates the minimum log level.
func (l *logger) SetLevel(level slog.Level) {
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	l.shared.level = level
}

// SetOutput changes the destination writer.
func (l *logger) SetOutput(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	l.shared.output = w
	var handler slog.Handler
	if l.shared.format == "json" {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: &l.shared.level})
	} else {
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{Level: &l.shared.level})
	}
	l.impl = slog.New(handler)
}

// logIfEnabled checks the level before constructing the log record.
func (l *logger) logIfEnabled(lvl slog.Level, msg string, args ...any) {
	l.shared.mu.RLock()
	defer l.shared.mu.RUnlock()
	if lvl < l.shared.level {
		return
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
