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
	"sort"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type CompoundOp struct {
	left         Operator
	right        Operator
	op           PS.CompoundOp
	params       []interface{}
	materialized bool
	buf          []Row
	pos          int
	// post-process
	orderBy []PS.OrderItem
	limit   PS.Expr
	offset  PS.Expr
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

func (c *CompoundOp) WithParams(p []interface{}) Operator {
	c.params = p
	return c
}

func (c *CompoundOp) Next(ctx context.Context) (Row, error) {
	if !c.materialized {
		leftRows, err := drainAll(ctx, c.left)
		if err != nil {
			return Row{}, err
		}
		rightRows, err := drainAll(ctx, c.right)
		if err != nil {
			return Row{}, err
		}
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
			sort.SliceStable(result, func(i, j int) bool {
				a := result[i]
				b := result[j]
				for _, k := range c.orderBy {
					av, _ := Eval(k.Expr, &a, c.params)
					bv, _ := Eval(k.Expr, &b, c.params)
					cmp := compare(av, bv)
					if cmp == 0 {
						continue
					}
					if k.Desc {
						return cmp > 0
					}
					return cmp < 0
				}
				return false
			})
		}
		// Apply OFFSET / LIMIT.
		off := 0
		if c.offset != nil {
			if v, err := Eval(c.offset, nil, c.params); err == nil {
				if n, ok := toInt64(v); ok {
					off = int(n)
				}
			}
		}
		if off > 0 && off < len(result) {
			result = result[off:]
		} else if off >= len(result) {
			result = nil
		}
		if c.limit != nil {
			if v, err := Eval(c.limit, nil, c.params); err == nil {
				if n, ok := toInt64(v); ok && int(n) < len(result) {
					result = result[:n]
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

func (c *CompoundOp) Close() error {
	c.buf = nil
	c.pos = 0
	if err := c.left.Close(); err != nil {
		return err
	}
	return c.right.Close()
}

// drainAll pulls all rows from op into a slice.
func drainAll(ctx context.Context, op Operator) ([]Row, error) {
	var out []Row
	for {
		r, err := op.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				return out, nil
			}
			return nil, err
		}
		out = append(out, r)
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
