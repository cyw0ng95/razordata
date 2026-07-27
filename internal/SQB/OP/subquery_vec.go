package OP

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// VectorizedInSubquery pre-materializes a subquery's key column into
// a hash set and provides batch-level IN membership testing.
// REQ001445. REQ002042: supports multi-column composite keys via
// string encoding (fmt.Sprintf("%v", col) joined with "\x00").
type VectorizedInSubquery struct {
	keySet   map[string]bool
	keyTypes []LX.TokenType
	numCols  int
	mu       sync.Once
	children []UT.BatchProducer
	done     bool
}

// NewVectorizedInSubquery creates a new VectorizedInSubquery.
// keyTypes describes the type of each key column.
func NewVectorizedInSubquery(children ...UT.BatchProducer) *VectorizedInSubquery {
	return &VectorizedInSubquery{
		keyTypes: nil, // populated lazily
		children: children,
	}
}

// WithKeyTypes sets the key column types for multi-column IN.
func (v *VectorizedInSubquery) WithKeyTypes(types []LX.TokenType) *VectorizedInSubquery {
	v.keyTypes = types
	return v
}

// BuildKeySet drains all child producers and builds the hash set.
// REQ002042: builds composite keys from ALL columns, not just Cols[0].
func (v *VectorizedInSubquery) BuildKeySet(ctx context.Context) error {
	v.mu.Do(func() {
		v.keySet = make(map[string]bool)
		v.numCols = 0
		for _, child := range v.children {
			for {
				batch, err := child.NextBatch(ctx)
				if err != nil {
					v.keySet = nil
					return
				}
				if batch == nil {
					break
				}
				if v.numCols == 0 {
					// Use ColNames() to get the actual number of active
					// columns (pooled batches have Cols with MaxColumns
					// entries, but only the first N have names set).
					v.numCols = len(batch.ColNames())
					if v.numCols == 0 {
						v.numCols = len(batch.Cols)
					}
				}
				for i := 0; i < batch.Size; i++ {
					key := encodeCompositeKey(batch, i, v.numCols)
					v.keySet[key] = true
				}
				batch.Put()
			}
		}
	})
	return nil
}

// Contains checks if the given key is in the set. For single-column
// integer keys, this is equivalent to the old API. REQ002042.
func (v *VectorizedInSubquery) Contains(key int64) bool {
	if v.keySet == nil {
		return false
	}
	// Fast path for single-column integer keys.
	if v.numCols <= 1 {
		return v.keySet[fmt.Sprintf("%d", key)]
	}
	return false
}

// ContainsKey checks if a composite key is in the set. REQ002042.
func (v *VectorizedInSubquery) ContainsKey(keys []int64) bool {
	if v.keySet == nil || len(keys) != v.numCols {
		return false
	}
	return v.keySet[encodeKeySlice(keys)]
}

// encodeCompositeKey builds a string key from the i-th row of a batch.
func encodeCompositeKey(batch *UT.Batch, row, nCols int) string {
	if nCols <= 0 {
		return ""
	}
	if nCols == 1 && len(batch.Cols) > 0 {
		// Fast path for single column.
		switch batch.Cols[0].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if row < len(batch.Cols[0].Data.Ints) {
				return fmt.Sprintf("%d", batch.Cols[0].Data.Ints[row])
			}
		case LX.T_FLOAT_KW:
			if row < len(batch.Cols[0].Data.Floats) {
				return fmt.Sprintf("%v", batch.Cols[0].Data.Floats[row])
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if row < len(batch.Cols[0].Data.Strs) {
				return batch.Cols[0].Data.Strs[row]
			}
		case LX.T_BOOL:
			if row < len(batch.Cols[0].Data.Bools) {
				return fmt.Sprintf("%t", batch.Cols[0].Data.Bools[row])
			}
		}
		return fmt.Sprintf("%v", UT.ToValue(batch.Cols[0], row).ToAny())
	}
	// Multi-column: join with "\x00" separator.
	var parts []string
	for c := 0; c < nCols && c < len(batch.Cols); c++ {
		parts = append(parts, fmt.Sprintf("%v", UT.ToValue(batch.Cols[c], row).ToAny()))
	}
	return strings.Join(parts, "\x00")
}

// encodeKeySlice builds a string key from a []int64 slice.
func encodeKeySlice(keys []int64) string {
	if len(keys) == 0 {
		return ""
	}
	if len(keys) == 1 {
		return fmt.Sprintf("%d", keys[0])
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d", k)
	}
	return strings.Join(parts, "\x00")
}

// Close releases all child producers.
func (v *VectorizedInSubquery) Close() error {
	for _, child := range v.children {
		if err := child.Close(); err != nil {
			return err
		}
	}
	return nil
}

// VectorizedExistsSubquery pre-materializes a subquery and checks
// for existence (short-circuits on first row).
// REQ001445.
type VectorizedExistsSubquery struct {
	hasRows bool
	child   UT.BatchProducer
	done    bool
}

// NewVectorizedExistsSubquery creates a new VectorizedExistsSubquery.
func NewVectorizedExistsSubquery(child UT.BatchProducer) *VectorizedExistsSubquery {
	return &VectorizedExistsSubquery{child: child}
}

// Build checks if the subquery produces any rows.
func (v *VectorizedExistsSubquery) Build(ctx context.Context) error {
	for {
		batch, err := v.child.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		if batch.Size > 0 {
			v.hasRows = true
		}
		batch.Put()
		if v.hasRows {
			break // short-circuit
		}
	}
	return nil
}

// Exists returns true if the subquery produced at least one row.
func (v *VectorizedExistsSubquery) Exists() bool {
	return v.hasRows
}

// Close releases the child producer.
func (v *VectorizedExistsSubquery) Close() error {
	if v.child != nil {
		return v.child.Close()
	}
	return nil
}

// VectorizedScalarSubquery pre-materializes a scalar subquery result.
// Returns the first row's first column value.
// REQ001445.
type VectorizedScalarSubquery struct {
	value any
	hasVal bool
	child  UT.BatchProducer
	done   bool
}

// NewVectorizedScalarSubquery creates a new VectorizedScalarSubquery.
func NewVectorizedScalarSubquery(child UT.BatchProducer) *VectorizedScalarSubquery {
	return &VectorizedScalarSubquery{child: child}
}

// Build drains the subquery and captures the first row's first column.
func (v *VectorizedScalarSubquery) Build(ctx context.Context) error {
	for {
		batch, err := v.child.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		if batch.Size > 0 && len(batch.Cols) > 0 {
			switch batch.Cols[0].Type {
			case LX.T_INT_KW, LX.T_BIGINT:
				if batch.Size <= len(batch.Cols[0].Data.Ints) {
					v.value = batch.Cols[0].Data.Ints[0]
					v.hasVal = true
				}
			case LX.T_FLOAT_KW:
				if batch.Size <= len(batch.Cols[0].Data.Floats) {
					v.value = batch.Cols[0].Data.Floats[0]
					v.hasVal = true
				}
			case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
				if batch.Size <= len(batch.Cols[0].Data.Strs) {
					v.value = batch.Cols[0].Data.Strs[0]
					v.hasVal = true
				}
			case LX.T_BOOL:
				if batch.Size <= len(batch.Cols[0].Data.Bools) {
					v.value = batch.Cols[0].Data.Bools[0]
					v.hasVal = true
				}
			}
		}
		batch.Put()
		if v.hasVal {
			break // scalar subquery returns first row only
		}
	}
	return nil
}

// GetValue returns the scalar value.
func (v *VectorizedScalarSubquery) GetValue() (any, bool) {
	return v.value, v.hasVal
}

// Close releases the child producer.
func (v *VectorizedScalarSubquery) Close() error {
	if v.child != nil {
		return v.child.Close()
	}
	return nil
}
