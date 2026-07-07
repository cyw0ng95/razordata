//go:build debug

package cd

import (
	"fmt"
	"time"
)

// EventType identifies a CTE trace event.
type EventType int

const (
	EventSeed           EventType = 0
	EventIteration      EventType = 1
	EventMaxIterations  EventType = 2
)

func (t EventType) String() string {
	switch t {
	case EventSeed:
		return "Seed"
	case EventIteration:
		return "Iteration"
	case EventMaxIterations:
		return "MaxIterations"
	default:
		return fmt.Sprintf("Unknown(%d)", int(t))
	}
}

// CTEEvent is a single trace event for CTE execution.
type CTEEvent struct {
	Type EventType
	Time time.Time
	Name string // CTE name

	// Seed fields
	NumRows int

	// Iteration fields
	Iter    int
	RowsIn  int
	RowsOut int
	Cols    []string
}

func (e CTEEvent) String() string {
	switch e.Type {
	case EventSeed:
		return fmt.Sprintf("[%s] %s seed rows=%d", e.Time.Format("15:04:05.000"), e.Name, e.NumRows)
	case EventIteration:
		return fmt.Sprintf("[%s] %s iter=%d rows_in=%d rows_out=%d",
			e.Time.Format("15:04:05.000"), e.Name, e.Iter, e.RowsIn, e.RowsOut)
	case EventMaxIterations:
		return fmt.Sprintf("[%s] %s max_iterations_reached", e.Time.Format("15:04:05.000"), e.Name)
	default:
		return fmt.Sprintf("[%s] %s Unknown(%d)", e.Time.Format("15:04:05.000"), e.Name, int(e.Type))
	}
}