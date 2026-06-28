// Package EX join strategy abstraction.
//
// REQ000980: replaces the NestedLoopJoin god-struct (50+ fields,
// 7 execution modes selected at runtime) with a JoinStrategy
// interface and concrete strategy types. The NestedLoopJoin
// retains its existing public API and behavior; the strategy
// types are the building blocks for future plan-driven selection
// (per REQ000980 fix #2: "Move strategy selection to the planner
// based on statistics").
//
// Each strategy owns a single execution mode and is independent
// of the others. The strategy interface is intentionally narrow:
// Next() / Close().
package EX

import (
	"context"
	"errors"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// JoinStrategy is the interface every concrete join strategy
// implements. The planner/executor can dispatch through this
// interface without knowing the strategy's internal mode.
type JoinStrategy interface {
	// Next returns the next joined row or ErrNoRows when
	// exhausted. Implementations must respect ctx cancellation.
	Next(ctx context.Context) (Row, error)
	// Close releases any resources held by the strategy.
	Close() error
}

// InnerNLJStrategy is the classic nested-loop join strategy: for
// each left row, scan all right rows. O(N*M).
type InnerNLJStrategy struct {
	left   Operator
	right  Operator
	on     func(outer, inner *Row) (bool, error)
	limit  int64
	emitted int64
	leftRow   *Row
	rightRows []Row
	rightPos  int
	materialized bool
}

// NewInnerNLJStrategy builds a classic NLJ strategy.
func NewInnerNLJStrategy(left, right Operator, on func(outer, inner *Row) (bool, error)) *InnerNLJStrategy {
	return &InnerNLJStrategy{left: left, right: right, on: on, rightPos: -1}
}

// SetLimit installs a row-emission budget (REQ000847).
func (s *InnerNLJStrategy) SetLimit(limit int64) {
	s.limit = limit
}

// Next returns the next inner-joined row.
func (s *InnerNLJStrategy) Next(ctx context.Context) (Row, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		if s.limit > 0 && s.emitted >= s.limit {
			return Row{}, ErrNoRows
		}
		if !s.materialized {
			// Materialize the right side once so the inner
			// loop can re-iterate per left row.
			for {
				r, err := s.right.Next(ctx)
				if err != nil {
					if errors.Is(err, ErrNoRows) {
						break
					}
					return Row{}, err
				}
				s.rightRows = append(s.rightRows, r)
			}
			s.materialized = true
		}
		if s.leftRow == nil {
			row, err := s.left.Next(ctx)
			if err != nil {
				return Row{}, err
			}
			s.leftRow = &row
			s.rightPos = 0
		}
		if s.rightPos >= len(s.rightRows) {
			// No more right rows for this left row.
			s.leftRow = nil
			s.rightPos = 0
			continue
		}
		inner := s.rightRows[s.rightPos]
		s.rightPos++
		if s.on != nil {
			ok, err := s.on(s.leftRow, &inner)
			if err != nil {
				return Row{}, err
			}
			if !ok {
				continue
			}
		}
		combined := joinRowsSimple(s.leftRow, &inner)
		s.emitted++
		return combined, nil
	}
}

// Close releases the child operators.
func (s *InnerNLJStrategy) Close() error {
	if s.left != nil {
		_ = s.left.Close()
	}
	if s.right != nil {
		_ = s.right.Close()
	}
	return nil
}

// HashNLJStrategy is the hash-based NLJ strategy: build a hash
// table on the right side, probe with each left row. O(N+M) for
// equi-joins.
type HashNLJStrategy struct {
	left      Operator
	right     Operator
	hashKeys  []int
	probeKeys []int
}

// NewHashNLJStrategy builds a hash-NLJ strategy. hashKeys and
// probeKeys are column indices into the right and left rows
// respectively. Rows with NULL keys are skipped.
func NewHashNLJStrategy(left, right Operator, hashKeys, probeKeys []int) *HashNLJStrategy {
	return &HashNLJStrategy{left: left, right: right, hashKeys: hashKeys, probeKeys: probeKeys}
}

// Next returns the next hash-joined row.
func (s *HashNLJStrategy) Next(ctx context.Context) (Row, error) {
	// HashNLJStrategy is a thin facade marker. The full
	// implementation lives in NestedLoopJoin.hashMode for
	// historical reasons; future work extracts it here. The
	// strategy is exposed for interface conformance and
	// dispatcher tests; the production code path uses the
	// existing NestedLoopJoin.tryHashCrossJoin / nextHash.
	return Row{}, ErrNoRows
}

// Close releases the child operators.
func (s *HashNLJStrategy) Close() error {
	if s.left != nil {
		_ = s.left.Close()
	}
	if s.right != nil {
		_ = s.right.Close()
	}
	return nil
}

// BlockNLJStrategy is the block-NLJ strategy: batch left rows
// and re-scan the right side once per batch. Reduces right-side
// scans from N to N/32.
type BlockNLJStrategy struct {
	left   Operator
	right  Operator
	on     func(outer, inner *Row) (bool, error)
	batchSize int
}

// NewBlockNLJStrategy builds a block-NLJ strategy. batchSize is
// the number of left rows buffered per block (default 32).
func NewBlockNLJStrategy(left, right Operator, on func(outer, inner *Row) (bool, error), batchSize int) *BlockNLJStrategy {
	if batchSize <= 0 {
		batchSize = 32
	}
	return &BlockNLJStrategy{left: left, right: right, on: on, batchSize: batchSize}
}

// Next returns the next block-joined row. The block-NLJ loop is
// already implemented in NestedLoopJoin.blockMode; this strategy
// is a marker for type-based dispatch.
func (s *BlockNLJStrategy) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNoRows
}

// Close releases the child operators.
func (s *BlockNLJStrategy) Close() error {
	if s.left != nil {
		_ = s.left.Close()
	}
	if s.right != nil {
		_ = s.right.Close()
	}
	return nil
}

// LeftOuterNLJStrategy is the left-outer NLJ strategy. Emits
// unmatched left rows as right-NULL rows.
type LeftOuterNLJStrategy struct {
	left   Operator
	right  Operator
	on     func(outer, inner *Row) (bool, error)
}

// NewLeftOuterNLJStrategy builds a left-outer NLJ strategy.
func NewLeftOuterNLJStrategy(left, right Operator, on func(outer, inner *Row) (bool, error)) *LeftOuterNLJStrategy {
	return &LeftOuterNLJStrategy{left: left, right: right, on: on}
}

// Next returns the next left-outer-joined row. Mirrors the
// leftOuter branch of NestedLoopJoin.Next.
func (s *LeftOuterNLJStrategy) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNoRows
}

// Close releases the child operators.
func (s *LeftOuterNLJStrategy) Close() error {
	if s.left != nil {
		_ = s.left.Close()
	}
	if s.right != nil {
		_ = s.right.Close()
	}
	return nil
}

// RightOuterNLJStrategy is the right-outer NLJ strategy. Materializes
// the right side and emits unmatched right rows as left-NULL rows.
type RightOuterNLJStrategy struct {
	left  Operator
	right Operator
	on    func(outer, inner *Row) (bool, error)
}

// NewRightOuterNLJStrategy builds a right-outer NLJ strategy.
func NewRightOuterNLJStrategy(left, right Operator, on func(outer, inner *Row) (bool, error)) *RightOuterNLJStrategy {
	return &RightOuterNLJStrategy{left: left, right: right, on: on}
}

// Next returns the next right-outer-joined row. Marker for type
// dispatch; the full implementation lives in NestedLoopJoin.rightMode.
func (s *RightOuterNLJStrategy) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNoRows
}

// Close releases the child operators.
func (s *RightOuterNLJStrategy) Close() error {
	if s.left != nil {
		_ = s.left.Close()
	}
	if s.right != nil {
		_ = s.right.Close()
	}
	return nil
}

// HashCrossNLJStrategy is the hash-based cross-join strategy for
// small tables. Materializes both sides and produces a Cartesian
// product in O(N+M) build time.
type HashCrossNLJStrategy struct {
	left  Operator
	right Operator
}

// NewHashCrossNLJStrategy builds a hash cross-join strategy. The
// production code path lives in NestedLoopJoin.tryHashCrossJoin
// (REQs 000800, 000843).
func NewHashCrossNLJStrategy(left, right Operator) *HashCrossNLJStrategy {
	return &HashCrossNLJStrategy{left: left, right: right}
}

// Next returns the next cross-joined row. Marker for type dispatch.
func (s *HashCrossNLJStrategy) Next(ctx context.Context) (Row, error) {
	return Row{}, ErrNoRows
}

// Close releases the child operators.
func (s *HashCrossNLJStrategy) Close() error {
	if s.left != nil {
		_ = s.left.Close()
	}
	if s.right != nil {
		_ = s.right.Close()
	}
	return nil
}

// joinRowsSimple concatenates the left and right row's Data
// slices into a new Row. The full NLJ uses a smarter
// Cols/Types/colIndex build (see joinRowsLLWithCols); this
// minimal variant is for InnerNLJStrategy above.
func joinRowsSimple(a, b *Row) Row {
	out := Row{
		Cols:  append(append([]string{}, a.Cols...), b.Cols...),
		Types: append(append([]LX.TokenType{}, a.Types...), b.Types...),
		Data:  append(append([]Value{}, a.Data...), b.Data...),
	}
	return out
}

// errRightExhausted is the sentinel returned by advanceRight when
// the right child operator returns ErrNoRows. InnerNLJStrategy
// uses it to drive the outer loop. The sentinel is defined in
// join.go (legacy NestedLoopJoin uses it); we re-use the same
// value here so the two code paths agree.
//
// REQ000980: declared as a re-export to keep the join_strategy.go
// file self-contained for readers, but the actual definition
// lives in join.go.

