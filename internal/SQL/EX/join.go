// Package EX's join.go hosts NestedLoopJoin. Joins are not part of the
// v1 MVP scope per design/ARCH.md; the operator is retained in v1.1 for
// upcoming releases.
package EX

import (
	"context"
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
			tablesMu.RLock()
			j.rightRows = make([]Row, len(tables[j.rightTbl]))
			copy(j.rightRows, tables[j.rightTbl])
			tablesMu.RUnlock()
			j.rightPos = 0
			j.matched = false
			_ = j.right.Close()
		}
		if j.rightPos >= len(j.rightRows) {
			// No more right rows
			if j.kind == JoinKindLeft || j.kind == JoinKindFull {
				// OUTER JOIN: emit left row with NULL-padded right
				if !j.matched {
					nullRow := j.nullRightRow()
					result := joinRows(j.leftRow, &nullRow)
					j.leftRow = nil
					return result, nil
				}
			}
			j.leftRow = nil
			continue
		}
		inner := cloneRow(j.rightRows[j.rightPos])
		j.rightPos++
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
		return joinRows(j.leftRow, &inner), nil
	}
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

func joinRows(a, b *Row) Row {
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
