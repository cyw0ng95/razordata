//go:build debug

package lg

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

var (
	levelOverrides sync.Map // map[string]*atomic.Int32
	levelMapOnce   sync.Once
)

func initLevelMap() {
	levelMapOnce.Do(func() {})
}

func loadOrStoreLevel(name string, defaultVal int32) *atomic.Int32 {
	val, _ := levelOverrides.LoadOrStore(name, &atomic.Int32{})
	lvl := val.(*atomic.Int32)
	lvl.CompareAndSwap(0, defaultVal)
	return lvl
}

// NewSubsystemLogger returns a logger gated by per-subsystem atomic level overrides.
func NewSubsystemLogger(name string, defaultLevel slog.Level) *slog.Logger {
	initLevelMap()
	lvl := loadOrStoreLevel(name, int32(defaultLevel))
	h := &subsystemHandler{name: name, level: lvl, base: slog.Default().Handler()}
	return slog.New(h)
}

// SetSubsystemLevel overrides the log level for a subsystem at runtime.
func SetSubsystemLevel(name string, level slog.Level) {
	val, _ := levelOverrides.LoadOrStore(name, &atomic.Int32{})
	val.(*atomic.Int32).Store(int32(level))
}

// GetSubsystemLevel returns the current effective level for a subsystem.
func GetSubsystemLevel(name string) slog.Level {
	val, ok := levelOverrides.Load(name)
	if !ok {
		return slog.LevelInfo
	}
	return slog.Level(val.(*atomic.Int32).Load())
}

// subsystemHandler wraps a base handler with per-subsystem level gating.
type subsystemHandler struct {
	name  string
	level *atomic.Int32
	base  slog.Handler
}

func (h *subsystemHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.Level(h.level.Load())
}

func (h *subsystemHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.base.Handle(ctx, r)
}

func (h *subsystemHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &subsystemHandler{name: h.name, level: h.level, base: h.base.WithAttrs(attrs)}
}

func (h *subsystemHandler) WithGroup(name string) slog.Handler {
	return &subsystemHandler{name: h.name, level: h.level, base: h.base.WithGroup(name)}
}
