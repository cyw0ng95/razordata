package PX

import (
	"github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

type Reducer interface {
	Accumulate(slot int, batch *UT.Batch, rowIdx int) error
	Finalize(groupKeys GroupKeySource) ([]*UT.Batch, error)
	Reset()
}

type GroupKeySource interface {
	GroupCount() int
	KeyValue(idx int, colIdx int) (int64, bool)
	KeyColumns() int
}

type AggregateReducer struct {
	specs     []AccumulatorSpec
	accums    []UnifiedAccum
	groupCols []int
}

func NewAggregateReducer(specs []AccumulatorSpec, groupCols []int) *AggregateReducer {
	return &AggregateReducer{
		specs:     specs,
		accums:    make([]UnifiedAccum, 0, 64),
		groupCols: groupCols,
	}
}

func (r *AggregateReducer) Accumulate(slot int, batch *UT.Batch, rowIdx int) error {
	for len(r.accums) <= slot {
		r.accums = append(r.accums, UnifiedAccum{})
	}
	for i := range r.specs {
		r.accums[slot].Update(&r.specs[i], batch, rowIdx)
	}
	return nil
}

func (r *AggregateReducer) debugAccumCount() int { return len(r.accums) }
func (r *AggregateReducer) debugAccumHasValue() bool {
	if len(r.accums) == 0 {
		return false
	}
	return r.accums[0].debugHasValue()
}

func (r *AggregateReducer) Finalize(groupKeys GroupKeySource) ([]*UT.Batch, error) {
	var numGroups int
	var numKeyCols int
	if groupKeys != nil {
		numGroups = groupKeys.GroupCount()
		numKeyCols = groupKeys.KeyColumns()
	} else {
		numGroups = 1
	}

	numAggCols := len(r.specs)
	totalCols := numKeyCols + numAggCols

	batch := UT.GetBatch(totalCols)

	for c := 0; c < numKeyCols; c++ {
		batch.Cols[c].Type = LX.T_INT_KW
		batch.Cols[c].Name = "group" + intStr(c)
	}
	for i, spec := range r.specs {
		colIdx := numKeyCols + i
		inputType := LX.T_INT_KW
		_ = inputType
		batch.Cols[colIdx].Type = spec.ResultType(inputType)
		batch.Cols[colIdx].Name = "agg" + intStr(i)
	}

	for g := 0; g < numGroups; g++ {
		var accum *UnifiedAccum
		if groupKeys != nil {
			if g >= len(r.accums) {
				continue
			}
			accum = &r.accums[g]
		} else {
			if len(r.accums) == 0 {
				accum = &UnifiedAccum{}
			} else {
				accum = &r.accums[0]
			}
		}

		for c := 0; c < numKeyCols; c++ {
			val, ok := groupKeys.KeyValue(g, c)
			if !ok {
				batch.Cols[c].Nulls = append(batch.Cols[c].Nulls, true)
			} else {
				batch.Cols[c].Data.Ints = append(batch.Cols[c].Data.Ints, val)
				batch.Cols[c].Nulls = append(batch.Cols[c].Nulls, false)
			}
		}

		for i, spec := range r.specs {
			colIdx := numKeyCols + i
			val, ok := accum.Result(&spec)
			if !ok {
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

func (r *AggregateReducer) Reset() {
	for i := range r.accums {
		r.accums[i].Reset()
	}
	r.accums = r.accums[:0]
}

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