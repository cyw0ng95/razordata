//go:build debug

package dc

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// Control provides runtime toggling of debug knobs.
type Control struct {
	levelOverrides sync.Map
	traceEnabled   sync.Map
	slowThreshold  atomic.Int64
}

// NewControl creates a dynamic control instance.
func NewControl() *Control { return &Control{} }

// SetLogLevel overrides the log level for a subsystem.
func (c *Control) SetLogLevel(subsystem string, level slog.Level) {
	val, _ := c.levelOverrides.LoadOrStore(subsystem, &atomic.Int32{})
	val.(*atomic.Int32).Store(int32(level))
}

// GetLogLevel returns the current level for a subsystem.
func (c *Control) GetLogLevel(subsystem string) slog.Level {
	val, ok := c.levelOverrides.Load(subsystem)
	if !ok {
		return slog.LevelInfo
	}
	return slog.Level(val.(*atomic.Int32).Load())
}

// EnableTrace toggles a trace class on/off.
func (c *Control) EnableTrace(class string, enabled bool) {
	val, _ := c.traceEnabled.LoadOrStore(class, &atomic.Bool{})
	val.(*atomic.Bool).Store(enabled)
}

// IsTraceEnabled checks if a trace class is enabled.
func (c *Control) IsTraceEnabled(class string) bool {
	val, ok := c.traceEnabled.Load(class)
	if !ok {
		return false
	}
	return val.(*atomic.Bool).Load()
}

// SetSlowThreshold sets the slow query threshold in nanoseconds.
func (c *Control) SetSlowThreshold(d int64) { c.slowThreshold.Store(d) }

// GetSlowThreshold returns the current slow query threshold.
func (c *Control) GetSlowThreshold() int64 { return c.slowThreshold.Load() }
