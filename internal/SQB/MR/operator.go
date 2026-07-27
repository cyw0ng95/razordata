package MR

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// MapReduceOperator is a batch producer that implements the map-reduce
// pattern. It drains all child batches through the Mapper into the
// Shuffler, then Finalizes to produce output batches.
type MapReduceOperator struct {
	child   UT.BatchProducer
	mapper  Mapper
	shuffle Shuffler

	result []*UT.Batch
	pos    int
	done   bool
}

// NewMapReduceOperator creates a new map-reduce operator.
func NewMapReduceOperator(child UT.BatchProducer, mapper Mapper, shuffle Shuffler) *MapReduceOperator {
	return &MapReduceOperator{
		child:   child,
		mapper:  mapper,
		shuffle: shuffle,
	}
}

// NextBatch returns the next output batch. On first call, drains all
// child batches through map-shuffle-reduce. Subsequent calls return
// from the cached result. Returns nil at EOF.
func (o *MapReduceOperator) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if !o.done {
		if err := o.drain(ctx); err != nil {
			return nil, err
		}
	}
	if o.pos >= len(o.result) {
		return nil, nil
	}
	b := o.result[o.pos]
	o.pos++
	return b, nil
}

func (o *MapReduceOperator) drain(ctx context.Context) error {
	for {
		batch, err := o.child.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		if err := o.mapper.MapBatch(ctx, batch, o.shuffle); err != nil {
			batch.Put()
			return err
		}
		batch.Put()
	}
	result, err := o.shuffle.Finalize(ctx)
	if err != nil {
		return err
	}
	o.result = result
	o.done = true
	return nil
}

// Close releases all resources.
func (o *MapReduceOperator) Close() error {
	o.result = nil
	o.shuffle.Reset()
	o.mapper.Reset()
	return o.child.Close()
}

// Reset resets the operator for re-execution with the same child.
func (o *MapReduceOperator) Reset() {
	o.result = nil
	o.pos = 0
	o.done = false
	o.shuffle.Reset()
	o.mapper.Reset()
}
