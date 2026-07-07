//go:build debug

package cd

import (
	"testing"
)

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		tt   EventType
		want string
	}{
		{EventSeed, "Seed"},
		{EventIteration, "Iteration"},
		{EventMaxIterations, "MaxIterations"},
		{EventType(99), "Unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.tt.String(); got != tt.want {
			t.Errorf("EventType(%d).String() = %q, want %q", tt.tt, got, tt.want)
		}
	}
}

func TestBufferAppendFlush(t *testing.T) {
	buf := NewBuffer(4)
	e1 := CTEEvent{Type: EventSeed, Name: "t1", NumRows: 5}
	e2 := CTEEvent{Type: EventIteration, Name: "t1", Iter: 1, RowsIn: 5, RowsOut: 3}
	e3 := CTEEvent{Type: EventMaxIterations, Name: "t1"}

	buf.Append(e1)
	buf.Append(e2)
	buf.Append(e3)

	events := buf.Flush()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Type != EventSeed {
		t.Errorf("event 0 type = %v, want %v", events[0].Type, EventSeed)
	}
	if events[1].Type != EventIteration {
		t.Errorf("event 1 type = %v, want %v", events[1].Type, EventIteration)
	}
	if events[2].Type != EventMaxIterations {
		t.Errorf("event 2 type = %v, want %v", events[2].Type, EventMaxIterations)
	}
}

func TestBufferOverwrite(t *testing.T) {
	buf := NewBuffer(2)
	for i := 0; i < 5; i++ {
		buf.Append(CTEEvent{Type: EventSeed, Name: "t1", NumRows: i})
	}
	events := buf.Flush()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	_ = events
}

func TestBufferFlushAfterFlush(t *testing.T) {
	buf := NewBuffer(4)
	buf.Append(CTEEvent{Type: EventSeed, Name: "t1", NumRows: 5})
	events1 := buf.Flush()
	events2 := buf.Flush()
	if len(events1) != 1 {
		t.Fatalf("first flush: expected 1 event, got %d", len(events1))
	}
	if len(events2) != 0 {
		t.Fatalf("second flush: expected 0 events, got %d", len(events2))
	}
}

func TestBufferStats(t *testing.T) {
	buf := NewBuffer(4)
	for i := 0; i < 10; i++ {
		buf.Append(CTEEvent{Type: EventSeed, Name: "t1", NumRows: i})
	}
	cap, used, dropped := buf.Stats()
	if cap != 4 {
		t.Errorf("capacity = %d, want 4", cap)
	}
	if used != 4 {
		t.Errorf("used = %d, want 4", used)
	}
	if dropped != 6 {
		t.Errorf("dropped = %d, want 6", dropped)
	}
}