//go:build debug

package cd

import (
	"sync/atomic"
	"time"
)

// Verbosity level constants.
const (
	LevelOff     = 0
	LevelSummary = 1
	LevelDetailed = 2
	LevelFull    = 3
)

// CTETracer defines the interface that CTE execution calls at key points.
type CTETracer interface {
	Seed(name string, numRows int)
	Iteration(name string, iter int, rowsIn, rowsOut int, cols []string)
	MaxIterationsReached(name string)
}

// noopTracer is a no-op implementation used when CTE tracing is off.
type noopTracer struct{}

func (noopTracer) Seed(name string, numRows int) {}
func (noopTracer) Iteration(name string, iter int, rowsIn, rowsOut int, cols []string) {}
func (noopTracer) MaxIterationsReached(name string) {}

var (
	globalTracer CTETracer
	verbosity    atomic.Int32
)

// SetCTETracer sets the global CTE tracer instance.
func SetCTETracer(t CTETracer) { globalTracer = t }

// GetCTETracer returns the global CTE tracer instance.
func GetCTETracer() CTETracer { return globalTracer }

// SetVerbosity sets the verbosity level.
func SetVerbosity(level int) { verbosity.Store(int32(level)) }

// GetVerbosity returns the current verbosity level.
func GetVerbosity() int { return int(verbosity.Load()) }

// IsEnabled returns true if a tracer is set and verbosity is above zero.
func IsEnabled() bool {
	return globalTracer != nil && verbosity.Load() > 0
}

// BufferedTracer captures CTE events into a ring buffer.
type BufferedTracer struct {
	buf       *Buffer
	verbosity int
}

// NewBufferedTracer creates a tracer with the given capacity and verbosity.
func NewBufferedTracer(capacity, verbosity int) *BufferedTracer {
	return &BufferedTracer{buf: NewBuffer(capacity), verbosity: verbosity}
}

func (t *BufferedTracer) Seed(name string, numRows int) {
	if t.verbosity < LevelSummary {
		return
	}
	t.buf.Append(CTEEvent{
		Type:    EventSeed,
		Time:    time.Now(),
		Name:    name,
		NumRows: numRows,
	})
}

func (t *BufferedTracer) Iteration(name string, iter int, rowsIn, rowsOut int, cols []string) {
	if t.verbosity < LevelSummary {
		return
	}
	t.buf.Append(CTEEvent{
		Type:    EventIteration,
		Time:    time.Now(),
		Name:    name,
		Iter:    iter,
		RowsIn:  rowsIn,
		RowsOut: rowsOut,
		Cols:    cols,
	})
}

func (t *BufferedTracer) MaxIterationsReached(name string) {
	if t.verbosity < LevelSummary {
		return
	}
	t.buf.Append(CTEEvent{
		Type: EventMaxIterations,
		Time: time.Now(),
		Name: name,
	})
}

// Flush returns all buffered events.
func (t *BufferedTracer) Flush() []CTEEvent { return t.buf.Flush() }

// Stats returns buffer statistics.
func (t *BufferedTracer) Stats() (cap int, used int64, dropped int64) {
	return t.buf.Stats()
}