package ls

// ColumnStats captures per-column selectivity statistics (REQ000254).
// REQ001057b: MostCommonVals + MostCommonFreqs enable MCV-based
// selectivity estimation for IN-list and equality predicates
// (CockroachDB / PostgreSQL model).
type ColumnStats struct {
	DistinctCount int64
	NullCount     int64
	MinValue      []byte
	MaxValue      []byte
	Histogram     []HistogramBucket
	RowCount      int64
	// MostCommonVals lists the top-K most frequent distinct values
	// in the column, parallel to MostCommonFreqs. Index i in
	// MostCommonVals has frequency MostCommonFreqs[i] (0 ≤ f ≤ 1).
	// Both slices must have the same length, or both be nil.
	MostCommonVals  [][]byte
	MostCommonFreqs []float64
}

// TableStats aggregates column-level statistics for a table, used by
// the planner for statistics-driven cost estimation (REQ000787).
type TableStats struct {
	RowCount      int64
	ColStats      map[string]*ColumnStats // colName -> ColumnStats
	TotalWidth    int                     // avg row width in bytes
	LastAnalyzed  int64                   // unix nanos
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
