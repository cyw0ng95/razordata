package UT

import "context"

// BatchProducer is the canonical interface for any operator that
// produces columnar batches. It lives in UT because UT defines
// Batch, and this interface returns *Batch. Both AG and OP
// vectorized operators satisfy this interface.
type BatchProducer interface {
	NextBatch(ctx context.Context) (*Batch, error)
	Close() error
}
