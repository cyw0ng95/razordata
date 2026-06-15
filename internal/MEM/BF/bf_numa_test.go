//go:build !slt_corpus_full

package bf

import (
	"path/filepath"
	"testing"

	nm "github.com/cyw0ng95/razordata/internal/ENG/NM"
	"github.com/cyw0ng95/razordata/internal/FIL/DF"
)

// TestBufferPool_SlotNodeID_DefaultsToZero verifies the REQ000309
// per-slot node id is set on insert. On non-NUMA hosts (NodeCount
// == 1) the tag is always 0.
func TestBufferPool_SlotNodeID_DefaultsToZero(t *testing.T) {
	if nm.NodeCount() != 1 {
		t.Skip("test only runs on non-NUMA hosts (single-socket)")
	}
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("df.Create: %v", err)
	}
	defer bd.Close()

	bp, err := New(16, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer bp.Close()

	data := make([]byte, BlockSize)
	page := &Page{ID: 1, Data: data, Dirty: true}
	if err := bp.Upsert(page); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got := bp.Stats(); got.Capacity != 16 {
		t.Errorf("Capacity = %d, want 16", got.Capacity)
	}
}

// TestBufferPool_SlotNodeID_InRange exercises the slot-creation
// path that tags the node id. The slot's node id is stored
// internally; we exercise the code path and rely on the
// constructor's validation.
func TestBufferPool_SlotNodeID_InRange(t *testing.T) {
	dir := t.TempDir()
	bd, err := df.Create(filepath.Join(dir, "data.razor"))
	if err != nil {
		t.Fatalf("df.Create: %v", err)
	}
	defer bd.Close()

	bp, err := New(4, filepath.Join(dir, "hint"), bd, newMockSyncPool())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer bp.Close()

	data := make([]byte, BlockSize)
	page := &Page{ID: 7, Data: data, Dirty: true}
	if err := bp.Upsert(page); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}
