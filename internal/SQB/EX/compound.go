// REQ000383: UNION / UNION ALL / INTERSECT / EXCEPT executor.
// CompoundStmt is a left-associative chain of two child
// statements. The compound operator materializes both children
// (which is required for INTERSECT / EXCEPT / UNION, since the
// set operators need both sides to be available), then either:
//   - emits all rows concatenated (UNION ALL), or
//   - dedups by row content (UNION), or
//   - emits rows that are in both (INTERSECT), or
//   - emits rows in the left that are not in the right (EXCEPT).
//
// After the set operator, the optional ORDER BY / LIMIT / OFFSET
// declared on the CompoundStmt is applied to the materialized
// result.
package EX

import (
	"context"
	"fmt"
	"slices"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type CompoundOp struct {
	left         Operator
	right        Operator
	op           PS.CompoundOp
	params       []any
	materialized bool
	buf          []Row
	pos          int
	// post-process
	orderBy []PS.OrderItem
	limit   PS.Expr
	offset  PS.Expr
	// REQ000838: streaming state for the fast-path UNION ALL with
	// no post-processing. Avoids materializing the full result set
	// into a []Row at every level of the binary tree.
	streamSide streamSide
	// REQ000842: streaming state for EXCEPT/INTERSECT. Right side
	// is fully drained first to build a distinct-key hash set.
	// Left side is then streamed and probed against the set.
	rightKeys    map[string]bool
	emittedKeys  map[string]bool
	rightDrained bool
	// REQ001057: memory budget for drainAll to prevent OOM on deep
	// compound chains. 0 means use the default 1M row cap.
	maxDrainRows int64
	// REQ001057: memory limit for drainAll materialization.
	// 0 = unlimited. Set by Planner from Executor.WithMemoryBudget.
	maxMemory int64
	// Re-exported from PS for convenience.
	_ bool // alignment placeholder
}

func NewCompoundOp(left, right Operator, op PS.CompoundOp, orderBy []PS.OrderItem, limit, offset PS.Expr) *CompoundOp {
	return &CompoundOp{
		left:    left,
		right:   right,
		op:      op,
		orderBy: orderBy,
		limit:   limit,
		offset:  offset,
	}
}

// WithMemoryLimit sets the per-compound-operator memory cap.
// 0 = unlimited. REQ001057.
func (c *CompoundOp) WithMemoryLimit(v int64) *CompoundOp {
	c.maxMemory = v
	return c
}

func (c *CompoundOp) WithParams(p []any) Operator {
	c.params = p
	return c
}

func (c *CompoundOp) Next(ctx context.Context) (Row, error) {
	// REQ000838: fast path for UNION ALL with no post-processing
	// (no ORDER BY, no LIMIT, no OFFSET). The original implementation
	// always drained both children into []Row and concatenated —
	// for select4's 8-branch UNION ALL chains this materializes
	// intermediate slices at every level of the binary tree, an
	// O(N^2) memory traffic shape. The streaming path simply
	// pulls rows from left.Next() until ErrNoRows, then from
	// right.Next() — no allocation, no dedup work, no order/limit
	// processing. The plan tree can still nest CompoundOp instances
	// (a UNION ALL b UNION ALL c is a left-deep tree), and each
	// level benefits from this fast path so memory traffic is O(N)
	// total instead of O(N * depth).
	if c.canStream() {
		return c.nextStreaming(ctx)
	}
	if !c.materialized {
		leftRows, err := drainAll(ctx, c.left, c.maxDrainRows, c.maxMemory)
		if err != nil {
			return Row{}, err
		}
		// REQ001081: close children after draining to reset streaming
		// state in nested compound operators.
		_ = c.left.Close()
		rightRows, err := drainAll(ctx, c.right, c.maxDrainRows, c.maxMemory)
		if err != nil {
			return Row{}, err
		}
		_ = c.right.Close()
		// REQ000383: column-count check.
		if len(leftRows) > 0 && len(rightRows) > 0 &&
			len(leftRows[0].Data) != len(rightRows[0].Data) {
			return Row{}, fmt.Errorf("compound select: column count mismatch (%d vs %d)",
				len(leftRows[0].Data), len(rightRows[0].Data))
		}
		// Choose schema from left (UNION's output is the left's
		// column names; the right side's aliases are discarded).
		var cols []string
		var types []int
		if len(leftRows) > 0 {
			cols = leftRows[0].Cols
			types = leftRows[0].Types
		} else if len(rightRows) > 0 {
			cols = rightRows[0].Cols
			types = rightRows[0].Types
		}
		var result []Row
		switch c.op {
		case PS.CompoundUnionAll:
			result = append(result, leftRows...)
			result = append(result, rightRows...)
		case PS.CompoundUnion:
			result = dedupRows(append(append([]Row{}, leftRows...), rightRows...))
		case PS.CompoundIntersect:
			result = intersectRows(leftRows, rightRows)
		case PS.CompoundExcept:
			result = exceptRows(leftRows, rightRows)
		}
		// Normalize row schema to chosen cols/types. The right
		// side's rows may have different Cols/Types; replace
		// them with the left's so the result is consistent.
		for i := range result {
			result[i].Cols = cols
			result[i].Types = types
		}
		// Apply ORDER BY if present.
		if len(c.orderBy) > 0 {
			// REQ001019: Schwartzian transform — pre-extract sort key
			// values for all rows to avoid O(N log N) expression
			// evaluations per comparison.
			type decoratedRow struct {
				row  Row
				keys []Value
			}
			decorated := make([]decoratedRow, len(result))
			for i := range result {
				vals := make([]Value, len(c.orderBy))
				for j, k := range c.orderBy {
					v, _ := EvalValue(k.Expr, &result[i], c.params)
					vals[j] = v
				}
				decorated[i].row = result[i]
				decorated[i].keys = vals
			}
			slices.SortStableFunc(decorated, func(a, b decoratedRow) int {
				for j, k := range c.orderBy {
					cmp := compareValue(a.keys[j], b.keys[j])
					if cmp == 0 {
						continue
					}
					if k.Desc {
						return -cmp
					}
					return cmp
				}
				return 0
			})
			for i, d := range decorated {
				result[i] = d.row
			}
		}
		// Apply OFFSET / LIMIT.
		off := 0
		if c.offset != nil {
			if v, err := EvalValue(c.offset, nil, c.params); err == nil {
				if v.Kind == KindInt {
					off = int(v.I64)
				}
			}
		}
		if off > 0 && off < len(result) {
			result = result[off:]
		} else if off >= len(result) {
			result = nil
		}
		if c.limit != nil {
			if v, err := EvalValue(c.limit, nil, c.params); err == nil {
				if v.Kind == KindInt && int(v.I64) < len(result) {
					result = result[:v.I64]
				}
			}
		}
		c.buf = result
		c.materialized = true
	}
	if c.pos >= len(c.buf) {
		return Row{}, ErrNoRows
	}
	r := c.buf[c.pos]
	c.pos++
	return r, nil
}

// canStream reports whether c can use the streaming path (REQ000838/842):
// UNION ALL, EXCEPT, or INTERSECT with no ORDER BY, LIMIT, or OFFSET.
// UNION ALL streams both children. EXCEPT/INTERSECT drain the right
// side first (to build a hash set), then stream the left side.
func (c *CompoundOp) canStream() bool {
	if len(c.orderBy) > 0 || c.limit != nil || c.offset != nil {
		return false
	}
	return c.op == PS.CompoundUnionAll ||
		c.op == PS.CompoundExcept ||
		c.op == PS.CompoundIntersect
}

// streamSide tracks which child is currently being drained in the
// streaming path. REQ000838.
type streamSide uint8

const (
	streamLeft streamSide = iota
	streamRight
	streamDone
)

// nextStreaming implements the streaming fast paths:
//   - UNION ALL (REQ000838): pull left then right, no intermediate buffer.
//   - EXCEPT/INTERSECT (REQ000842): drain right first (hash set), then stream
//     left and probe against rightKeys. Left-side rows are deduplicated via
//     emittedKeys tracking. This cuts peak memory by avoiding left-side
//     materialization for deep EXCEPT/INTERSECT chains.
func (c *CompoundOp) nextStreaming(ctx context.Context) (Row, error) {
	if c.op == PS.CompoundUnionAll {
		return c.nextStreamingUnionAll(ctx)
	}
	return c.nextStreamingSetOp(ctx)
}

func (c *CompoundOp) nextStreamingUnionAll(ctx context.Context) (Row, error) {
	if c.streamSide == streamDone {
		return Row{}, ErrNoRows
	}
	for {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		if c.streamSide == streamLeft {
			r, err := c.left.Next(ctx)
			if err == nil {
				return r, nil
			}
			if err != ErrNoRows {
				return Row{}, err
			}
			c.streamSide = streamRight
			continue
		}
		r, err := c.right.Next(ctx)
		if err == nil {
			return r, nil
		}
		if err != ErrNoRows {
			return Row{}, err
		}
		c.streamSide = streamDone
		return Row{}, ErrNoRows
	}
}

func (c *CompoundOp) nextStreamingSetOp(ctx context.Context) (Row, error) {
	if !c.rightDrained {
		rightRows, err := drainAll(ctx, c.right, c.maxDrainRows, c.maxMemory)
		if err != nil {
			return Row{}, err
		}
		// REQ001081: close the right child after draining to reset
		// any streaming state in nested compound operators.
		_ = c.right.Close()
		c.rightKeys = make(map[string]bool, len(rightRows))
		for _, r := range rightRows {
			c.rightKeys[distinctKey(r)] = true
		}
		c.emittedKeys = make(map[string]bool)
		c.rightDrained = true
	}

	for {
		if err := ctx.Err(); err != nil {
			return Row{}, err
		}
		r, err := c.left.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				return Row{}, ErrNoRows
			}
			return Row{}, err
		}
		k := distinctKey(r)
		inRight := c.rightKeys[k]
		if c.op == PS.CompoundExcept {
			if inRight {
				continue
			}
		} else {
			if !inRight {
				continue
			}
		}
		if c.emittedKeys[k] {
			continue
		}
		c.emittedKeys[k] = true
		return r, nil
	}
}

func (c *CompoundOp) Close() error {
	// REQ001081: close children FIRST so their internal state
	// (Filter compiledOnce, batch buffers, SeqScan pos) is reset
	// before we clear our own references. This is critical for
	// memo-cached plan trees where the same CompoundOp instance
	// is reused across executions.
	var err1, err2 error
	err1 = c.left.Close()
	err2 = c.right.Close()
	// Now reset our own state.
	c.buf = nil
	c.pos = 0
	c.materialized = false
	c.streamSide = streamLeft
	c.rightDrained = false
	c.emittedKeys = nil
	c.rightKeys = nil
	if err1 != nil {
		return err1
	}
	return err2
}

// drainAll pulls up to maxRows rows from op. maxRows=0 means unlimited.
// Defaults to 1M rows when maxRows is 0 (safety limit for intermediate
// compound operator materialization — REQ001056).
// maxMemory caps total memory usage (0 = unlimited). REQ001057.
func drainAll(ctx context.Context, op Operator, maxRows int64, maxMemory int64) ([]Row, error) {
	if maxRows <= 0 {
		maxRows = 1_000_000 // safety cap: 1M rows ≈ 50MB per drain
	}
	var out []Row
	var memUsed int64
	for {
		if int64(len(out)) >= maxRows {
			return out, nil
		}
		r, err := op.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				return out, nil
			}
			return nil, err
		}
		out = append(out, r)
		// Estimate memory: each Row ≈ len(Data) * 24 bytes + 64 base.
		memUsed += int64(len(r.Data))*24 + 64
		if maxMemory > 0 && memUsed > maxMemory {
			return out, fmt.Errorf("compound operator materialized %d rows (~%d bytes), exceeds memory limit=%d", len(out), memUsed, maxMemory)
		}
	}
}

func dedupRows(in []Row) []Row {
	seen := make(map[string]bool, len(in))
	out := make([]Row, 0, len(in))
	for _, r := range in {
		k := distinctKey(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func intersectRows(left, right []Row) []Row {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	rightKeys := make(map[string]bool, len(right))
	for _, r := range right {
		rightKeys[distinctKey(r)] = true
	}
	seen := make(map[string]bool)
	var out []Row
	for _, r := range left {
		k := distinctKey(r)
		if rightKeys[k] && !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func exceptRows(left, right []Row) []Row {
	if len(left) == 0 {
		return nil
	}
	rightKeys := make(map[string]bool, len(right))
	for _, r := range right {
		rightKeys[distinctKey(r)] = true
	}
	seen := make(map[string]bool)
	var out []Row
	for _, r := range left {
		k := distinctKey(r)
		if rightKeys[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}
