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
	Sync() error
}

// logger is the concrete implementation of Logger.
type logger struct {
	impl *slog.Logger
	// level is the minimum log level, protected by mu for thread-safe access.
	// Slog handler uses &level so it respects the same value.
	level  slog.Level
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

	level := opts.Level // local var, lives on stack
	var handler slog.Handler
	if opts.Format == "json" {
		handler = slog.NewJSONHandler(opts.Output, &slog.HandlerOptions{Level: &level})
	} else {
		handler = slog.NewTextHandler(opts.Output, &slog.HandlerOptions{Level: &level})
	}

	return &logger{impl: slog.New(handler), level: level}
}

// SetLevel updates the minimum log level.
func (l *logger) SetLevel(level slog.Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// logIfEnabled checks the level before constructing the log record.
func (l *logger) logIfEnabled(lvl slog.Level, msg string, args ...any) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if lvl < l.level {
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
func (l *logger) With(args ...any) Logger {
	l.mu.RLock()
	currentLevel := l.level
	l.mu.RUnlock()
	return &logger{impl: l.impl.With(args...), level: currentLevel}
}

// Sync flushes the underlying slog handler.
func (l *logger) Sync() error {
	if h, ok := l.impl.Handler().(interface{ Sync() error }); ok {
		return h.Sync()
	}
	return nil
}
