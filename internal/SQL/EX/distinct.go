// Package EX's distinct.go hosts the Distinct operator. DISTINCT is
// not part of the v1 MVP scope per docs/design/ARCH.md; the operator is
// retained in v1.1 for upcoming releases.
package EX

import (
	"context"
	"strconv"
)

type Distinct struct {
	child Operator
	seen  map[string]bool
	buf   []Row
	pos   int
}

func NewDistinct(child Operator) *Distinct {
	return &Distinct{child: child, seen: make(map[string]bool)}
}

func (d *Distinct) Next(ctx context.Context) (Row, error) {
	if d.buf == nil {
		for {
			row, err := d.child.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				return Row{}, err
			}
			key := distinctKey(row)
			if !d.seen[key] {
				d.seen[key] = true
				d.buf = append(d.buf, row)
			}
		}
	}
	if d.pos >= len(d.buf) {
		return Row{}, ErrNoRows
	}
	r := d.buf[d.pos]
	d.pos++
	return r, nil
}

func (d *Distinct) Close() error {
	d.buf = nil
	d.pos = 0
	d.seen = make(map[string]bool)
	return d.child.Close()
}

func distinctKey(row Row) string {
	if len(row.Data) == 0 {
		return ""
	}
out := make([]byte, 0, len(row.Data)*8)
	for i, d := range row.Data {
		if i > 0 {
			out = append(out, 0)
		}
		switch d.Kind {
		case KindNull:
			out = append(out, "N"...)
		case KindInt:
			out = strconv.AppendInt(out, d.I64, 10)
		case KindFloat:
			out = strconv.AppendFloat(out, d.F64, 'g', -1, 64)
		case KindText:
			out = append(out, "S"...)
			out = append(out, d.S...)
		case KindBool:
			if d.B {
				out = append(out, "T"...)
			} else {
				out = append(out, "F"...)
			}
		default:
			out = append(out, "O"...)
		}
	}
	return string(out)
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func ftoa(f float64) string {
	return itoa(int64(f)) + "." + itoa(int64(f*1000)%1000)
}
