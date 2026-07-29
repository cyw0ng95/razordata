package PX

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SQB/UT"
)

type MapReduceOperator struct {
	child   UT.BatchProducer
	mapper  Mapper
	shuffle Shuffler

	result []*UT.Batch
	pos    int
	done   bool
}

func NewMapReduceOperator(child UT.BatchProducer, mapper Mapper, shuffle Shuffler) *MapReduceOperator {
	return &MapReduceOperator{
		child:   child,
		mapper:  mapper,
		shuffle: shuffle,
	}
}

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

func (o *MapReduceOperator) Close() error {
	o.result = nil
	o.shuffle.Reset()
	o.mapper.Reset()
	return o.child.Close()
}

func (o *MapReduceOperator) Reset() {
	o.result = nil
	o.pos = 0
	o.done = false
	o.shuffle.Reset()
	o.mapper.Reset()
}