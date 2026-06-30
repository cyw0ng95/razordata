package UT

import (
	"context"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"time"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	"github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// Analyze is the DDL operator for ANALYZE. REQ000258.
// It scans the table, collects column statistics using
// reservoir sampling, builds histograms, and persists
// the stats to the catalog.
type Analyze struct {
	stmt    *PS.AnalyzeStmt
	store   Store
	schema  *StoreSchema
	done    bool
	rowsAff int64
}

func NewAnalyze(stmt *PS.AnalyzeStmt) *Analyze {
	return &Analyze{stmt: stmt}
}

// NewAnalyzeWithStore builds an Analyze that reads from the engine.
func NewAnalyzeWithStore(store Store, stmt *PS.AnalyzeStmt) (*Analyze, error) {
	ss, ok := DT.SchemaFor(stmt.Table)
	if !ok && stmt.Table != "" {
		return nil, DT.ErrTableNotRegisteredForStorage
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

	// Analyze all DT.Tables
	// For now, just return success (empty implementation for phase 1)
	a.rowsAff = 0
	return Row{}, ErrNoRows
}

func (a *Analyze) Close() error { return nil }

func (a *Analyze) RowsAffected() int64 { return a.rowsAff }

func (a *Analyze) WithParams(p []any) Operator { return a }

// analyzeTable performs reservoir sampling on a single table
// and builds column statistics. REQ000258.
func (a *Analyze) analyzeTable(ctx context.Context, tableName string) error {
	cat := DT.Catalog()
	if cat == nil {
		return nil
	}

	// Look up the catalog's table ID for this table (the EX-layer's
	// tableID is not the same as the catalog's internal table ID).
	var catTableID uint64
	var catIDFound bool
	for _, e := range cat.List() {
		if e.Name == tableName {
			catTableID = e.TableID
			catIDFound = true
			break
		}
	}
	if !catIDFound {
		// Table not registered in the catalog — can't persist stats.
		return nil
	}

	ss, ok := DT.SchemaFor(tableName)
	if !ok {
		return DT.ErrTableNotRegisteredForStorage
	}

	if a.store == nil || ss == nil {
		return nil
	}

	const sampleSize = 10000
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(time.Now().UnixNano()+1)))

	nCols := len(ss.Cols)
	type colInfo struct {
		nullCount  int64
		distinct   map[string]struct{}
		minValue   []byte
		maxValue   []byte
		reservoir  [][]byte
		reservoirN int64
	}
	cols := make([]colInfo, nCols)
	for i := range cols {
		cols[i].distinct = make(map[string]struct{})
		cols[i].reservoir = make([][]byte, 0, sampleSize)
	}

	rowCount := int64(0)
	prefix := DT.TablePrefix(tableName)
	if prefix == nil {
		return DT.ErrTableNotRegisteredForStorage
	}
	it := a.store.NewIterator(prefix)
	defer it.Close()

	var rowBuf Row
	var err error
	for it.Next() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rowCount++
		encoded := it.Value()
		if len(encoded) == 0 {
			continue
		}
		rowBuf, err = DT.DecodeRow(encoded, ss)
		if err != nil {
			continue
		}
		for i, v := range rowBuf.Data {
			ci := &cols[i]
			if v.IsNull() {
				ci.nullCount++
				continue
			}

			b := valueToBytes(v)
			if b == nil {
				continue
			}
			key := string(b)

			ci.distinct[key] = struct{}{}

			if ci.minValue == nil || key < string(ci.minValue) {
				ci.minValue = append(ci.minValue[:0], b...)
			}
			if ci.maxValue == nil || key > string(ci.maxValue) {
				ci.maxValue = append(ci.maxValue[:0], b...)
			}

			ci.reservoirN++
			if len(ci.reservoir) < sampleSize {
				ci.reservoir = append(ci.reservoir, append([]byte(nil), b...))
			} else {
				j := rng.IntN(int(ci.reservoirN))
				if j < sampleSize {
					ci.reservoir[j] = append(ci.reservoir[j][:0], b...)
				}
			}
		}
	}
	if err := it.Err(); err != nil {
		return err
	}

	for i, colName := range ss.Cols {
		ci := &cols[i]
		stats := ls.ColumnStats{
			DistinctCount: int64(len(ci.distinct)),
			NullCount:     ci.nullCount,
			MinValue:      ci.minValue,
			MaxValue:      ci.maxValue,
			Histogram:     buildHistogram(ci.reservoir, 256),
			RowCount:      rowCount,
		}
		if err := cat.PutStats(catTableID, colName, stats); err != nil {
			return err
		}
	}

	lm := PL.Learned()
	histograms := make([]struct {
		Count         int64
		Lower, Upper  []byte
		TotalRows     int64
		DistinctCount int64
		NullCount     int64
	}, 0)
	for _, ci := range cols {
		for _, b := range buildHistogram(ci.reservoir, 256) {
			histograms = append(histograms, struct {
				Count         int64
				Lower, Upper  []byte
				TotalRows     int64
				DistinctCount int64
				NullCount     int64
			}{
				Count:         b.Count,
				Lower:         b.LowerBound,
				Upper:         b.UpperBound,
				TotalRows:     rowCount,
				DistinctCount: int64(len(ci.distinct)),
				NullCount:     ci.nullCount,
			})
		}
	}
	lm.BootstrapFromHistograms(histograms)

	return nil
}

// valueToBytes converts a Value to a sortable byte representation
// suitable for distinct tracking, min/max, and histogram building.
func valueToBytes(v Value) []byte {
	switch v.Kind {
	case DT.KindInt:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(v.I64))
		return b[:]
	case DT.KindFloat:
		var b [8]byte
		bits := math.Float64bits(v.F64)
		// Flip sign bit for negative values so byte ordering matches float ordering
		if bits&(1<<63) != 0 {
			bits = ^bits
		} else {
			bits ^= 1 << 63
		}
		binary.BigEndian.PutUint64(b[:], bits)
		return b[:]
	case DT.KindText:
		return []byte(v.S)
	case DT.KindBool:
		if v.Bo {
			return []byte{1}
		}
		return []byte{0}
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
// It reclaims tombstone space by triggering LSM compaction.
type Vacuum struct {
	stmt    *PS.VacuumStmt
	store   Store
	done    bool
	rowsAff int64
}

func NewVacuum(stmt *PS.VacuumStmt) *Vacuum {
	return &Vacuum{stmt: stmt}
}

// NewVacuumWithStore creates Vacuum with store access for compaction.
func NewVacuumWithStore(stmt *PS.VacuumStmt, store Store) *Vacuum {
	return &Vacuum{stmt: stmt, store: store}
}

func (v *Vacuum) Next(ctx context.Context) (Row, error) {
	if v.done {
		return Row{}, ErrNoRows
	}
	v.done = true

	// Trigger manual compaction to reclaim tombstone space
	if v.store != nil {
		if err := v.store.ManualCompact(); err != nil {
			// Compaction already in progress or failed
			// Still return success to user (best effort)
		}
	}

	v.rowsAff = 1
	return Row{}, ErrNoRows
}

func (v *Vacuum) Close() error { return nil }

func (v *Vacuum) RowsAffected() int64 { return v.rowsAff }

func (v *Vacuum) WithParams(p []any) Operator { return v }
