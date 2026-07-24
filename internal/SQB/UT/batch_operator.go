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

// BatchSupportChecker is an optional interface that BatchProducer
// implementations may satisfy to indicate whether batch mode is
// currently available. For example, SeqScan satisfies BatchProducer
// but only supports batch mode when it has a Store. Operators that
// always support batch mode do not need to implement this.
type BatchSupportChecker interface {
	BatchSupported() bool
}
