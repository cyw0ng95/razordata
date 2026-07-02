package ls

import (
	"context"
	"testing"
)

// TestCompactionStyle_String verifies the human-readable names.
// REQ000320.
func TestCompactionStyle_String(t *testing.T) {
	cases := []struct {
		s    CompactionStyle
		want string
	}{
		{CompactionStyleLeveled, "leveled"},
		{CompactionStyleTiered, "tiered"},
		{CompactionStyleHybrid, "hybrid"},
		{CompactionStyle(99), "leveled"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("%d: got %q, want %q", c.s, got, c.want)
		}
	}
}

// TestCompactionStyle_ShouldCompact covers all three styles
// at L0 and L1+ boundaries. REQ000320.
func TestCompactionStyle_ShouldCompact(t *testing.T) {
	// Leveled: size-based. Below budget => no compact.
	if CompactionStyleLeveled.shouldCompact(0, 100, 1024) {
		t.Errorf("leveled L0 below budget should not compact")
	}
	if !CompactionStyleLeveled.shouldCompact(0, 100, 100*1024*1024) {
		t.Errorf("leveled L0 above budget should compact")
	}
	// Tiered: count-based. Below threshold => no compact.
	if CompactionStyleTiered.shouldCompact(0, 3, 0) {
		t.Errorf("tiered L0 below run threshold should not compact")
	}
	if !CompactionStyleTiered.shouldCompact(0, 5, 0) {
		t.Errorf("tiered L0 above run threshold should compact")
	}
	// Hybrid: L0 = tiered, L1+ = leveled.
	if CompactionStyleHybrid.shouldCompact(0, 3, 0) {
		t.Errorf("hybrid L0 below run threshold should not compact")
	}
	if !CompactionStyleHybrid.shouldCompact(0, 5, 0) {
		t.Errorf("hybrid L0 above run threshold should compact")
	}
	if CompactionStyleHybrid.shouldCompact(1, 100, 1024) {
		t.Errorf("hybrid L1 below budget should not compact")
	}
	if !CompactionStyleHybrid.shouldCompact(1, 100, 100*1024*1024) {
		t.Errorf("hybrid L1 above budget should compact")
	}
}

// TestCompactionStyle_IsTiered verifies the level-based dispatch
// for hybrid. REQ000320.
func TestCompactionStyle_IsTiered(t *testing.T) {
	if CompactionStyleLeveled.IsTiered(0) {
		t.Errorf("leveled is not tiered at any level")
	}
	if !CompactionStyleTiered.IsTiered(0) {
		t.Errorf("tiered is tiered at all levels")
	}
	if !CompactionStyleHybrid.IsTiered(0) {
		t.Errorf("hybrid is tiered at L0")
	}
	if CompactionStyleHybrid.IsTiered(1) {
		t.Errorf("hybrid is not tiered at L1+")
	}
}

// TestCompactionManager_CompactionStyle verifies Set/GET.
// REQ000320.
func TestCompactionManager_CompactionStyle(t *testing.T) {
	dir := t.TempDir()
	mfst, err := newManifest(dir)
	if err != nil {
		t.Fatalf("newManifest: %v", err)
	}
	cur := mfst.current.Load()
	cur.levels = make([][]SSTFileMeta, 6)
	mfst.current.Store(cur)

	cm := newCompactionManager(DefaultFS(), dir, mfst)
	defer cm.Stop(context.Background())

	if cm.CompactionStyle() != CompactionStyleLeveled {
		t.Errorf("default style should be leveled, got %v", cm.CompactionStyle())
	}
	cm.SetCompactionStyle(CompactionStyleTiered)
	if cm.CompactionStyle() != CompactionStyleTiered {
		t.Errorf("expected tiered, got %v", cm.CompactionStyle())
	}
	cm.SetCompactionStyle(CompactionStyleHybrid)
	if cm.CompactionStyle() != CompactionStyleHybrid {
		t.Errorf("expected hybrid, got %v", cm.CompactionStyle())
	}
}
