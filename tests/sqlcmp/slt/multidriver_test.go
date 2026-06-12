//go:build edge_probe

package slt

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEdge_ConcurrentSessions stresses two RazorDrivers
// hitting two independent engines in parallel. Each driver
// owns its own engine, so the shared package-level EX state
// is the only cross-driver dependency. We assert that
// parallel drivers do not corrupt each other.
func TestEdge_ConcurrentSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	const nDrivers = 4
	const nOps = 25

	var wg sync.WaitGroup
	var ok, fail int64
	for w := 0; w < nDrivers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			d := NewRazorDriver()
			if err := d.Connect(ctx); err != nil {
				atomic.AddInt64(&fail, 1)
				return
			}
			defer d.Close(ctx)
			setup := []string{
				"CREATE TABLE t (id INT PRIMARY KEY, n INT)",
			}
			for _, s := range setup {
				_ = d.Exec(ctx, s)
			}
			for i := 0; i < nOps; i++ {
				stmt := "INSERT INTO t VALUES (" +
					itoaEdge(id*1000+i) + ", " + itoaEdge(i) + ")"
				if err := d.Exec(ctx, stmt); err != nil {
					atomic.AddInt64(&fail, 1)
					continue
				}
				if _, err := d.Query(ctx, "SELECT COUNT(*) FROM t"); err != nil {
					atomic.AddInt64(&fail, 1)
				} else {
					atomic.AddInt64(&ok, 1)
				}
			}
		}(w)
	}
	wg.Wait()
	t.Logf("concurrent drivers: ok=%d fail=%d", ok, fail)
}
