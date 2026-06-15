//go:build edge_probe

package slt

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// TestEdge_ConcurrentReaders probes race conditions when
// multiple goroutines hit the same RazorDriver. The driver
// is not designed for concurrent use; we measure the
// failure mode to see whether it crashes or just returns
// errors.
func TestEdge_ConcurrentReaders(t *testing.T) {
	ctx := context.Background()
	d := NewRazorDriver()
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close(ctx) })

	if err := d.Exec(ctx, "CREATE TABLE t (id INT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 50; i++ {
		if err := d.Exec(ctx,
			"INSERT INTO t VALUES ("+itoaEdge(i)+")"); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var ok, fail int64
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, err := d.Query(ctx, "SELECT id FROM t ORDER BY id")
				if err != nil {
					atomic.AddInt64(&fail, 1)
				} else {
					atomic.AddInt64(&ok, 1)
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("concurrent reads: ok=%d fail=%d", ok, fail)
	if fail > ok {
		t.Errorf("more failures than successes: %d vs %d", fail, ok)
	}
}

func itoaEdge(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
