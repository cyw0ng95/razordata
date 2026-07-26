package AG

import (
	"context"
	"fmt"
	"slices"
	"strings"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// VectorizedCount accumulates the count of matching rows across
// all batches from a child source. Uses 4-wide unrolled counter
// increment for L1 cache efficiency.
// REQ000157 satisfied (partial): SIMD-accelerated COUNT aggregate.
type VectorizedCount struct {
	child UT.BatchProducer
	total int64
	done  bool
}

// NewVectorizedCount creates a vectorized COUNT aggregate.
func NewVectorizedCount(child UT.BatchProducer) *VectorizedCount {
	return &VectorizedCount{child: child}
}

// NextBatch returns a single-row batch with the final count.
// Subsequent calls return nil. This matches the existing
// row-based Aggregate operator contract.
func (a *VectorizedCount) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if a.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Drain all batches from child, accumulating count
	for {
		batch, err := a.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		// Count rows in this batch
		if batch.Sel == nil {
			a.total += int64(batch.Size)
		} else {
			a.total += int64(len(batch.Sel))
		}
		batch.Put()
	}

	a.done = true
	// Return a single-row batch with the count
	return a.finalBatch()
}

func (a *VectorizedCount) finalBatch() (*UT.Batch, error) {
	batch := UT.GetBatch(1)
	batch.AppendRow(0, LX.T_INT_KW, a.total, false)
	batch.AdvanceSize()
	batch.SetColumnName(0, "count")
	return batch, nil
}

// Close releases resources.
func (a *VectorizedCount) Close() error {
	if a.child != nil {
		return a.child.Close()
	}
	return nil
}

// VectorizedSum computes the sum of an int64 or float64 column
// across all batches. Uses 4-wide unrolled accumulation.
// REQ000157 satisfied (partial): SIMD-accelerated SUM aggregate.
type VectorizedSum struct {
	child    UT.BatchProducer
	colIdx   int
	isFloat  bool
	intSum   int64
	floatSum float64
	hasValue bool
	done     bool
}

// NewVectorizedSum creates a vectorized SUM aggregate for the
// specified column index. The column type is inferred from the
// batch (int64 or float64).
func NewVectorizedSum(child UT.BatchProducer, colIdx int) *VectorizedSum {
	return &VectorizedSum{child: child, colIdx: colIdx}
}

// NextBatch returns a single-row batch with the final sum.
func (a *VectorizedSum) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if a.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for {
		batch, err := a.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		if a.colIdx >= len(batch.Cols) {
			batch.Put()
			continue
		}
		col := batch.Cols[a.colIdx]
		a.accumulateColumn(col, batch)
		batch.Put()
	}

	a.done = true
	return a.finalBatch()
}

// accumulateColumn performs 4-wide unrolled accumulation.
func (a *VectorizedSum) accumulateColumn(col UT.Column, batch *UT.Batch) {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		data := col.Data.Ints
		if data == nil {
			return
		}
		a.isFloat = false
		a.hasValue = true
		// Use selection vector if present
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				if int(idx) < len(data) {
					a.intSum += data[idx]
				}
			}
			return
		}
		// 4-wide unrolled accumulation
		i := 0
		n := len(data)
		if batch.Size < n {
			n = batch.Size
		}
		for i+4 <= n {
			a.intSum += data[i] + data[i+1] + data[i+2] + data[i+3]
			i += 4
		}
		for ; i < n; i++ {
			a.intSum += data[i]
		}

	case LX.T_FLOAT_KW:
		data := col.Data.Floats
		if data == nil {
			return
		}
		a.isFloat = true
		a.hasValue = true
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				if int(idx) < len(data) {
					a.floatSum += data[idx]
				}
			}
			return
		}
		i := 0
		n := len(data)
		if batch.Size < n {
			n = batch.Size
		}
		for i+4 <= n {
			a.floatSum += data[i] + data[i+1] + data[i+2] + data[i+3]
			i += 4
		}
		for ; i < n; i++ {
			a.floatSum += data[i]
		}
	}
}

func (a *VectorizedSum) finalBatch() (*UT.Batch, error) {
	if !a.hasValue {
		batch := UT.GetBatch(1)
		batch.AppendRow(0, LX.T_INT_KW, nil, true)
		batch.AdvanceSize()
		batch.SetColumnName(0, "sum")
		return batch, nil
	}
	batch := UT.GetBatch(1)
	if a.isFloat {
		batch.AppendRow(0, LX.T_FLOAT_KW, a.floatSum, false)
	} else {
		batch.AppendRow(0, LX.T_INT_KW, a.intSum, false)
	}
	batch.AdvanceSize()
	batch.SetColumnName(0, "sum")
	return batch, nil
}

// Close releases resources.
func (a *VectorizedSum) Close() error {
	if a.child != nil {
		return a.child.Close()
	}
	return nil
}

// VectorizedAvg computes the average of a column. Implemented
// as a wrapper around VectorizedSum and VectorizedCount.
type VectorizedAvg struct {
	sum  *VectorizedSum
	cnt  *VectorizedCount
	done bool
}

// NewVectorizedAvg creates a vectorized AVG aggregate.
func NewVectorizedAvg(child UT.BatchProducer, colIdx int) *VectorizedAvg {
	return &VectorizedAvg{
		sum: NewVectorizedSum(child, colIdx),
		cnt: NewVectorizedCount(child),
	}
}

// NextBatch returns a single-row batch with the average.
func (a *VectorizedAvg) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if a.done {
		return nil, nil
	}
	// Run both sum and count on the same data
	// For simplicity, we run them sequentially on the same child
	for {
		batch, err := a.sum.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		// Accumulate both sum and count for this batch
		if batch.Sel == nil {
			a.cnt.total += int64(batch.Size)
		} else {
			a.cnt.total += int64(len(batch.Sel))
		}
		if a.sum.colIdx < len(batch.Cols) {
			a.sum.accumulateColumn(batch.Cols[a.sum.colIdx], batch)
		}
		batch.Put()
	}

	a.done = true

	// Compute average
	if a.cnt.total == 0 {
		batch := UT.GetBatch(1)
		batch.AppendRow(0, LX.T_INT_KW, nil, true)
		batch.AdvanceSize()
		batch.SetColumnName(0, "avg")
		return batch, nil
	}
	var avg float64
	if a.sum.isFloat {
		avg = a.sum.floatSum / float64(a.cnt.total)
	} else {
		avg = float64(a.sum.intSum) / float64(a.cnt.total)
	}

	batch := UT.GetBatch(1)
	batch.AppendRow(0, LX.T_FLOAT_KW, avg, false)
	batch.AdvanceSize()
	batch.SetColumnName(0, "avg")
	return batch, nil
}

// Close releases resources.
func (a *VectorizedAvg) Close() error {
	if err := a.sum.Close(); err != nil {
		return err
	}
	return a.cnt.Close()
}

// VectorizedMin finds the minimum value of a column. 4-wide
// unrolled min reduction.
type VectorizedMin struct {
	child    UT.BatchProducer
	colIdx   int
	intMin   int64
	floatMin float64
	isFloat  bool
	hasValue bool
	done     bool
}

// NewVectorizedMin creates a vectorized MIN aggregate.
func NewVectorizedMin(child UT.BatchProducer, colIdx int) *VectorizedMin {
	return &VectorizedMin{child: child, colIdx: colIdx}
}

// NextBatch returns a single-row batch with the min value.
func (a *VectorizedMin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if a.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for {
		batch, err := a.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		if a.colIdx < len(batch.Cols) {
			a.reduceColumn(batch.Cols[a.colIdx], batch)
		}
		batch.Put()
	}

	a.done = true
	return a.finalBatch()
}

// reduceColumn performs 4-wide unrolled min reduction.
func (a *VectorizedMin) reduceColumn(col UT.Column, batch *UT.Batch) {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		data := col.Data.Ints
		if data == nil {
			return
		}
		a.isFloat = false
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				if int(idx) < len(data) {
					if !a.hasValue || data[idx] < a.intMin {
						a.intMin = data[idx]
						a.hasValue = true
					}
				}
			}
			return
		}
		n := len(data)
		if batch.Size < n {
			n = batch.Size
		}
		i := 0
		for i+4 <= n {
			minVal := data[i]
			if data[i+1] < minVal {
				minVal = data[i+1]
			}
			if data[i+2] < minVal {
				minVal = data[i+2]
			}
			if data[i+3] < minVal {
				minVal = data[i+3]
			}
			if !a.hasValue || minVal < a.intMin {
				a.intMin = minVal
				a.hasValue = true
			}
			i += 4
		}
		for ; i < n; i++ {
			if !a.hasValue || data[i] < a.intMin {
				a.intMin = data[i]
				a.hasValue = true
			}
		}
	case LX.T_FLOAT_KW:
		data := col.Data.Floats
		if data == nil {
			return
		}
		a.isFloat = true
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				if int(idx) < len(data) {
					if !a.hasValue || data[idx] < a.floatMin {
						a.floatMin = data[idx]
						a.hasValue = true
					}
				}
			}
			return
		}
		n := len(data)
		if batch.Size < n {
			n = batch.Size
		}
		i := 0
		for i+4 <= n {
			minVal := data[i]
			if data[i+1] < minVal {
				minVal = data[i+1]
			}
			if data[i+2] < minVal {
				minVal = data[i+2]
			}
			if data[i+3] < minVal {
				minVal = data[i+3]
			}
			if !a.hasValue || minVal < a.floatMin {
				a.floatMin = minVal
				a.hasValue = true
			}
			i += 4
		}
		for ; i < n; i++ {
			if !a.hasValue || data[i] < a.floatMin {
				a.floatMin = data[i]
				a.hasValue = true
			}
		}
	}
}

func (a *VectorizedMin) finalBatch() (*UT.Batch, error) {
	if !a.hasValue {
		batch := UT.GetBatch(1)
		batch.AppendRow(0, LX.T_INT_KW, nil, true)
		batch.AdvanceSize()
		batch.SetColumnName(0, "min")
		return batch, nil
	}
	batch := UT.GetBatch(1)
	if a.isFloat {
		batch.AppendRow(0, LX.T_FLOAT_KW, a.floatMin, false)
	} else {
		batch.AppendRow(0, LX.T_INT_KW, a.intMin, false)
	}
	batch.AdvanceSize()
	batch.SetColumnName(0, "min")
	return batch, nil
}

// Close releases resources.
func (a *VectorizedMin) Close() error {
	if a.child != nil {
		return a.child.Close()
	}
	return nil
}

// VectorizedMax is the same as Min but for maximum values.
type VectorizedMax struct {
	child    UT.BatchProducer
	colIdx   int
	intMax   int64
	floatMax float64
	isFloat  bool
	hasValue bool
	done     bool
}

// NewVectorizedMax creates a vectorized MAX aggregate.
func NewVectorizedMax(child UT.BatchProducer, colIdx int) *VectorizedMax {
	return &VectorizedMax{child: child, colIdx: colIdx}
}

// NextBatch returns a single-row batch with the max value.
func (a *VectorizedMax) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if a.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for {
		batch, err := a.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		if a.colIdx < len(batch.Cols) {
			a.reduceColumn(batch.Cols[a.colIdx], batch)
		}
		batch.Put()
	}

	a.done = true
	return a.finalBatch()
}

// reduceColumn performs 4-wide unrolled max reduction.
func (a *VectorizedMax) reduceColumn(col UT.Column, batch *UT.Batch) {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		data := col.Data.Ints
		if data == nil {
			return
		}
		a.isFloat = false
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				if int(idx) < len(data) {
					if !a.hasValue || data[idx] > a.intMax {
						a.intMax = data[idx]
						a.hasValue = true
					}
				}
			}
			return
		}
		n := len(data)
		if batch.Size < n {
			n = batch.Size
		}
		i := 0
		for i+4 <= n {
			maxVal := data[i]
			if data[i+1] > maxVal {
				maxVal = data[i+1]
			}
			if data[i+2] > maxVal {
				maxVal = data[i+2]
			}
			if data[i+3] > maxVal {
				maxVal = data[i+3]
			}
			if !a.hasValue || maxVal > a.intMax {
				a.intMax = maxVal
				a.hasValue = true
			}
			i += 4
		}
		for ; i < n; i++ {
			if !a.hasValue || data[i] > a.intMax {
				a.intMax = data[i]
				a.hasValue = true
			}
		}
	case LX.T_FLOAT_KW:
		data := col.Data.Floats
		if data == nil {
			return
		}
		a.isFloat = true
		if batch.Sel != nil {
			for _, idx := range batch.Sel {
				if int(idx) < len(data) {
					if !a.hasValue || data[idx] > a.floatMax {
						a.floatMax = data[idx]
						a.hasValue = true
					}
				}
			}
			return
		}
		n := len(data)
		if batch.Size < n {
			n = batch.Size
		}
		i := 0
		for i+4 <= n {
			maxVal := data[i]
			if data[i+1] > maxVal {
				maxVal = data[i+1]
			}
			if data[i+2] > maxVal {
				maxVal = data[i+2]
			}
			if data[i+3] > maxVal {
				maxVal = data[i+3]
			}
			if !a.hasValue || maxVal > a.floatMax {
				a.floatMax = maxVal
				a.hasValue = true
			}
			i += 4
		}
		for ; i < n; i++ {
			if !a.hasValue || data[i] > a.floatMax {
				a.floatMax = data[i]
				a.hasValue = true
			}
		}
	}
}

func (a *VectorizedMax) finalBatch() (*UT.Batch, error) {
	if !a.hasValue {
		batch := UT.GetBatch(1)
		batch.AppendRow(0, LX.T_INT_KW, nil, true)
		batch.AdvanceSize()
		batch.SetColumnName(0, "max")
		return batch, nil
	}
	batch := UT.GetBatch(1)
	if a.isFloat {
		batch.AppendRow(0, LX.T_FLOAT_KW, a.floatMax, false)
	} else {
		batch.AppendRow(0, LX.T_INT_KW, a.intMax, false)
	}
	batch.AdvanceSize()
	batch.SetColumnName(0, "max")
	return batch, nil
}

// Close releases resources.
func (a *VectorizedMax) Close() error {
	if a.child != nil {
		return a.child.Close()
	}
	return nil
}

// AggKind identifies the type of aggregate operation.
type AggKind int

const (
	AggSum         AggKind = 0
	AggCount       AggKind = 1
	AggMin         AggKind = 2
	AggMax         AggKind = 3
	AggAvg         AggKind = 4
	AggGroupConcat AggKind = 5
	AggStringAgg   AggKind = 6
)

// AggDef describes one aggregate column. Kind is the operation,
// Col is the source column index in the input batch (use -1 for COUNT(*)).
// Separator is the separator string for GROUP_CONCAT/STRING_AGG.
// Distinct controls deduplication for GROUP_CONCAT.
type AggDef struct {
	Kind      AggKind
	Col       int
	Separator string
	Distinct  bool
}

// aggPayload holds the accumulator state for one hash table slot.
type aggPayload struct {
	Count       int64
	Sum         int64
	Min         int64
	Max         int64
	HasValue    bool
	StrParts    []string           // REQ001993: GROUP_CONCAT/STRING_AGG accumulator
	StrSep      string             // REQ001993: separator for string concat
	StrSeen     map[any]bool       // REQ001993: DISTINCT dedup for GROUP_CONCAT
	StrDistinct bool               // REQ001993: whether DISTINCT is enabled
	// REQ001730: per-DISTINCT-agg dedup sets for numeric aggregates
	// (SUM/COUNT/MIN/MAX/AVG). Lazily allocated when an aggregate with
	// Distinct=true processes its first row.
	DistinctSeen []map[int64]struct{}
}

// VectorizedHashAggregate is a vectorized hash aggregate that
// supports GROUP BY on multiple int64 key columns. It drains
// all child batches, builds a hash table, and returns one result
// batch with group keys (if grouped) followed by aggregate columns.
type VectorizedHashAggregate struct {
	child          UT.BatchProducer
	groupCols      []int // column indices for GROUP BY (nil = no GROUP BY)
	aggDefs        []AggDef
	ht             *UT.HashTable
	noGroupPayload *aggPayload // for no-GROUP-BY case (stride==0)
	done           bool

	// REQ001662: pooled scratch buffers reused across batches to
	// avoid the four per-batch allocs in processBatch (keys,
	// hashes, validRows, plus the redundant keysToPass/hashesToPass
	// copies). Capacity grows monotonically; cleared between
	// batches by re-slicing to 0.
	scratchKeys    []int64
	scratchHashes  []uint64
	scratchValid   []int
}

// hashInt64 computes a uint64 hash of an int64 key using
// FNV-1a mixing (same as hashjoin.go).
func hashInt64(x int64) uint64 {
	u := uint64(x)
	return u*0x9e3779b97f4a7c15 ^ (u >> 31)
}

// NewVectorizedHashAggregate creates a new vectorized hash aggregate.
// groupCols are column indices for GROUP BY (nil for no grouping).
// defs specifies the aggregate operations.
func NewVectorizedHashAggregate(child UT.BatchProducer, groupCols []int, defs []AggDef) *VectorizedHashAggregate {
	return &VectorizedHashAggregate{
		child:     child,
		groupCols: groupCols,
		aggDefs:   defs,
	}
}

// NextBatch drains all child batches and returns a single result
// batch. Subsequent calls return nil (EOF).
func (a *VectorizedHashAggregate) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if a.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	stride := len(a.groupCols)
	if stride == 0 {
		a.ht = UT.NewHashTable(64)
	} else {
		a.ht = UT.NewHashTableWithCols(64, stride)
	}
	a.ht.Payloads = make([]any, 0, 64)

	// Drain child batches
	// REQ001649: ctx cancellation check every 1024 batches.
	var ctxCheck int
	for {
		batch, err := a.child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			break
		}
		ctxCheck++
		if ctxCheck >= 1024 {
			ctxCheck = 0
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		a.processBatch(batch)
		batch.Put()
	}

	a.done = true
	return a.buildResultBatch()
}

// processBatch accumulates one batch into the hash table.
func (a *VectorizedHashAggregate) processBatch(batch *UT.Batch) {
	n := batch.LogicalSize()
	if n == 0 {
		return
	}

	stride := len(a.groupCols)
	if stride == 0 {
		// No GROUP BY: all rows accumulate at slot 0
		for row := 0; row < n; row++ {
			src := row
			if batch.Sel != nil {
				src = int(batch.Sel[row])
			}
			a.updateAggregates(batch, src, 0)
		}
		return
	}

	// Multi-column GROUP BY: build flat-packed keys
	// REQ001662: reuse scratch buffers across batches instead of
	// reallocating four slices per call. The two extra slices
	// (keysToPass, hashesToPass) are eliminated entirely — we
	// fill keys/hashes directly in validRow-compact order.
	keys, hashes, validRows := a.acquireScratch(n, stride)

	// Pass 1: collect valid (non-NULL) src indices.
	for row := 0; row < n; row++ {
		src := row
		if batch.Sel != nil {
			src = int(batch.Sel[row])
		}
		// Check for NULL in any group column
		hasNull := false
		for _, colIdx := range a.groupCols {
			if colIdx >= len(batch.Cols) {
				hasNull = true
				break
			}
			col := batch.Cols[colIdx]
			if col.Nulls != nil && src < len(col.Nulls) && col.Nulls[src] {
				hasNull = true
				break
			}
			if src >= len(col.Data.Ints) {
				hasNull = true
				break
			}
		}
		if hasNull {
			continue
		}
		validRows = append(validRows, src)
	}

	if len(validRows) == 0 {
		return
	}

	// Pass 2: pack keys directly into validRow-compact order so
	// hashes/keys are aligned with validRows. This replaces the
	// previous keysToPass copy.
	for i, src := range validRows {
		k := i * stride
		for c, colIdx := range a.groupCols {
			col := batch.Cols[colIdx]
			keys[k+c] = col.Data.Ints[src]
		}
		hashes[i] = UT.HashComposite(keys[k : k+stride])
	}

	// Probe hash table — pass the compact slice range directly.
	a.ht.Probe(keys, hashes, len(validRows), func(idx int, row int) {
		a.updateAggregates(batch, validRows[row], idx)
	})
}

// acquireScratch returns reusable scratch buffers sized for the
// current batch. REQ001662. Buffers grow monotonically via
// slices.Grow and are reused across batches in the same
// NextBatch call.
func (a *VectorizedHashAggregate) acquireScratch(n, stride int) (keys []int64, hashes []uint64, validRows []int) {
	keysLen := n * stride
	if cap(a.scratchKeys) < keysLen {
		a.scratchKeys = slices.Grow(a.scratchKeys, keysLen)[:keysLen]
	} else {
		a.scratchKeys = a.scratchKeys[:keysLen]
	}
	keys = a.scratchKeys
	if cap(a.scratchHashes) < n {
		a.scratchHashes = slices.Grow(a.scratchHashes, n)[:n]
	} else {
		a.scratchHashes = a.scratchHashes[:n]
	}
	hashes = a.scratchHashes
	if cap(a.scratchValid) < n {
		a.scratchValid = slices.Grow(a.scratchValid, n)[:0]
	} else {
		a.scratchValid = a.scratchValid[:0]
	}
	validRows = a.scratchValid
	return
}

// updateAggregates updates aggregate accumulators for one row at the
// given hash table slot.
func (a *VectorizedHashAggregate) updateAggregates(batch *UT.Batch, src, slot int) {
	// No-GROUP-BY path: accumulate in noGroupPayload
	if len(a.groupCols) == 0 {
		if a.noGroupPayload == nil {
			a.noGroupPayload = &aggPayload{}
		}
		p := a.noGroupPayload
		for di, def := range a.aggDefs {
			_ = di
			// REQ001730: COUNT(*) increments unconditionally.
			// COUNT(col) and COUNT(DISTINCT col) need per-value logic.
			if def.Kind == AggCount && def.Col == -1 {
				p.Count++
				continue
			}
			if def.Col < 0 || def.Col >= len(batch.Cols) {
				continue
			}
			col := batch.Cols[def.Col]
			// REQ001993: handle string aggregates
			if def.Kind == AggGroupConcat || def.Kind == AggStringAgg {
				if col.Data.Strs == nil || src >= len(col.Data.Strs) {
					continue
				}
				if col.Nulls != nil && src < len(col.Nulls) && col.Nulls[src] {
					continue
				}
				val := col.Data.Strs[src]
				if def.Distinct {
					if p.StrSeen == nil {
						p.StrSeen = make(map[any]bool)
					}
					if p.StrSeen[val] {
						continue
					}
					p.StrSeen[val] = true
				}
				p.StrSep = def.Separator
				p.StrDistinct = def.Distinct
				p.StrParts = append(p.StrParts, val)
				continue
			}
			if col.Data.Ints == nil || src >= len(col.Data.Ints) {
				continue
			}
			if col.Nulls != nil && src < len(col.Nulls) && col.Nulls[src] {
				continue
			}
			val := col.Data.Ints[src]
			// REQ001730: DISTINCT dedup for numeric aggregates.
			if def.Distinct {
				if di >= len(p.DistinctSeen) {
					p.DistinctSeen = append(p.DistinctSeen, nil)
				}
				if p.DistinctSeen[di] == nil {
					p.DistinctSeen[di] = make(map[int64]struct{})
				}
				if _, ok := p.DistinctSeen[di][val]; ok {
					continue
				}
				p.DistinctSeen[di][val] = struct{}{}
			}
			switch def.Kind {
			case AggCount:
				// REQ001730: COUNT(col) counts non-null rows;
				// nulls already skipped above.
				p.Count++
			case AggSum:
				p.Sum += val
			case AggMin:
				if !p.HasValue || val < p.Min {
					p.Min = val
					p.HasValue = true
				}
			case AggMax:
				if !p.HasValue || val > p.Max {
					p.Max = val
					p.HasValue = true
				}
			case AggAvg:
				p.Sum += val
				p.Count++
			}
		}
		return
	}

	// GROUP BY path: use PayloadIdx
	if slot < 0 || slot >= len(a.ht.PayloadIdx) {
		return
	}
	pIdx := a.ht.PayloadIdx[slot]
	if pIdx < 0 || pIdx >= len(a.ht.Payloads) {
		return
	}
	if a.ht.Payloads[pIdx] == nil {
		a.ht.Payloads[pIdx] = &aggPayload{}
	}
	p := a.ht.Payloads[pIdx].(*aggPayload)
	p.Count++

	for di, def := range a.aggDefs {
		_ = di
		if def.Kind == AggCount && def.Col == -1 {
			continue
		}
		if def.Col < 0 || def.Col >= len(batch.Cols) {
			continue
		}
		col := batch.Cols[def.Col]
		// REQ001993: handle string aggregates
		if def.Kind == AggGroupConcat || def.Kind == AggStringAgg {
			if col.Data.Strs == nil || src >= len(col.Data.Strs) {
				continue
			}
			if col.Nulls != nil && src < len(col.Nulls) && col.Nulls[src] {
				continue
			}
			val := col.Data.Strs[src]
			if def.Distinct {
				if p.StrSeen == nil {
					p.StrSeen = make(map[any]bool)
				}
				if p.StrSeen[val] {
					continue
				}
				p.StrSeen[val] = true
			}
			p.StrSep = def.Separator
			p.StrDistinct = def.Distinct
			p.StrParts = append(p.StrParts, val)
			continue
		}
		if col.Data.Ints == nil || src >= len(col.Data.Ints) {
			continue
		}
		if col.Nulls != nil && src < len(col.Nulls) && col.Nulls[src] {
			continue
		}
		val := col.Data.Ints[src]
		// REQ001730: DISTINCT dedup for numeric aggregates.
		if def.Distinct {
			if di >= len(p.DistinctSeen) {
				p.DistinctSeen = append(p.DistinctSeen, nil)
			}
			if p.DistinctSeen[di] == nil {
				p.DistinctSeen[di] = make(map[int64]struct{})
			}
			if _, ok := p.DistinctSeen[di][val]; ok {
				continue
			}
			p.DistinctSeen[di][val] = struct{}{}
		}
		switch def.Kind {
		case AggCount:
			p.Count++
		case AggSum:
			p.Sum += val
		case AggMin:
			if !p.HasValue || val < p.Min {
				p.Min = val
				p.HasValue = true
			}
		case AggMax:
			if !p.HasValue || val > p.Max {
				p.Max = val
				p.HasValue = true
			}
		case AggAvg:
			p.Sum += val
		}
	}
}

// buildResultBatch constructs the output batch from the hash table.
func (a *VectorizedHashAggregate) buildResultBatch() (*UT.Batch, error) {
	stride := len(a.groupCols)
	ncols := stride + len(a.aggDefs)
	if ncols == 0 {
		ncols = 1
	}

	batch := UT.GetBatch(ncols)
	colIdx := 0

	for c := 0; c < stride; c++ {
		batch.SetColumnName(colIdx, fmt.Sprintf("group_%d", c))
		batch.Cols[colIdx].Type = LX.T_INT_KW
		batch.Cols[colIdx].Data.Ints = make([]int64, 0)
		colIdx++
	}

	for _, def := range a.aggDefs {
		switch def.Kind {
		case AggCount:
			batch.SetColumnName(colIdx, "count")
		case AggSum:
			batch.SetColumnName(colIdx, "sum")
		case AggMin:
			batch.SetColumnName(colIdx, "min")
		case AggMax:
			batch.SetColumnName(colIdx, "max")
		case AggAvg:
			batch.SetColumnName(colIdx, "avg")
		case AggGroupConcat:
			batch.SetColumnName(colIdx, "group_concat")
		case AggStringAgg:
			batch.SetColumnName(colIdx, "string_agg")
		}

		if def.Kind == AggGroupConcat || def.Kind == AggStringAgg {
			batch.Cols[colIdx].Type = LX.T_TEXT
			batch.Cols[colIdx].Data.Strs = make([]string, 0)
		} else {
			batch.Cols[colIdx].Type = LX.T_INT_KW
			batch.Cols[colIdx].Data.Ints = make([]int64, 0)
		}
		colIdx++
	}

	if stride == 0 {
		if a.noGroupPayload == nil {
			a.noGroupPayload = &aggPayload{}
		}
		p := a.noGroupPayload
		colIdx = 0
		for _, def := range a.aggDefs {
			if def.Kind == AggGroupConcat || def.Kind == AggStringAgg {
				var s string
				if len(p.StrParts) > 0 {
					var b strings.Builder
					total := len(p.StrParts[0])
					for _, part := range p.StrParts[1:] {
						total += len(p.StrSep) + len(part)
					}
					b.Grow(total)
					b.WriteString(p.StrParts[0])
					for _, part := range p.StrParts[1:] {
						b.WriteString(p.StrSep)
						b.WriteString(part)
					}
					s = b.String()
				}
				batch.Cols[colIdx].Data.Strs = append(batch.Cols[colIdx].Data.Strs, s)
				colIdx++
				continue
			}
			var val int64
			switch def.Kind {
			case AggCount:
				val = p.Count
			case AggSum:
				val = p.Sum
			case AggMin:
				if p.HasValue {
					val = p.Min
				}
			case AggMax:
				if p.HasValue {
					val = p.Max
				}
			case AggAvg:
				if p.Count > 0 {
					val = p.Sum / p.Count
				}
			}
			batch.Cols[colIdx].Data.Ints = append(batch.Cols[colIdx].Data.Ints, val)
			colIdx++
		}
		batch.AdvanceSize()
		return batch, nil
	}

	hashStride := a.ht.NumCols
	for i := uint32(0); i < a.ht.Capacity; i++ {
		if (a.ht.Bitmap[i/64]>>(i%64))&1 == 0 {
			continue
		}
		colIdx = 0
		pIdx := a.ht.PayloadIdx[i]
		var p aggPayload
		if pIdx >= 0 && pIdx < len(a.ht.Payloads) {
			if pp, ok := a.ht.Payloads[pIdx].(*aggPayload); ok && pp != nil {
				p = *pp
			}
		}

		base := int(i) * hashStride
		for c := 0; c < stride; c++ {
			batch.Cols[colIdx].Data.Ints = append(batch.Cols[colIdx].Data.Ints, a.ht.Keys[base+c])
			colIdx++
		}
		for _, def := range a.aggDefs {
			if def.Kind == AggGroupConcat || def.Kind == AggStringAgg {
				var s string
				if len(p.StrParts) > 0 {
					var b strings.Builder
					total := len(p.StrParts[0])
					for _, part := range p.StrParts[1:] {
						total += len(p.StrSep) + len(part)
					}
					b.Grow(total)
					b.WriteString(p.StrParts[0])
					for _, part := range p.StrParts[1:] {
						b.WriteString(p.StrSep)
						b.WriteString(part)
					}
					s = b.String()
				}
				batch.Cols[colIdx].Data.Strs = append(batch.Cols[colIdx].Data.Strs, s)
				colIdx++
				continue
			}
			var val int64
			switch def.Kind {
			case AggCount:
				val = p.Count
			case AggSum:
				val = p.Sum
			case AggMin:
				if p.HasValue {
					val = p.Min
				}
			case AggMax:
				if p.HasValue {
					val = p.Max
				}
			case AggAvg:
				if p.Count > 0 {
					val = p.Sum / p.Count
				}
			}
			batch.Cols[colIdx].Data.Ints = append(batch.Cols[colIdx].Data.Ints, val)
			colIdx++
		}

		batch.AdvanceSize()
	}

	if batch.Size == 0 {
		colIdx = 0
		for c := 0; c < stride; c++ {
			batch.Cols[colIdx].Data.Ints = append(batch.Cols[colIdx].Data.Ints, 0)
			if batch.Cols[colIdx].Nulls == nil {

				batch.Cols[colIdx].Nulls = make([]bool, 1)
			} else if len(batch.Cols[colIdx].Nulls) < 1 {
				batch.Cols[colIdx].Nulls = append(batch.Cols[colIdx].Nulls, false)
			}

			batch.Cols[colIdx].Nulls[0] = true

			colIdx++
		}
		for range a.aggDefs {
			di := colIdx - stride
			if di >= 0 && di < len(a.aggDefs) && (a.aggDefs[di].Kind == AggGroupConcat || a.aggDefs[di].Kind == AggStringAgg) {
				batch.Cols[colIdx].Data.Strs = append(batch.Cols[colIdx].Data.Strs, "")
				if batch.Cols[colIdx].Nulls == nil {
					batch.Cols[colIdx].Nulls = make([]bool, 1)
				} else if len(batch.Cols[colIdx].Nulls) < 1 {
					batch.Cols[colIdx].Nulls = append(batch.Cols[colIdx].Nulls, false)
				}
				batch.Cols[colIdx].Nulls[0] = true
				colIdx++
				continue
			}
			batch.Cols[colIdx].Data.Ints = append(batch.Cols[colIdx].Data.Ints, 0)
			if batch.Cols[colIdx].Nulls == nil {
				batch.Cols[colIdx].Nulls = make([]bool, 1)
			} else if len(batch.Cols[colIdx].Nulls) < 1 {
				batch.Cols[colIdx].Nulls = append(batch.Cols[colIdx].Nulls, false)
			}
			batch.Cols[colIdx].Nulls[0] = true
			colIdx++
		}

		batch.AdvanceSize()
	}

	return batch, nil
}

// Close releases resources.
func (a *VectorizedHashAggregate) Close() error {
	if a.child != nil {
		return a.child.Close()
	}
	return nil
}
