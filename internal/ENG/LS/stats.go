package ls

// ColumnStats captures per-column selectivity statistics (REQ000254).
type ColumnStats struct {
	DistinctCount int64
	NullCount     int64
	MinValue      []byte
	MaxValue      []byte
	Histogram     []HistogramBucket
	RowCount      int64
}

// HistogramBucket represents a single bucket in a column value histogram,
// spanning [LowerBound, UpperBound] with Count rows.
type HistogramBucket struct {
	LowerBound []byte
	UpperBound []byte
	Count      int64
}

// StatsEntry maps a column name to its ColumnStats.
// StatsEntry maps a table column to its collected statistics.
type StatsEntry struct {
	TableID uint64
	Column  string
	Stats   ColumnStats
}
