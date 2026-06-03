package EX

import (
	"context"
)

type NestedLoopJoin struct {
	left      Operator
	right     Operator
	leftTbl   string
	rightTbl  string
	on        func(outer, inner *Row) (bool, error)
	leftRow   *Row
	rightPos  int
	rightRows []Row
}

func NewNestedLoopJoin(left, right Operator, leftTable, rightTable string, on func(outer, inner *Row) (bool, error)) *NestedLoopJoin {
	return &NestedLoopJoin{left: left, right: right, leftTbl: leftTable, rightTbl: rightTable, on: on}
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
			_ = j.right.Close()
		}
		if j.rightPos >= len(j.rightRows) {
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
		return joinRows(j.leftRow, &inner), nil
	}
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
