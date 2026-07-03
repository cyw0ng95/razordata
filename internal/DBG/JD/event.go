//go:build debug

package JD

import (
	"fmt"
	"time"
)

type EventType int

const (
	EventRowFlow      EventType = 0
	EventPredicate    EventType = 1
	EventColumnOffset EventType = 2
	EventStrategy     EventType = 3
	EventCorrelation  EventType = 4
)

func (t EventType) String() string {
	switch t {
	case EventRowFlow:
		return "RowFlow"
	case EventPredicate:
		return "Predicate"
	case EventColumnOffset:
		return "ColumnOffset"
	case EventStrategy:
		return "Strategy"
	case EventCorrelation:
		return "Correlation"
	default:
		return fmt.Sprintf("Unknown(%d)", int(t))
	}
}

type JoinEvent struct {
	Type  EventType
	Time  time.Time
	Op    string

	// RowFlow fields
	Table    string
	RowID    uint64
	Entering bool

	// Predicate fields
	Expr       string
	LeftRowID  uint64
	RightRowID uint64
	Passed     bool

	// ColumnOffset fields
	ExpectedCols int
	ActualCols   int
	ColName      string

	// Strategy fields
	Chosen        string
	Reason        string
	EstimatedCost float64

	// Correlation fields
	Stage    int
	Tables   []string
	RowCount int64
}

func (e JoinEvent) String() string {
	switch e.Type {
	case EventRowFlow:
		verb := "exit"
		if e.Entering {
			verb = "enter"
		}
		return fmt.Sprintf("[%s] %s %s row=%d %s", e.Time.Format("15:04:05.000"), e.Op, verb, e.RowID, e.Table)
	case EventPredicate:
		verb := "FAIL"
		if e.Passed {
			verb = "PASS"
		}
		return fmt.Sprintf("[%s] %s %s L=%d R=%d %q", e.Time.Format("15:04:05.000"), e.Op, verb, e.LeftRowID, e.RightRowID, e.Expr)
	case EventColumnOffset:
		return fmt.Sprintf("[%s] %s col=%q expected=%d actual=%d", e.Time.Format("15:04:05.000"), e.Op, e.ColName, e.ExpectedCols, e.ActualCols)
	case EventStrategy:
		return fmt.Sprintf("[%s] %s chosen=%q cost=%.2f reason=%q", e.Time.Format("15:04:05.000"), e.Op, e.Chosen, e.EstimatedCost, e.Reason)
	case EventCorrelation:
		return fmt.Sprintf("[%s] %s stage=%d tables=%v rows=%d", e.Time.Format("15:04:05.000"), e.Op, e.Stage, e.Tables, e.RowCount)
	default:
		return fmt.Sprintf("[%s] Unknown(%d) %s", e.Time.Format("15:04:05.000"), int(e.Type), e.Op)
	}
}
