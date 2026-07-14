// Catalog/Stats reader interfaces. SQO/OC needs table/index
// metadata and column statistics to make optimization decisions,
// but it must not import SQB/EX or ENG/LS. Concrete
// implementations live in SQB/EX (a small adapter that wraps the
// in-memory catalog) and are passed in via Context at optimize
// time.
//
// REQ001432: scaffold only — no implementations yet. REQ001435
// (OperatorFactory) and REQ001442 (resolve_slots wire-in) bring
// the first concrete adapters.
package OC

// CatalogReader exposes the table and index metadata an
// optimizer pass needs. Implemented by SQB/EX over its in-memory
// `catalog` map.
type CatalogReader interface {
	// TableColumns returns the column names of a table in
	// declared order. Returns nil if the table does not exist.
	TableColumns(table string) []string
	// Indexes returns the index names defined on a table.
	Indexes(table string) []string
	// IndexColumns returns the column names of an index, in
	// the order the index keys them. Returns nil if the index
	// does not exist.
	IndexColumns(table, index string) []string
	// TablePK returns the primary-key column name of a table,
	// or "" if the table has no PK.
	TablePK(table string) string
}

// StatsReader exposes per-column statistics for selectivity
// estimation. Implemented by SQB/EX over pl.StatsCatalog.
type StatsReader interface {
	// NDV returns the number of distinct values in a column.
	// Returns -1 if no stats are available.
	NDV(table, column string) int64
	// RowCount returns the estimated row count for a table.
	// Returns -1 if no stats are available.
	RowCount(table string) int64
	// NullCount returns the number of NULL values in a column.
	// Returns -1 if no stats are available.
	NullCount(table, column string) int64
	// Min returns the minimum value in a column (raw bytes).
	// Returns nil if no stats are available.
	Min(table, column string) []byte
	// Max returns the maximum value in a column (raw bytes).
	// Returns nil if no stats are available.
	Max(table, column string) []byte
}

// Compile-time assertions: CatalogReader and StatsReader are
// interfaces and may be nil. No implementation is required in
// the OC package itself.
var (
	_ CatalogReader = (CatalogReader)(nil)
	_ StatsReader   = (StatsReader)(nil)
)
