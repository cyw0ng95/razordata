package ls

import (
	"bytes"
	"testing"
)

// TestPivotKeys_Sorted verifies pivotKeys returns strictly
// increasing keys spanning the full input range. REQ000319.
func TestPivotKeys_Sorted(t *testing.T) {
	inputs := []SSTFileMeta{
		{MinKey: []byte("a"), MaxKey: []byte("f")},
		{MinKey: []byte("c"), MaxKey: []byte("k")},
		{MinKey: []byte("h"), MaxKey: []byte("z")},
	}
	pivots := pivotKeys(inputs, 3)
	if len(pivots) < 2 {
		t.Fatalf("expected >=2 pivots, got %d", len(pivots))
	}
	for i := 1; i < len(pivots); i++ {
		if bytes.Compare(pivots[i-1], pivots[i]) >= 0 {
			t.Errorf("pivots not strictly increasing: %q >= %q",
				pivots[i-1], pivots[i])
		}
	}
	// First pivot <= all min keys, last pivot >= all max keys.
	if bytes.Compare(pivots[0], []byte("a")) > 0 {
		t.Errorf("first pivot %q > min key 'a'", pivots[0])
	}
	last := pivots[len(pivots)-1]
	if bytes.Compare(last, []byte("z")) < 0 {
		t.Errorf("last pivot %q < max key 'z'", last)
	}
}

// TestPivotKeys_SingleInput covers the degenerate n=1 case.
func TestPivotKeys_SingleInput(t *testing.T) {
	inputs := []SSTFileMeta{
		{MinKey: []byte("m"), MaxKey: []byte("n")},
	}
	pivots := pivotKeys(inputs, 4)
	if len(pivots) < 2 {
		t.Fatalf("expected >=2 pivots, got %d", len(pivots))
	}
}

// TestNewSubCompactor_Defaults verifies concurrency is clamped to
// >= 1.
func TestNewSubCompactor_Defaults(t *testing.T) {
	sc := NewSubCompactor("/tmp", &manifest{}, 0)
	if sc.concurrency != 1 {
		t.Errorf("expected concurrency=1, got %d", sc.concurrency)
	}
	sc2 := NewSubCompactor("/tmp", &manifest{}, 8)
	if sc2.concurrency != 8 {
		t.Errorf("expected concurrency=8, got %d", sc2.concurrency)
	}
}
