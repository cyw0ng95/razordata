package ls

// ColumnStats captures per-column selectivity statistics used by
// the planner for cost-based index selection. REQ000254.
// The stats are stored in the catalog alongside the table entry.
// They are populated by ANALYZE (manual in iter-22, auto on flush
// in a future iteration) and queried by the planner's
// estimateSelectivity function.
type ColumnStats struct {
	// DistinctCount is the number of distinct values observed in
	// the column. Used as the denominator for uniform-distribution
	// selectivity estimation when no histogram is available.
	DistinctCount int64

	// NullCount is the number of NULL values observed. Used to
	// adjust selectivity for IS NULL / IS NOT NULL predicates.
	NullCount int64

	// MinValue and MaxValue bound the value range. Used for range
	// scan cost estimation and to skip scans that cannot match.
	MinValue []byte
	MaxValue []byte

	// Histogram is an optional equi-depth histogram with at most
	// 256 buckets. When present, the planner uses linear
	// interpolation between bucket boundaries to estimate
	// selectivity for range predicates. When absent, the planner
	// falls back to uniform distribution.
	Histogram []HistogramBucket

	// RowCount is the total number of rows sampled. Used as the
	// denominator when scaling per-bucket counts.
	RowCount int64
}

// HistogramBucket is one bucket in an equi-depth histogram.
// LowerBound and UpperBound are inclusive (closed interval) for
// the last bucket, exclusive (half-open) for all others.
type HistogramBucket struct {
	LowerBound []byte
	UpperBound []byte
	// Count is the number of rows whose value falls in this bucket.
	Count int64
}

// StatsEntry maps a column name to its ColumnStats within a table.
// The catalog stores one StatsEntry per indexed column (lazily
// populated by ANALYZE).
type StatsEntry struct {
	TableID uint64
	Column  string
	Stats   ColumnStats
}
