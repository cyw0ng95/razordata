//go:build debug

package JD

import (
	"testing"
	"time"
)

func TestBuffer_Append(t *testing.T) {
	buf := NewBuffer(4)
	now := time.Now()

	for i := 0; i < 4; i++ {
		overwritten := buf.Append(JoinEvent{
			Type:  EventRowFlow,
			Time:  now,
			Op:    "NestedLoop",
			Table: "t1",
			RowID: uint64(i),
		})
		if overwritten {
			t.Fatalf("event %d should not overwrite", i)
		}
	}

	capacity, used, dropped := buf.Stats()
	if capacity != 4 {
		t.Fatalf("capacity: want 4, got %d", capacity)
	}
	if used != 4 {
		t.Fatalf("used: want 4, got %d", used)
	}
	if dropped != 0 {
		t.Fatalf("dropped: want 0, got %d", dropped)
	}
}

func TestBuffer_Wrap(t *testing.T) {
	buf := NewBuffer(4)
	now := time.Now()

	overwriteCount := 0
	for i := 0; i < 8; i++ {
		overwritten := buf.Append(JoinEvent{
			Type:  EventRowFlow,
			Time:  now,
			Op:    "NestedLoop",
			Table: "t1",
			RowID: uint64(i),
		})
		if overwritten {
			overwriteCount++
		}
	}

	if overwriteCount != 4 {
		t.Fatalf("overwrite count: want 4, got %d", overwriteCount)
	}

	capacity, used, dropped := buf.Stats()
	if capacity != 4 {
		t.Fatalf("capacity: want 4, got %d", capacity)
	}
	if used != 4 {
		t.Fatalf("used: want 4, got %d", used)
	}
	if dropped != 4 {
		t.Fatalf("dropped: want 4, got %d", dropped)
	}
}

func TestBuffer_Flush(t *testing.T) {
	buf := NewBuffer(4)
	now := time.Now()

	buf.Append(JoinEvent{
		Type:  EventRowFlow,
		Time:  now,
		Op:    "NestedLoop",
		Table: "t1",
		RowID: 10,
	})
	buf.Append(JoinEvent{
		Type:  EventPredicate,
		Time:  now,
		Op:    "HashJoin",
		Expr:  "a=b",
		RowID: 20,
	})

	events := buf.Flush()
	if len(events) != 2 {
		t.Fatalf("flush: want 2 events, got %d", len(events))
	}
	if events[0].RowID != 10 {
		t.Fatalf("event[0].RowID: want 10, got %d", events[0].RowID)
	}
	if events[1].Expr != "a=b" {
		t.Fatalf("event[1].Expr: want 'a=b', got %q", events[1].Expr)
	}

	// Verify buffer is empty after flush
	_, used, dropped := buf.Stats()
	if used != 0 {
		t.Fatalf("used after flush: want 0, got %d", used)
	}
	if dropped != 0 {
		t.Fatalf("dropped after flush: want 0, got %d", dropped)
	}

	events2 := buf.Flush()
	if events2 != nil {
		t.Fatalf("second flush: want nil, got %d events", len(events2))
	}
}

func TestBuffer_NonPowerOf2RoundsUp(t *testing.T) {
	buf := NewBuffer(3)
	capacity, _, _ := buf.Stats()
	if capacity != 4 {
		t.Fatalf("capacity for input 3: want 4 (round up), got %d", capacity)
	}
}

func TestBuffer_SingleCapacity(t *testing.T) {
	buf := NewBuffer(1)
	capacity, _, _ := buf.Stats()
	if capacity != 1 {
		t.Fatalf("capacity for input 1: want 1, got %d", capacity)
	}

	now := time.Now()
	for i := 0; i < 5; i++ {
		buf.Append(JoinEvent{
			Type:  EventRowFlow,
			Time:  now,
			Op:    "Test",
			RowID: uint64(i),
		})
	}

	_, used, dropped := buf.Stats()
	if used != 1 {
		t.Fatalf("used: want 1, got %d", used)
	}
	if dropped != 4 {
		t.Fatalf("dropped: want 4, got %d", dropped)
	}

	events := buf.Flush()
	if len(events) != 1 {
		t.Fatalf("flush: want 1 event, got %d", len(events))
	}
	if events[0].RowID != 4 {
		t.Fatalf("event.RowID: want 4 (last written), got %d", events[0].RowID)
	}
}
