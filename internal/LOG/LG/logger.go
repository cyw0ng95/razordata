package lg

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
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
	impl   *slog.Logger
	levelp *atomic.Int32 // pointer to shared atomic level
}

// level returns the atomic level value.
func (l *logger) level() int32 {
	return l.levelp.Load()
}

var _ Logger = (*logger)(nil)

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

	var handler slog.Handler
	if opts.Format == "json" {
		handler = slog.NewJSONHandler(opts.Output, nil)
	} else {
		handler = slog.NewTextHandler(opts.Output, nil)
	}

	return newFromHandler(slog.New(handler), opts.Level)
}

// newFromHandler creates a Logger from an existing *slog.Logger and level.
func newFromHandler(impl *slog.Logger, level slog.Level) *logger {
	l := &logger{impl: impl}
	// Note: each new logger gets its own level initially.
	// Callers who want shared level should create a pointer externally.
	levelInt := new(atomic.Int32)
	levelInt.Store(int32(level))
	l.levelp = levelInt
	return l
}

// SetLevel atomically updates the minimum log level.
func (l *logger) SetLevel(level slog.Level) {
	l.levelp.Store(int32(level))
}

func (l *logger) currentLevel() slog.Level {
	return slog.Level(l.level())
}

// logIfEnabled checks the atomic level before constructing the log record.
// Zero overhead when the level is disabled.
func (l *logger) logIfEnabled(level slog.Level, msg string, args ...any) {
	if level < l.currentLevel() {
		return
	}
	l.impl.Log(context.Background(), level, msg, args...)
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
// The new logger shares the same atomic level as the original — changes to either affect both.
// This is intentional: the level is a global property of the logging system.
func (l *logger) With(args ...any) Logger {
	return &logger{
		impl:   l.impl.With(args...),
		levelp: l.levelp, // share the same atomic level pointer
	}
}

// Sync flushes the underlying slog handler.
// Note: Handler.Sync() is available in Go 1.24+. On older versions, this is a no-op.
// The underlying slog handlers flush on close or error, so this is best-effort.
func (l *logger) Sync() error {
	if h, ok := l.impl.Handler().(interface{ Sync() error }); ok {
		return h.Sync()
	}
	return nil // no-op on older Go versions
}
