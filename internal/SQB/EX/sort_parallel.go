package EX

import (
	"context"
	"slices"
	"sync"

	PL "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// SortOrder specifies ascending or descending order.
type SortOrder int

const (
	AscOrder SortOrder = iota
	DescOrder
)

// SortKey identifies a column to sort by.
type SortKey struct {
	ColName string
	Order   SortOrder
}

// ParallelSort performs a parallel sample sort:
// 1. Sample the input to find N-1 splitter values
// 2. Partition the data by splitter ranges
// 3. Each worker sorts its partition in parallel
// 4. Merge the sorted partitions into the output
// For small datasets (≤1024 rows), uses sequential sort to
// avoid partition overhead.
// REQ000145 satisfied (partial): Parallel sort via sample sort.
type ParallelSort struct {
	source     *OP.VectorizedSeqScan
	keys       []SortKey
	keyIndices []int
	pool       *UT.WorkerPool
	rows       []DT.Row
	pos        int
	done       bool
}

// NewParallelSort creates a parallel sort. If pool is nil,
// uses sequential sort.
func NewParallelSort(source *OP.VectorizedSeqScan, keys []SortKey, pool *UT.WorkerPool) *ParallelSort {
	return &ParallelSort{
		source: source,
		keys:   keys,
		pool:   pool,
	}
}

// NextBatch returns a batch with sorted data. Currently returns
// the entire sorted dataset in a single batch (or multiple
// batches if data exceeds UT.BatchSize).
func (s *ParallelSort) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if s.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Materialize all rows from the source (first call only)
	if s.rows == nil {
		for {
			batch, err := s.source.NextBatch(ctx)
			if err != nil {
				return nil, err
			}
			if batch == nil {
				break
			}
			rows := batchToRows(batch)
			s.rows = append(s.rows, rows...)
			batch.Put()
		}

		// OP.Sort (parallel or sequential)
		if s.pool != nil && len(s.rows) > 1024 {
			s.parallelSort()
		} else {
			s.sequentialSort()
		}
	}

	// Return batch from current position
	batch, err := s.batchFromPos()
	if err != nil {
		return nil, err
	}
	if batch == nil {
		s.done = true
		return nil, nil
	}
	return batch, nil
}

// parallelSort implements sample sort.
func (s *ParallelSort) parallelSort() {
	workers := s.pool.Workers()
	if workers < 2 || len(s.rows) < workers*2 {
		s.sequentialSort()
		return
	}

	// Pre-compute column indices for O(1) row access (REQ001018).
	if s.keyIndices == nil && len(s.rows) > 0 {
		s.keyIndices = buildKeyIndices(s.keys, s.rows[0])
	}

	// 1. Sample: pick 1 sample per partition
	sampleStep := len(s.rows) / (workers * 4)
	if sampleStep < 1 {
		sampleStep = 1
	}
	samples := make([]DT.Row, 0, workers-1)
	for i := 0; i < workers-1; i++ {
		idx := (i + 1) * sampleStep
		if idx < len(s.rows) {
			samples = append(samples, s.rows[idx])
		}
	}
	// OP.Sort the samples
	sortSample(samples, s.keys, s.keyIndices)

	// 2. Pick splitters (every 4th sample)
	splitters := make([]DT.Row, 0, workers-1)
	for i := 0; i < len(samples); i += 4 {
		splitters = append(splitters, samples[i])
	}

	// 3. Partition by splitters
	partitions := partitionBySplitters(s.rows, splitters, s.keys, s.keyIndices)

	// 4. OP.Sort each partition in parallel
	sorted := make([][]DT.Row, len(partitions))
	var wg sync.WaitGroup
	for i, p := range partitions {
		wg.Add(1)
		err := s.pool.Submit(context.Background(), func() error {
			defer wg.Done()
			slices.SortStableFunc(p, func(a, b DT.Row) int {
				if lessRowIdx(a, b, s.keys, s.keyIndices) {
					return -1
				}
				if lessRowIdx(b, a, s.keys, s.keyIndices) {
					return 1
				}
				return 0
			})
			sorted[i] = p
			return nil
		})
		if err != nil {
			wg.Done()
			s.sequentialSort()
			return
		}
	}
	wg.Wait()

	// 5. Concatenate sorted partitions
	total := 0
	for _, p := range sorted {
		total += len(p)
	}
	merged := make([]DT.Row, 0, total)
	for _, p := range sorted {
		merged = append(merged, p...)
	}
	s.rows = merged
}

// sequentialSort uses Go's built-in sort.
func (s *ParallelSort) sequentialSort() {
	if s.keyIndices == nil && len(s.rows) > 0 {
		s.keyIndices = buildKeyIndices(s.keys, s.rows[0])
	}
	slices.SortStableFunc(s.rows, func(a, b DT.Row) int {
		if lessRowIdx(a, b, s.keys, s.keyIndices) {
			return -1
		}
		if lessRowIdx(b, a, s.keys, s.keyIndices) {
			return 1
		}
		return 0
	})
}

// firstBatch returns the first batch from the sorted rows.
func (s *ParallelSort) batchFromPos() (*UT.Batch, error) {
	if s.pos >= len(s.rows) {
		return nil, nil
	}
	schema := make([]string, 0)
	types := make([]LX.TokenType, 0)
	if len(s.rows) > 0 {
		schema = s.rows[0].Cols
		for _, t := range s.rows[0].Types {
			types = append(types, LX.TokenType(t))
		}
	}

	batch := UT.GetBatch(len(schema))
	for i, name := range schema {
		batch.SetColumnName(i, name)
	}

	end := s.pos + UT.BatchSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.pos; i < end; i++ {
		row := s.rows[i]
		for j, colName := range schema {
			val, ok := row.Lookup(colName)
			if !ok {
				val = nil
			}
			isNull := val == nil
			var typ LX.TokenType
			if j < len(types) {
				typ = types[j]
			}
			batch.AppendRow(j, typ, val, isNull)
		}
		batch.AdvanceSize()
	}

	// Truncate Data slices to logical size to avoid exposing
	// uninitialized zero values from the pre-allocated pool.
	for i := range batch.Cols {
		batch.Cols[i].Data = truncateColumnData(batch.Cols[i].Data, batch.Size)
		if batch.Cols[i].Nulls != nil {
			batch.Cols[i].Nulls = batch.Cols[i].Nulls[:batch.Size]
		}
	}

	s.pos = end
	if batch.Size == 0 {
		batch.Put()
		return nil, nil
	}
	return batch, nil
}

// truncateColumnData shrinks a column's Data slices to the
// specified logical size.
func truncateColumnData(data UT.ColumnData, size int) UT.ColumnData {
	if size == 0 {
		return UT.ColumnData{}
	}
	if data.Ints != nil {
		data.Ints = data.Ints[:size]
	} else if data.Floats != nil {
		data.Floats = data.Floats[:size]
	} else if data.Strs != nil {
		data.Strs = data.Strs[:size]
	} else if data.Bools != nil {
		data.Bools = data.Bools[:size]
	}
	return data
}

func (s *ParallelSort) Close() error {
	if s.source != nil {
		return s.source.Close()
	}
	return nil
}

// sortSample sorts a small sample of rows by the given keys.
func sortSample(rows []DT.Row, keys []SortKey, indices []int) {
	if len(rows) == 0 {
		return
	}
	if indices == nil {
		indices = buildKeyIndices(keys, rows[0])
	}
	slices.SortStableFunc(rows, func(a, b DT.Row) int {
		if lessRowIdx(a, b, keys, indices) {
			return -1
		}
		if lessRowIdx(b, a, keys, indices) {
			return 1
		}
		return 0
	})
}

// partitionBySplitters divides rows into partitions based on
// splitter rows. Rows < splitter[0] go to partition 0, etc.
func partitionBySplitters(rows []DT.Row, splitters []DT.Row, keys []SortKey, indices []int) [][]DT.Row {
	partitions := make([][]DT.Row, len(splitters)+1)
	for _, row := range rows {
		placed := false
		for i, splitter := range splitters {
			if lessRowIdx(row, splitter, keys, indices) {
				partitions[i] = append(partitions[i], row)
				placed = true
				break
			}
		}
		if !placed {
			partitions[len(splitters)] = append(partitions[len(splitters)], row)
		}
	}
	return partitions
}

// lessRowIdx compares two rows using pre-computed column indices.
// Avoids map lookups in DT.Row.Lookup by indexing directly into Data.
// REQ001018.
func lessRowIdx(a, b DT.Row, keys []SortKey, indices []int) bool {
	for i, key := range keys {
		if i >= len(indices) {
			continue
		}
		idx := indices[i]
		if idx < 0 {
			continue
		}
		av := DT.Value{}
		bv := DT.Value{}
		if idx < len(a.Data) {
			av = a.Data[idx]
		}
		if idx < len(b.Data) {
			bv = b.Data[idx]
		}
		cmp := PL.CompareValue(av, bv)
		if cmp == 0 {
			continue
		}
		if key.Order == AscOrder {
			return cmp < 0
		}
		return cmp > 0
	}
	return false
}

// buildKeyIndices pre-computes column indices from sort key names
// for O(1) DT.Row.Data access during sort comparisons. REQ001018.
func buildKeyIndices(keys []SortKey, first DT.Row) []int {
	indices := make([]int, len(keys))
	for i, key := range keys {
		idx := -1
		for j, c := range first.Cols {
			if c == key.ColName {
				idx = j
				break
			}
		}
		indices[i] = idx
	}
	return indices
}

// lessRow compares two rows by the sort keys, in order.
// Deprecated: use lessRowIdx for better performance. REQ001018.
func lessRow(a, b DT.Row, keys []SortKey) bool {
	for _, key := range keys {
		av, aok := a.Lookup(key.ColName)
		bv, bok := b.Lookup(key.ColName)
		if !aok && !bok {
			continue
		}
		// Compare values
		cmp := compareValues(av, bv)
		if cmp == 0 {
			continue
		}
		if key.Order == AscOrder {
			return cmp < 0
		}
		return cmp > 0
	}
	return false
}

// compareValues returns -1, 0, or 1 for a vs b.
func compareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch av := a.(type) {
	case int64:
		if bv, ok := b.(int64); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	case float64:
		if bv, ok := b.(float64); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	case string:
		if bv, ok := b.(string); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	case bool:
		if bv, ok := b.(bool); ok {
			if av == bv {
				return 0
			}
			if !av {
				return -1
			}
			return 1
		}
	}
	return 0
}

// batchToRows converts a columnar batch back to rows (for sorting).
func batchToRows(batch *UT.Batch) []DT.Row {
	if batch == nil || batch.Size == 0 {
		return nil
	}
	rows := make([]DT.Row, 0, batch.Size)
	cols := make([]string, 0, len(batch.Cols))
	types := make([]LX.TokenType, 0, len(batch.Cols))
	for i := range batch.Cols {
		if batch.Cols[i].Name != "" {
			cols = append(cols, batch.Cols[i].Name)
		} else {
			cols = append(cols, "c"+itoaSimple(i))
		}
		types = append(types, batch.Cols[i].Type)
	}

	for i := 0; i < batch.Size; i++ {
		row := DT.Row{
			Cols:  cols,
			Types: types,
			Data:  make([]DT.Value, len(cols)),
		}
		for j, colName := range cols {
			row.Data[j] = DT.ValueFromAny(EV.BatchValueAt(batch.Cols[j], i))
			_ = colName
		}
		rows = append(rows, row)
	}
	return rows
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
