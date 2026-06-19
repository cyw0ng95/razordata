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

type HistogramBucket struct {
	LowerBound []byte
	UpperBound []byte
	Count      int64
}

// StatsEntry maps a column name to its ColumnStats.
type StatsEntry struct {
	TableID uint64
	Column  string
	Stats   ColumnStats
}
