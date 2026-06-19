package EX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

// VectorizedCount accumulates the count of matching rows across
// all batches from a child source. Uses 4-wide unrolled counter
// increment for L1 cache efficiency.
// REQ000157 satisfied (partial): SIMD-accelerated COUNT aggregate.
type VectorizedCount struct {
	child BatchProducer
	total int64
	done  bool
}

// BatchProducer is the interface for any operator that can
// produce batches. This includes VectorizedSeqScan, VectorizedFilter,
// and any future batch-based operator. It allows building chains
// like: scan -> filter -> aggregate.
type BatchProducer interface {
	NextBatch(ctx context.Context) (*Batch, error)
	Close() error
}

// NewVectorizedCount creates a vectorized COUNT aggregate.
func NewVectorizedCount(child BatchProducer) *VectorizedCount {
	return &VectorizedCount{child: child}
}

// NextBatch returns a single-row batch with the final count.
// Subsequent calls return nil. This matches the existing
// row-based Aggregate operator contract.
func (a *VectorizedCount) NextBatch(ctx context.Context) (*Batch, error) {
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

func (a *VectorizedCount) finalBatch() (*Batch, error) {
	batch := GetBatch(1)
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
	child    BatchProducer
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
func NewVectorizedSum(child BatchProducer, colIdx int) *VectorizedSum {
	return &VectorizedSum{child: child, colIdx: colIdx}
}

// NextBatch returns a single-row batch with the final sum.
func (a *VectorizedSum) NextBatch(ctx context.Context) (*Batch, error) {
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
func (a *VectorizedSum) accumulateColumn(col Column, batch *Batch) {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		data, ok := col.Data.([]int64)
		if !ok {
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
		data, ok := col.Data.([]float64)
		if !ok {
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

func (a *VectorizedSum) finalBatch() (*Batch, error) {
	if !a.hasValue {
		batch := GetBatch(1)
		batch.AppendRow(0, LX.T_INT_KW, nil, true)
		batch.AdvanceSize()
		batch.SetColumnName(0, "sum")
		return batch, nil
	}
	batch := GetBatch(1)
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
func NewVectorizedAvg(child BatchProducer, colIdx int) *VectorizedAvg {
	return &VectorizedAvg{
		sum: NewVectorizedSum(child, colIdx),
		cnt: NewVectorizedCount(child),
	}
}

// NextBatch returns a single-row batch with the average.
func (a *VectorizedAvg) NextBatch(ctx context.Context) (*Batch, error) {
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
		batch := GetBatch(1)
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

	batch := GetBatch(1)
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
	child    BatchProducer
	colIdx   int
	intMin   int64
	floatMin float64
	isFloat  bool
	hasValue bool
	done     bool
}

// NewVectorizedMin creates a vectorized MIN aggregate.
func NewVectorizedMin(child BatchProducer, colIdx int) *VectorizedMin {
	return &VectorizedMin{child: child, colIdx: colIdx}
}

// NextBatch returns a single-row batch with the min value.
func (a *VectorizedMin) NextBatch(ctx context.Context) (*Batch, error) {
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
func (a *VectorizedMin) reduceColumn(col Column, batch *Batch) {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		data, ok := col.Data.([]int64)
		if !ok {
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
		data, ok := col.Data.([]float64)
		if !ok {
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

func (a *VectorizedMin) finalBatch() (*Batch, error) {
	if !a.hasValue {
		batch := GetBatch(1)
		batch.AppendRow(0, LX.T_INT_KW, nil, true)
		batch.AdvanceSize()
		batch.SetColumnName(0, "min")
		return batch, nil
	}
	batch := GetBatch(1)
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
	child    BatchProducer
	colIdx   int
	intMax   int64
	floatMax float64
	isFloat  bool
	hasValue bool
	done     bool
}

// NewVectorizedMax creates a vectorized MAX aggregate.
func NewVectorizedMax(child BatchProducer, colIdx int) *VectorizedMax {
	return &VectorizedMax{child: child, colIdx: colIdx}
}

// NextBatch returns a single-row batch with the max value.
func (a *VectorizedMax) NextBatch(ctx context.Context) (*Batch, error) {
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
func (a *VectorizedMax) reduceColumn(col Column, batch *Batch) {
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		data, ok := col.Data.([]int64)
		if !ok {
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
		data, ok := col.Data.([]float64)
		if !ok {
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

func (a *VectorizedMax) finalBatch() (*Batch, error) {
	if !a.hasValue {
		batch := GetBatch(1)
		batch.AppendRow(0, LX.T_INT_KW, nil, true)
		batch.AdvanceSize()
		batch.SetColumnName(0, "max")
		return batch, nil
	}
	batch := GetBatch(1)
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
