package PX

import (
	"context"
	"fmt"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// CompoundStageSpec creates CompoundStage instances for set operations.
type CompoundStageSpec struct {
	Op PS.CompoundOp
}

// NewRuntime creates a CompoundStage from this spec.
func (s *CompoundStageSpec) NewRuntime() Stage {
	return &CompoundStage{op: s.Op}
}

// Category returns CatMapReduce — compound operations need to
// materialize both sides (except UNION ALL).
func (s *CompoundStageSpec) Category() StageCategory { return CatMapReduce }

// CompoundStage implements UNION/UNION ALL/INTERSECT/EXCEPT.
// UNION ALL: streaming passthrough (no pipeline break).
// UNION: dedup via hash set.
// EXCEPT/INTERSECT: drain right into hash set, stream left probing.
type CompoundStage struct {
	childLeft  Stage
	childRight Stage
	op         PS.CompoundOp

	mu           sync.Mutex
	closed       bool
	phase        int // 0=left, 1=right, 2=done
	rightSet     map[string]bool
	emittedSet   map[string]bool
	rightDrained bool
}

func (s *CompoundStage) SetChild(side ChildSide, child Stage) {
	switch side {
	case LeftChild:
		s.childLeft = child
	case RightChild:
		s.childRight = child
	}
}

func (s *CompoundStage) PropagateParams(args []any, buf *[]any) {}

// NextBatch returns the next batch from the compound result.
func (s *CompoundStage) NextBatch(ctx context.Context) (*UT.Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil
	}

	switch s.op {
	case PS.CompoundUnionAll:
		return s.nextUnionAll(ctx)
	case PS.CompoundUnion:
		return s.nextUnion(ctx)
	case PS.CompoundExcept:
		return s.nextExcept(ctx)
	case PS.CompoundIntersect:
		return s.nextIntersect(ctx)
	default:
		return nil, fmt.Errorf("px: unknown compound op %v", s.op)
	}
}

// unionAll: stream left then right.
func (s *CompoundStage) nextUnionAll(ctx context.Context) (*UT.Batch, error) {
	for s.phase < 2 {
		var child Stage
		if s.phase == 0 {
			child = s.childLeft
		} else {
			child = s.childRight
		}
		batch, err := child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch != nil {
			return batch, nil
		}
		s.phase++
	}
	return nil, nil
}

// union: dedup by row content.
func (s *CompoundStage) nextUnion(ctx context.Context) (*UT.Batch, error) {
	if s.emittedSet == nil {
		s.emittedSet = make(map[string]bool)
	}
	for s.phase < 2 {
		var child Stage
		if s.phase == 0 {
			child = s.childLeft
		} else {
			child = s.childRight
		}
		batch, err := child.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			s.phase++
			continue
		}
		keep := make([]uint16, 0, batch.Size)
		for i := 0; i < batch.Size; i++ {
			key := compoundRowKey(batch, i)
			if !s.emittedSet[key] {
				s.emittedSet[key] = true
				keep = append(keep, uint16(i))
			}
		}
		if len(keep) == 0 {
			batch.Put()
			continue
		}
		batch.Sel = keep
		batch.Size = len(keep)
		return batch, nil
	}
	return nil, nil
}

// except: drain right into set, stream left removing matches.
func (s *CompoundStage) nextExcept(ctx context.Context) (*UT.Batch, error) {
	if !s.rightDrained {
		s.rightSet = make(map[string]bool)
		for {
			batch, err := s.childRight.NextBatch(ctx)
			if err != nil {
				return nil, err
			}
			if batch == nil {
				break
			}
			for i := 0; i < batch.Size; i++ {
				s.rightSet[compoundRowKey(batch, i)] = true
			}
			batch.Put()
		}
		s.rightDrained = true
	}
	if s.emittedSet == nil {
		s.emittedSet = make(map[string]bool)
	}
	for {
		batch, err := s.childLeft.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil
		}
		keep := make([]uint16, 0, batch.Size)
		for i := 0; i < batch.Size; i++ {
			key := compoundRowKey(batch, i)
			if !s.rightSet[key] && !s.emittedSet[key] {
				s.emittedSet[key] = true
				keep = append(keep, uint16(i))
			}
		}
		if len(keep) == 0 {
			batch.Put()
			continue
		}
		batch.Sel = keep
		batch.Size = len(keep)
		return batch, nil
	}
}

// intersect: drain right into set, stream left keeping matches.
func (s *CompoundStage) nextIntersect(ctx context.Context) (*UT.Batch, error) {
	if !s.rightDrained {
		s.rightSet = make(map[string]bool)
		for {
			batch, err := s.childRight.NextBatch(ctx)
			if err != nil {
				return nil, err
			}
			if batch == nil {
				break
			}
			for i := 0; i < batch.Size; i++ {
				s.rightSet[compoundRowKey(batch, i)] = true
			}
			batch.Put()
		}
		s.rightDrained = true
	}
	if s.emittedSet == nil {
		s.emittedSet = make(map[string]bool)
	}
	for {
		batch, err := s.childLeft.NextBatch(ctx)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			return nil, nil
		}
		keep := make([]uint16, 0, batch.Size)
		for i := 0; i < batch.Size; i++ {
			key := compoundRowKey(batch, i)
			if s.rightSet[key] && !s.emittedSet[key] {
				s.emittedSet[key] = true
				keep = append(keep, uint16(i))
			}
		}
		if len(keep) == 0 {
			batch.Put()
			continue
		}
		batch.Sel = keep
		batch.Size = len(keep)
		return batch, nil
	}
}

// Reset returns to pre-execution state.
func (s *CompoundStage) Reset(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = 0
	s.rightSet = nil
	s.emittedSet = nil
	s.rightDrained = false
	return nil
}

// Close releases all resources.
func (s *CompoundStage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.rightSet = nil
	s.emittedSet = nil
	return nil
}

// compoundRowKey builds a string key from a batch row for set dedup.
func compoundRowKey(batch *UT.Batch, row int) string {
	phys := row
	if batch.Sel != nil && row < len(batch.Sel) {
		phys = int(batch.Sel[row])
	}
	var key string
	for _, col := range batch.Cols {
		if col.Nulls != nil && phys < len(col.Nulls) && col.Nulls[phys] {
			key += "\x00"
			continue
		}
		switch col.Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			if phys < len(col.Data.Ints) {
				key += fmt.Sprintf("%d\x00", col.Data.Ints[phys])
			}
		case LX.T_FLOAT_KW:
			if phys < len(col.Data.Floats) {
				key += fmt.Sprintf("%f\x00", col.Data.Floats[phys])
			}
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			if phys < len(col.Data.Strs) {
				key += col.Data.Strs[phys] + "\x00"
			}
		case LX.T_BOOL:
			if phys < len(col.Data.Bools) {
				key += fmt.Sprintf("%t\x00", col.Data.Bools[phys])
			}
		default:
			key += "\x00"
		}
	}
	return key
}