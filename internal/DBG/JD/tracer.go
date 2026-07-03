//go:build debug

package JD

import "sync/atomic"

// Verbosity level constants.
const (
	LevelOff     = 0
	LevelSummary = 1
	LevelDetailed = 2
	LevelFull    = 3
)

// JoinTracer defines the interface that JOIN operators call at key points.
type JoinTracer interface {
	RowFlow(operator string, table string, rowID uint64, entering bool)
	Predicate(operator string, expr string, leftRowID, rightRowID uint64, passed bool)
	ColumnOffset(operator string, expected, actual int, colName string)
	Strategy(operator string, chosen string, reason string, estimatedCost float64)
	Correlation(stage int, tables []string, rowCount int64)
}

// noopTracer is a no-op implementation of JoinTracer used when debug tracing is off.
type noopTracer struct{}

func (noopTracer) RowFlow(operator string, table string, rowID uint64, entering bool) {}
func (noopTracer) Predicate(operator string, expr string, leftRowID, rightRowID uint64, passed bool) {
}
func (noopTracer) ColumnOffset(operator string, expected, actual int, colName string) {}
func (noopTracer) Strategy(operator string, chosen string, reason string, estimatedCost float64) {
}
func (noopTracer) Correlation(stage int, tables []string, rowCount int64) {}

var (
	globalTracer JoinTracer
	verbosity    atomic.Int32
)

// SetJoinTracer sets the global join tracer instance.
func SetJoinTracer(t JoinTracer) {
	globalTracer = t
}

// GetJoinTracer returns the global join tracer instance.
func GetJoinTracer() JoinTracer {
	return globalTracer
}

// SetVerbosity sets the verbosity level.
func SetVerbosity(level int) {
	verbosity.Store(int32(level))
}

// GetVerbosity returns the current verbosity level.
func GetVerbosity() int {
	return int(verbosity.Load())
}

// IsEnabled returns true if a tracer is set and verbosity is above zero.
func IsEnabled() bool {
	return globalTracer != nil && verbosity.Load() > 0
}
