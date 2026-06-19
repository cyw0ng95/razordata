// Package EX's join.go hosts NestedLoopJoin. Joins are not part of the
// v1 MVP scope per docs/design/ARCH.md; the operator is retained in v1.1 for
// upcoming releases.
package EX

import (
	"context"
	"errors"
)

// JoinKind specifies the type of join.
type JoinKind string

const (
	JoinKindInner JoinKind = "INNER"
	JoinKindLeft  JoinKind = "LEFT"
	JoinKindRight JoinKind = "RIGHT"
	JoinKindFull  JoinKind = "FULL"
	JoinKindCross JoinKind = "CROSS"
)

type NestedLoopJoin struct {
	left      Operator
	right     Operator
	leftTbl   string
	rightTbl  string
	on        func(outer, inner *Row) (bool, error)
	kind      JoinKind
	leftRow   *Row
	rightPos  int
	rightRows []Row
	matched   bool // for OUTER JOIN: track if left row found a match
}

func NewNestedLoopJoin(left, right Operator, leftTable, rightTable string, on func(outer, inner *Row) (bool, error), kind JoinKind) *NestedLoopJoin {
	return &NestedLoopJoin{left: left, right: right, leftTbl: leftTable, rightTbl: rightTable, on: on, kind: kind}
}

// LeftChild returns the left child operator.
func (j *NestedLoopJoin) LeftChild() Operator { return j.left }

// RightChild returns the right child operator.
func (j *NestedLoopJoin) RightChild() Operator { return j.right }

func (j *NestedLoopJoin) Next(ctx context.Context) (Row, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	for {
		if j.leftRow == nil {
			row, err := j.left.Next(ctx)
			if err != nil {
				return Row{}, err
			}
			prefixed := Row{Cols: prefixCols(row.Cols, j.leftTbl), Types: row.Types, Data: row.Data}
			j.leftRow = &prefixed
			// REQ000368: drive the right side through its own
			// operator rather than the in-memory `tables` map.
			// The in-memory map is empty for store-backed
			// tables, which caused CROSS JOIN (and any JOIN
			// of store-backed tables) to return zero rows.
			j.rightPos = -1
			j.matched = false
		}
		// Advance the right side. Each call to j.right.Next()
		// yields the next row; we re-init when we've exhausted
		// the right side and need to move to the next left row.
		inner, err := j.advanceRight(ctx)
		if err != nil {
			if err == errRightExhausted {
				// No more right rows for this left row
				if j.kind == JoinKindLeft || j.kind == JoinKindFull {
					if !j.matched {
						nullRow := j.nullRightRow()
						result := joinRowsLL(j.leftRow, &nullRow)
						j.leftRow = nil
						return result, nil
					}
				}
				j.leftRow = nil
				continue
			}
			return Row{}, err
		}
		inner.Cols = prefixCols(inner.Cols, j.rightTbl)
		inner.Outer = j.leftRow
		if j.on != nil {
			ok, err := j.on(j.leftRow, &inner)
			if err != nil {
				return Row{}, err
			}
			if !ok {
				continue
			}
		}
		j.matched = true
		return joinRowsLL(j.leftRow, &inner), nil
	}
}

// errRightExhausted is the sentinel returned by advanceRight when
// the right-side operator has no more rows for the current left row.
var errRightExhausted = errors.New("ex: right side exhausted")

// advanceRight returns the next row from the right-side operator.
// When the right side is exhausted (ErrNoRows), it returns
// errRightExhausted so the outer loop can move to the next left
// row. The right side is reset (Close + re-Next) on each new left
// row so the right's iterator state is rewound.
func (j *NestedLoopJoin) advanceRight(ctx context.Context) (Row, error) {
	if j.rightPos == -1 {
		// First call for this left row: close + reopen the
		// right side so its iterator state is fresh.
		_ = j.right.Close()
		j.rightPos = 0
	}
	row, err := j.right.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			// Mark so the next call resets the iterator.
			j.rightPos = -1
			return Row{}, errRightExhausted
		}
		return Row{}, err
	}
	return row, nil
}

// nullRightRow returns a row with all NULL values for the right table schema.
func (j *NestedLoopJoin) nullRightRow() Row {
	tablesMu.RLock()
	rightSchema := tables[j.rightTbl]
	tablesMu.RUnlock()

	nullRow := Row{
		Cols:  prefixCols(schemaCols(rightSchema), j.rightTbl),
		Types: schemaTypes(rightSchema),
		Data:  make([]any, len(rightSchema)),
	}
	return nullRow
}

func (j *NestedLoopJoin) Close() error {
	j.leftRow = nil
	j.rightRows = nil
	j.rightPos = 0
	_ = j.left.Close()
	return j.right.Close()
}

func joinRowsLL(a, b *Row) Row {
	out := Row{}
	out.Cols = append(out.Cols, a.Cols...)
	out.Cols = append(out.Cols, b.Cols...)
	out.Types = append(out.Types, a.Types...)
	out.Types = append(out.Types, b.Types...)
	out.Data = append(out.Data, a.Data...)
	out.Data = append(out.Data, b.Data...)
	return out
}

func prefixCols(cols []string, prefix string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + "." + c
	}
	return out
}

// schemaCols extracts column names from a schema.
func schemaCols(rows []Row) []string {
	if len(rows) == 0 {
		return nil
	}
	return append([]string(nil), rows[0].Cols...)
}

// schemaTypes extracts column types from a schema.
func schemaTypes(rows []Row) []int {
	if len(rows) == 0 {
		return nil
	}
	return append([]int(nil), rows[0].Types...)
}
