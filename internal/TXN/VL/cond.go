package VL

import (
	"context"
	"fmt"
	"time"
)

// WaitForActive blocks until the manager's active-transaction count
// drops to zero, the context is cancelled, or the timeout elapses.
// Returns nil on drain, ctx.Err() on cancellation, or a timeout
// error wrapping the residual active count.
//
// Phase 2 of SYS.md:215-282 (graceful shutdown) calls WaitForActive
// with a 30 s deadline. If it returns a non-nil error, Phase 2
// proceeds to force-abort the remaining transactions.
//
// Implementation note: the design spec calls for a sync.Cond with
// broadcast on Commit/Abort. Threading the cond into the hot path
// (every Commit, every Abort) was deferred to keep this change
// isolated; the poll compromise is acceptable for a shutdown-only
// path that runs at most once per process and would otherwise
// require locking around every transaction's commit/abort.
func (m *Manager) WaitForActive(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("waitforactive: timeout must be positive, got %v", timeout)
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if m.sm.NumActiveSlots() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("waitforactive: timed out after %v with %d active transactions", timeout, m.sm.NumActiveSlots())
		case <-ticker.C:
			// poll again
		}
	}
}
