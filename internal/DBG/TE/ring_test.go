//go:build debug

package te

import "testing"

func TestRing_Append(t *testing.T) {
	r := NewRing(4)
	for i := 0; i < 4; i++ {
		if r.Append("c", map[string]any{"i": i}) {
			t.Fatalf("unexpected overwrite at step %d", i)
		}
	}
	s := r.Stats()
	if s.Capacity != 4 || s.Used != 4 || s.Dropped != 0 {
		t.Errorf("unexpected stats: %+v", s)
	}
}

func TestRing_Wrap(t *testing.T) {
	r := NewRing(4)
	for i := 0; i < 8; i++ {
		r.Append("c", map[string]any{"i": i})
	}
	s := r.Stats()
	if s.Dropped != 4 {
		t.Errorf("expected 4 dropped, got %d", s.Dropped)
	}
	if s.Used != 4 {
		t.Errorf("expected used 4, got %d", s.Used)
	}
}

func TestRing_PowerOf2(t *testing.T) {
	r := NewRing(5)
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
