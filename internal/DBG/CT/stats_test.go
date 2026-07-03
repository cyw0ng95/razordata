//go:build debug

package ct

import (
	"sync"
	"testing"
)

func TestCounters_ConcurrentIncrement(t *testing.T) {
	var s DebugStats
	const goroutines = 16
	const iterations = 1000

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				s.QueriesTotal.Add(1)
				s.RowsReturned.Add(1)
				s.PagesRead.Add(1)
			}
		}()
	}
	wg.Wait()

	snap := s.Snapshot()
	expected := int64(goroutines * iterations)
	if snap["queries_total"] != expected {
		t.Errorf("queries_total: want %d, got %d", expected, snap["queries_total"])
	}
	if snap["rows_returned"] != expected {
		t.Errorf("rows_returned: want %d, got %d", expected, snap["rows_returned"])
	}
}
