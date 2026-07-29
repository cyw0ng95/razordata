package MR

import (
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// Reducer accumulates values per group and produces final results.
// It is called by the Shuffler: Accumulate for each row during the
// map phase, Finalize once after all rows have been processed.
type Reducer interface {
	// Accumulate adds one row's values to the accumulator for group slot.
	// slot is the group index (0 for scalar/no-groupby).
	Accumulate(slot int, batch *UT.Batch, rowIdx int) error

	// Finalize produces output batches from all accumulated groups.
	// groupKeys provides the key values for each group (nil for scalar).
	Finalize(groupKeys GroupKeySource) ([]*UT.Batch, error)

	// Reset clears all accumulator state but preserves buffer capacity.
	Reset()
}

// GroupKeySource provides group key values during Finalize.
type GroupKeySource interface {
	// GroupCount returns the number of groups.
	GroupCount() int
	// KeyValue returns the key value for group at index idx and column colIdx.
	KeyValue(idx int, colIdx int) (int64, bool)
	// KeyColumns returns the number of key columns.
	KeyColumns() int
}

// AggregateReducer accumulates aggregate function state per group.
// Each group slot has one UnifiedAccum per aggregate specification.
type AggregateReducer struct {
	specs     []AccumulatorSpec
	accums    []UnifiedAccum // one per group slot, indexed by slot
	groupCols []int          // GROUP BY column indices (nil for scalar)
}

// NewAggregateReducer creates a new aggregate reducer.
func NewAggregateReducer(specs []AccumulatorSpec, groupCols []int) *AggregateReducer {
	return &AggregateReducer{
		specs:     specs,
		accums:    make([]UnifiedAccum, 0, 64),
		groupCols: groupCols,
	}
}

// Accumulate adds one row's values to the accumulators for group slot.
func (r *AggregateReducer) Accumulate(slot int, batch *UT.Batch, rowIdx int) error {
	// Ensure we have enough accumulators for this slot
	for len(r.accums) <= slot {
		r.accums = append(r.accums, UnifiedAccum{})
	}
	for i := range r.specs {
		r.accums[slot].Update(&r.specs[i], batch, rowIdx)
	}
	return nil
}

// debugAccumCount returns the number of accumulator slots.
func (r *AggregateReducer) debugAccumCount() int { return len(r.accums) }
// debugAccumHasValue returns true if the first accumulator has a value.
func (r *AggregateReducer) debugAccumHasValue() bool {
	if len(r.accums) == 0 {
		return false
	}
	return r.accums[0].debugHasValue()
}

// Finalize produces output batches from all accumulated groups.
// groupKeys provides key column access; nil for scalar (no GROUP BY).
func (r *AggregateReducer) Finalize(groupKeys GroupKeySource) ([]*UT.Batch, error) {
	var numGroups int
	var numKeyCols int
	if groupKeys != nil {
		numGroups = groupKeys.GroupCount()
		numKeyCols = groupKeys.KeyColumns()
	} else {
		numGroups = 1 // scalar: one group (row 0)
	}

	numAggCols := len(r.specs)
	totalCols := numKeyCols + numAggCols

	batch := UT.GetBatch(totalCols)

	// Initialize column types
	for c := 0; c < numKeyCols; c++ {
		batch.Cols[c].Type = LX.T_INT_KW
		batch.Cols[c].Name = "group" + intStr(c)
	}
	for i, spec := range r.specs {
		colIdx := numKeyCols + i
		// Determine result type
		inputType := LX.T_INT_KW // default
		if spec.Col >= 0 && len(r.groupCols) == 0 {
			// Scalar case: we don't know the input type easily.
			// Use INT as default; MIN/MAX will inherit from first value.
		}
		batch.Cols[colIdx].Type = spec.ResultType(inputType)
		batch.Cols[colIdx].Name = "agg" + intStr(i)
	}

	// Fill rows
	for g := 0; g < numGroups; g++ {
		var accum *UnifiedAccum
		if groupKeys != nil {
			if g >= len(r.accums) {
				continue
			}
			accum = &r.accums[g]
		} else {
			if len(r.accums) == 0 {
				// Empty scalar: still emit one row for COUNT(*)
				accum = &UnifiedAccum{}
			} else {
				accum = &r.accums[0]
			}
		}

		// Group key columns
		for c := 0; c < numKeyCols; c++ {
			val, ok := groupKeys.KeyValue(g, c)
			if !ok {
				batch.Cols[c].Nulls = append(batch.Cols[c].Nulls, true)
			} else {
				batch.Cols[c].Data.Ints = append(batch.Cols[c].Data.Ints, val)
				batch.Cols[c].Nulls = append(batch.Cols[c].Nulls, false)
			}
		}

		// Aggregate columns
		for i, spec := range r.specs {
			colIdx := numKeyCols + i
			val, ok := accum.Result(&spec)
			if !ok {
				// NULL result
				batch.Cols[colIdx].Nulls = append(batch.Cols[colIdx].Nulls, true)
				continue
			}
			batch.Cols[colIdx].Nulls = append(batch.Cols[colIdx].Nulls, false)
			switch v := val.(type) {
			case int64:
				batch.Cols[colIdx].Data.Ints = append(batch.Cols[colIdx].Data.Ints, v)
				batch.Cols[colIdx].Type = LX.T_INT_KW
			case float64:
				batch.Cols[colIdx].Data.Floats = append(batch.Cols[colIdx].Data.Floats, v)
				batch.Cols[colIdx].Type = LX.T_FLOAT_KW
			case string:
				batch.Cols[colIdx].Data.Strs = append(batch.Cols[colIdx].Data.Strs, v)
				batch.Cols[colIdx].Type = LX.T_TEXT
			case bool:
				batch.Cols[colIdx].Data.Bools = append(batch.Cols[colIdx].Data.Bools, v)
				batch.Cols[colIdx].Type = LX.T_BOOL
			default:
				batch.Cols[colIdx].Nulls[len(batch.Cols[colIdx].Nulls)-1] = true
			}
		}

		batch.AdvanceSize()
	}

	return []*UT.Batch{batch}, nil
}

// Reset clears all accumulator state but preserves buffer capacity.
func (r *AggregateReducer) Reset() {
	for i := range r.accums {
		r.accums[i].Reset()
	}
	r.accums = r.accums[:0]
}

// intStr is a minimal int-to-string converter for column name generation.
func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
