package VL

import (
	"context"
	"fmt"
	"time"
)

// WaitForActive blocks until active-transaction count drops to zero (REQ000308).
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
		}
	}
}

// ForceAbortAll transitions every active slot to Aborted.
func (m *Manager) ForceAbortAll() int {
	count := 0
	for i := 0; i < MaxConcurrentTXNs; i++ {
		s := m.sm.GetSlot(i)
		if s == nil {
			continue
		}
		if SlotStatus(s.status.Load()) == SlotActive {
			s.status.Store(int32(SlotAborted))
			m.aborted.Add(1)
			count++
		}
	}
	return count
}
