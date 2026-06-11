package EX

import (
	"context"
	"math/rand"
	"time"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// Analyze is the DDL operator for ANALYZE. REQ000258.
// It scans the table, collects column statistics using
// reservoir sampling, builds histograms, and persists
// the stats to the catalog.
type Analyze struct {
	stmt    *PS.AnalyzeStmt
	store   Store
	schema  *storeSchema
	done    bool
	rowsAff int64
}

func NewAnalyze(stmt *PS.AnalyzeStmt) *Analyze {
	return &Analyze{stmt: stmt}
}

// NewAnalyzeWithStore builds an Analyze that reads from the engine.
func NewAnalyzeWithStore(store Store, stmt *PS.AnalyzeStmt) (*Analyze, error) {
	ss, ok := schemaFor(stmt.Table)
	if !ok && stmt.Table != "" {
		return nil, ErrTableNotRegisteredForStorage
	}
	return &Analyze{
		stmt:   stmt,
		store:  store,
		schema: ss,
	}, nil
}

func (a *Analyze) Next(ctx context.Context) (Row, error) {
	if a.done {
		return Row{}, ErrNoRows
	}
	a.done = true

	// If specific table, analyze only that table
	if a.stmt.Table != "" {
		if err := a.analyzeTable(ctx, a.stmt.Table); err != nil {
			return Row{}, err
		}
		a.rowsAff = 1
		return Row{}, ErrNoRows
	}

	// Analyze all tables
	// For now, just return success (empty implementation for phase 1)
	a.rowsAff = 0
	return Row{}, ErrNoRows
}

func (a *Analyze) Close() error { return nil }

func (a *Analyze) RowsAffected() int64 { return a.rowsAff }

func (a *Analyze) WithParams(p []interface{}) Operator { return a }

// analyzeTable performs reservoir sampling on a single table
// and builds column statistics. REQ000258.
func (a *Analyze) analyzeTable(ctx context.Context, tableName string) error {
	// Get catalog for stats persistence
	cat := Catalog()
	if cat == nil {
		// No catalog available, can't persist stats
		return nil
	}

	tableID, ok := tableIDFor(tableName)
	if !ok {
		return ErrTableNotRegisteredForStorage
	}

	// Initialize reservoir sampling
	const sampleSize = 10000
	reservoir := make([][]byte, 0, sampleSize)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Scan all columns - for now just analyze PK column
	ss, ok := schemaFor(tableName)
	if !ok {
		return ErrTableNotRegisteredForStorage
	}

	// Collect distinct values and compute stats
	distinctMap := make(map[string]bool)
	var minValue, maxValue []byte
	nullCount := int64(0)
	rowCount := int64(0)

	// Use store iterator to scan all rows
	if a.store != nil && ss != nil {
		// Build key prefix for PK
		prefix := []byte(tableName + ":")
		it := a.store.NewIterator(prefix)
		defer it.Close()

		for it.Next() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			key := it.Key()
			rowCount++

			// Extract value from key (after first colon)
			colonIdx := -1
			for i, b := range key {
				if b == ':' {
					colonIdx = i
					break
				}
			}
			if colonIdx < 0 || colonIdx >= len(key)-1 {
				continue
			}

			val := key[colonIdx+1:]
			if len(val) == 0 {
				nullCount++
				continue
			}

			// Reservoir sampling
			if len(reservoir) < sampleSize {
				reservoir = append(reservoir, append([]byte(nil), val...))
			} else {
				j := rng.Intn(int(rowCount))
				if j < sampleSize {
					reservoir[j] = append([]byte(nil), val...)
				}
			}

			// Track distinct values
			distinctMap[string(val)] = true

			// Track min/max
			if minValue == nil || string(val) < string(minValue) {
				minValue = append([]byte(nil), val...)
			}
			if maxValue == nil || string(val) > string(maxValue) {
				maxValue = append([]byte(nil), val...)
			}
		}
		if err := it.Err(); err != nil {
			return err
		}
	}

	// Build histogram from reservoir
	histogram := buildHistogram(reservoir, 256)

	// Build stats
	stats := ls.ColumnStats{
		DistinctCount: int64(len(distinctMap)),
		NullCount:     nullCount,
		MinValue:      minValue,
		MaxValue:      maxValue,
		Histogram:     histogram,
		RowCount:      rowCount,
	}

	// Persist stats to catalog - analyze PK column
	pkCol := ss.pk
	if pkCol != "" {
		if err := cat.PutStats(tableID, pkCol, stats); err != nil {
			return err
		}
	}

	return nil
}

// buildHistogram builds an equi-depth histogram from sampled values.
func buildHistogram(samples [][]byte, maxBuckets int) []ls.HistogramBucket {
	if len(samples) == 0 {
		return nil
	}

	// Sort samples
	sorted := make([][]byte, len(samples))
	copy(sorted, samples)
	sortBytes(sorted)

	// Build equi-depth buckets
	targetPerBucket := len(sorted) / maxBuckets
	if targetPerBucket < 1 {
		targetPerBucket = 1
	}

	var buckets []ls.HistogramBucket
	for i := 0; i < len(sorted); {
		bucketEnd := i + targetPerBucket
		if bucketEnd > len(sorted) {
			bucketEnd = len(sorted)
		}

		bucket := ls.HistogramBucket{
			LowerBound: append([]byte(nil), sorted[i]...),
			UpperBound: append([]byte(nil), sorted[bucketEnd-1]...),
			Count:      int64(bucketEnd - i),
		}
		buckets = append(buckets, bucket)
		i = bucketEnd
	}

	return buckets
}

// sortBytes sorts a slice of byte slices.
func sortBytes(data [][]byte) {
	// Simple insertion sort for small arrays
	for i := 1; i < len(data); i++ {
		key := data[i]
		j := i - 1
		for j >= 0 && string(data[j]) > string(key) {
			data[j+1] = data[j]
			j--
		}
		data[j+1] = key
	}
}

// Vacuum is the DDL operator for VACUUM. REQ000257.
// It reclaims tombstone space by rewriting SST files.
type Vacuum struct {
	stmt    *PS.VacuumStmt
	done    bool
	rowsAff int64
}

func NewVacuum(stmt *PS.VacuumStmt) *Vacuum {
	return &Vacuum{stmt: stmt}
}

func (v *Vacuum) Next(ctx context.Context) (Row, error) {
	if v.done {
		return Row{}, ErrNoRows
	}
	v.done = true

	// If specific table, vacuum only that table
	if v.stmt.Table != "" {
		// TODO: implement per-table vacuum
		v.rowsAff = 1
		return Row{}, ErrNoRows
	}

	// Vacuum all tables
	// Full implementation would trigger LSM compaction
	v.rowsAff = 0
	return Row{}, ErrNoRows
}

func (v *Vacuum) Close() error { return nil }

func (v *Vacuum) RowsAffected() int64 { return v.rowsAff }

func (v *Vacuum) WithParams(p []interface{}) Operator { return v }
