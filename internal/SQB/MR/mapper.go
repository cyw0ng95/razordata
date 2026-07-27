package MR

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// Mapper extracts values from batches and feeds them to the Shuffler.
type Mapper interface {
	// MapBatch processes one batch, delegating row handling to the Shuffler.
	MapBatch(ctx context.Context, batch *UT.Batch, shuffle Shuffler) error
	// Reset clears mapper state.
	Reset()
}

// AggregateMapper maps batch rows to the aggregate shuffle.
// For the common case of int64 group keys, it delegates to the
// Shuffler's AcceptBatch for bulk processing.
type AggregateMapper struct {
	groupCols []int // GROUP BY column indices (nil = scalar)
}

// NewAggregateMapper creates an aggregate mapper.
func NewAggregateMapper(groupCols []int) *AggregateMapper {
	return &AggregateMapper{groupCols: groupCols}
}

func (m *AggregateMapper) MapBatch(ctx context.Context, batch *UT.Batch, shuffle Shuffler) error {
	return shuffle.AcceptBatch(ctx, batch)
}

func (m *AggregateMapper) Reset() {}
