package OP

import (
	"context"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// VectorizedInSubquery pre-materializes a subquery's key column into
// a hash set and provides batch-level IN membership testing.
// REQ001445.
type VectorizedInSubquery struct {
	keySet   map[int64]bool
	keyTypes []LX.TokenType
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
func (v *VectorizedInSubquery) BuildKeySet(ctx context.Context) error {
	v.mu.Do(func() {
		v.keySet = make(map[int64]bool)
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
				for i := 0; i < batch.Size; i++ {
					if len(batch.Cols) > 0 && batch.Cols[0].Type == LX.T_INT_KW {
						if i < len(batch.Cols[0].Data.Ints) {
							v.keySet[batch.Cols[0].Data.Ints[i]] = true
						}
					}
				}
				batch.Put()
			}
		}
	})
	return nil
}

// Contains checks if the given key is in the set.
func (v *VectorizedInSubquery) Contains(key int64) bool {
	if v.keySet == nil {
		return false
	}
	return v.keySet[key]
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
