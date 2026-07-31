//go:build debug

package te

import "testing"

func TestRing_Append(t *testing.T) {
	r := NewRing[Event](4)
	for i := 0; i < 4; i++ {
		if r.Append(Event{Class: "c", Fields: map[string]any{"i": i}, Seq: uint64(i)}) {
			t.Fatalf("unexpected overwrite at step %d", i)
		}
	}
	cap, used, dropped := r.Stats()
	if cap != 4 || used != 4 || dropped != 0 {
		t.Errorf("unexpected stats: cap=%d used=%d dropped=%d", cap, used, dropped)
	}
}

func TestRing_Wrap(t *testing.T) {
	r := NewRing[Event](4)
	for i := 0; i < 8; i++ {
		r.Append(Event{Class: "c", Fields: map[string]any{"i": i}, Seq: uint64(i)})
	}
	_, _, dropped := r.Stats()
	if dropped != 4 {
		t.Errorf("expected 4 dropped, got %d", dropped)
	}
	_, used, _ := r.Stats()
	if used != 4 {
		t.Errorf("expected used 4, got %d", used)
	}
}

func TestRing_PowerOf2(t *testing.T) {
	r := NewRing[Event](5)
	if r.capacity != 8 {
		t.Errorf("expected capacity 8, got %d", r.capacity)
	}
}

func TestNewTraceSink(t *testing.T) {
	sink := NewTraceSink()
	sink.Emit(nil, "test", map[string]any{"k": "v"})
	if s := sink.Stats(); s.Used != 1 {
		t.Errorf("expected used 1, got %d", s.Used)
	}
}