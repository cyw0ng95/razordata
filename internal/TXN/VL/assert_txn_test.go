//go:build debug

package VL

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/cyw0ng95/razordata/internal/TXN/MV"
)

func TestTXN_Assert_CommitStateMachine(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		sm := newSlotManager()
		mv := MV.NewMV()
		mgr := NewManagerShared(sm, mv)

		ctx := context.Background()
		txI, err := mgr.Begin(ctx)
		if err != nil {
			return
		}
		tx := txI.(*tx)
		tx.slot.status.Store(int32(SlotCommitted))
		_ = tx.Commit(ctx)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestTXN_Assert_CommitStateMachine")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

func TestTXN_Assert_CommitStateMachine_HappyPath(t *testing.T) {
	sm := newSlotManager()
	mv := MV.NewMV()
	mgr := NewManagerShared(sm, mv)

	ctx := context.Background()
	txI, err := mgr.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if err := txI.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestTXN_Assert_CommitStateMachine_AbortFlow(t *testing.T) {
	sm := newSlotManager()
	mv := MV.NewMV()
	mgr := NewManagerShared(sm, mv)

	ctx := context.Background()
	txI, err := mgr.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if err := txI.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}
}