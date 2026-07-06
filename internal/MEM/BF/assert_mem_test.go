//go:build debug

package bf

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	df "github.com/cyw0ng95/razordata/internal/FIL/DF"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
)

func TestMEM_Assert_PinUnderflow(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		dir := t.TempDir()
		dfPath := filepath.Join(dir, "bp")
		bd, err := df.Create(dfPath)
		if err != nil {
			return
		}
		bd.Close()
		bd, err = df.Open(dfPath)
		if err != nil {
			return
		}
		sp := sp.New()
		bp, err := New(4, "", bd, sp)
		if err != nil {
			return
		}
		pg := &Page{ID: 1, Data: make([]byte, BlockSize)}
		bp.Upsert(pg)
		bp.Pin(pg)    // pinCount: 0 -> 1
		bp.Unpin(pg)  // pinCount: 1 -> 0
		bp.Unpin(pg)  // pinCount: 0 -> -1, WARN_ON
		bp.Pin(pg)    // BUG_ON: pinCount -1 < 0
		bp.Close()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMEM_Assert_PinUnderflow")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestMEM_Assert_WrongSize(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		sp := sp.New()
		sp.Get(-1)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMEM_Assert_WrongSize")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestMEM_Assert_EvictPinned_HappyPath(t *testing.T) {
	dir := t.TempDir()
	dfPath := filepath.Join(dir, "bp")
	bd, err := df.Create(dfPath)
	if err != nil {
		t.Fatal(err)
	}
	bd.Close()
	bd, err = df.Open(dfPath)
	if err != nil {
		t.Fatal(err)
	}
	sp := sp.New()
	bp, err := New(2, "", bd, sp)
	if err != nil {
		t.Fatal(err)
	}
	defer bp.Close()

	p1 := &Page{ID: 1, Data: make([]byte, BlockSize)}
	p2 := &Page{ID: 2, Data: make([]byte, BlockSize)}
	if err := bp.Upsert(p1); err != nil {
		t.Fatal(err)
	}
	if err := bp.Upsert(p2); err != nil {
		t.Fatal(err)
	}
	bp.Pin(p1)
	bp.Pin(p2)
	p3 := &Page{ID: 3, Data: make([]byte, BlockSize)}
	err = bp.Upsert(p3)
	if err == nil {
		t.Log("upsert at capacity succeeded (expected behavior)")
	}
	stats := bp.Stats()
	if stats.Evicts > 0 {
		t.Logf("evictions occurred: %d", stats.Evicts)
	}
}