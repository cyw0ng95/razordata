package PX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

type Mapper interface {
	MapBatch(ctx context.Context, batch *UT.Batch, shuffle Shuffler) error
	Reset()
}

type AggregateMapper struct {
	groupCols []int
}

func NewAggregateMapper(groupCols []int) *AggregateMapper {
	return &AggregateMapper{groupCols: groupCols}
}

func (m *AggregateMapper) MapBatch(ctx context.Context, batch *UT.Batch, shuffle Shuffler) error {
	return shuffle.AcceptBatch(ctx, batch)
}

func (m *AggregateMapper) Reset() {}