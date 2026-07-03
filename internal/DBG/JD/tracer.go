//go:build debug

package JD

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

// BufferedTracer captures join events into a ring buffer.
type BufferedTracer struct {
	buf       *Buffer
	verbosity int
}

// NewBufferedTracer creates a tracer with the given capacity and verbosity.
func NewBufferedTracer(capacity, verbosity int) *BufferedTracer {
	return &BufferedTracer{buf: NewBuffer(capacity), verbosity: verbosity}
}

func (t *BufferedTracer) RowFlow(operator, table string, rowID uint64, entering bool) {
	if t.verbosity < LevelSummary {
		return
	}
	t.buf.Append(JoinEvent{
		Type:     EventRowFlow,
		Time:     time.Now(),
		Op:       operator,
		Table:    table,
		RowID:    rowID,
		Entering: entering,
	})
}

func (t *BufferedTracer) Predicate(operator, expr string, leftRowID, rightRowID uint64, passed bool) {
	if t.verbosity < LevelDetailed {
		return
	}
	t.buf.Append(JoinEvent{
		Type:       EventPredicate,
		Time:       time.Now(),
		Op:         operator,
		Expr:       expr,
		LeftRowID:  leftRowID,
		RightRowID: rightRowID,
		Passed:     passed,
	})
}

func (t *BufferedTracer) ColumnOffset(operator string, expected, actual int, colName string) {
	if t.verbosity < LevelDetailed {
		return
	}
	t.buf.Append(JoinEvent{
		Type:         EventColumnOffset,
		Time:         time.Now(),
		Op:           operator,
		ExpectedCols: expected,
		ActualCols:   actual,
		ColName:      colName,
	})
}

func (t *BufferedTracer) Strategy(operator, chosen, reason string, estimatedCost float64) {
	if t.verbosity < LevelSummary {
		return
	}
	t.buf.Append(JoinEvent{
		Type:          EventStrategy,
		Time:          time.Now(),
		Op:            operator,
		Chosen:        chosen,
		Reason:        reason,
		EstimatedCost: estimatedCost,
	})
}

func (t *BufferedTracer) Correlation(stage int, tables []string, rowCount int64) {
	if t.verbosity < LevelSummary {
		return
	}
	t.buf.Append(JoinEvent{
		Type:     EventCorrelation,
		Time:     time.Now(),
		Stage:    stage,
		Tables:   tables,
		RowCount: rowCount,
	})
}

// Flush returns all buffered events.
func (t *BufferedTracer) Flush() []JoinEvent { return t.buf.Flush() }

// Stats returns buffer statistics.
func (t *BufferedTracer) Stats() (cap int, used int64, dropped int64) {
	return t.buf.Stats()
}
