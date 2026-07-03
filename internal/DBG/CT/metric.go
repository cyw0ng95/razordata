//go:build debug

package ct

import (
	"expvar"
	"sync"
	"time"

	"github.com/cyw0ng95/razordata/internal/LOG/HK"
)

var histograms = map[string]*LatencyHist{
	"query_latency":      {},
	"commit_latency":     {},
	"page_read_latency":  {},
	"page_write_latency": {},
	"compaction_latency": {},
}

var expvarOnce sync.Once

type hook struct{}

// NewMetricHook returns a MetricSink backed by GlobalStats + expvar.
func NewMetricHook() hk.MetricSink {
	expvarOnce.Do(func() {
		expvar.Publish("razordata", &GlobalStats)
	})
	return &hook{}
}

func (h *hook) Observe(counter string, value int64) {
	switch counter {
	case "queries_total":
		GlobalStats.QueriesTotal.Add(value)
	case "queries_slow":
		GlobalStats.QueriesSlow.Add(value)
	case "rows_returned":
		GlobalStats.RowsReturned.Add(value)
	case "rows_written":
		GlobalStats.RowsWritten.Add(value)
	case "pages_read":
		GlobalStats.PagesRead.Add(value)
	case "pages_written":
		GlobalStats.PagesWritten.Add(value)
	case "wal_bytes_written":
		GlobalStats.WALBytesWritten.Add(value)
	case "wal_fsyncs":
		GlobalStats.WALFsyncs.Add(value)
	case "compactions_total":
		GlobalStats.CompactionsTotal.Add(value)
	case "compactions_bytes":
		GlobalStats.CompactionsBytes.Add(value)
	case "cache_hits":
		GlobalStats.CacheHits.Add(value)
	case "cache_misses":
		GlobalStats.CacheMisses.Add(value)
	case "txn_commits":
		GlobalStats.TxnCommits.Add(value)
	case "txn_aborts":
		GlobalStats.TxnAborts.Add(value)
	case "lock_contention_ns":
		GlobalStats.LockContentionNs.Add(value)
	}
}

func (h *hook) ObserveLatency(histogram string, d time.Duration) {
	if hist, ok := histograms[histogram]; ok {
		hist.Observe(d)
	}
}

func (h *hook) Snapshot() hk.MetricSnapshot {
	snap := hk.MetricSnapshot{
		Counters:   GlobalStats.Snapshot(),
		Histograms: make(map[string]hk.LatencyHistSnapshot),
	}
	for name, hist := range histograms {
		snap.Histograms[name] = hist.Snapshot()
	}
	return snap
}
