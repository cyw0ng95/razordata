//go:build debug

package ct

import "sync/atomic"

// DebugStats holds all atomic counters for the debug subsystem.
type DebugStats struct {
	QueriesTotal     atomic.Int64
	QueriesSlow      atomic.Int64
	RowsReturned     atomic.Int64
	RowsWritten      atomic.Int64
	PagesRead        atomic.Int64
	PagesWritten     atomic.Int64
	WALBytesWritten  atomic.Int64
	WALFsyncs        atomic.Int64
	CompactionsTotal atomic.Int64
	CompactionsBytes atomic.Int64
	CacheHits        atomic.Int64
	CacheMisses      atomic.Int64
	TxnCommits       atomic.Int64
	TxnAborts        atomic.Int64
	LockContentionNs atomic.Int64
}

// Snapshot returns a point-in-time read of all counters.
func (s *DebugStats) Snapshot() map[string]int64 {
	return map[string]int64{
		"queries_total":      s.QueriesTotal.Load(),
		"queries_slow":       s.QueriesSlow.Load(),
		"rows_returned":      s.RowsReturned.Load(),
		"rows_written":       s.RowsWritten.Load(),
		"pages_read":         s.PagesRead.Load(),
		"pages_written":      s.PagesWritten.Load(),
		"wal_bytes_written":  s.WALBytesWritten.Load(),
		"wal_fsyncs":         s.WALFsyncs.Load(),
		"compactions_total":  s.CompactionsTotal.Load(),
		"compactions_bytes":  s.CompactionsBytes.Load(),
		"cache_hits":         s.CacheHits.Load(),
		"cache_misses":       s.CacheMisses.Load(),
		"txn_commits":        s.TxnCommits.Load(),
		"txn_aborts":         s.TxnAborts.Load(),
		"lock_contention_ns": s.LockContentionNs.Load(),
	}
}

// String implements expvar.Var.
func (s *DebugStats) String() string {
	return "{...}"
}

// GlobalStats is the global counter instance.
var GlobalStats DebugStats
