package EX

import (
	"context"
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
		switch v := d.(type) {
		case nil:
			out = append(out, "N"...)
		case string:
			out = append(out, "S"...)
			out = append(out, v...)
		case []byte:
			out = append(out, "B"...)
			out = append(out, v...)
		case int64:
			out = append(out, "I"...)
			out = append(out, []byte(itoa(v))...)
		case int:
			out = append(out, "I"...)
			out = append(out, []byte(itoa(int64(v)))...)
		case float64:
			out = append(out, "F"...)
			out = append(out, []byte(ftoa(v))...)
		case bool:
			if v {
				out = append(out, "T"...)
			} else {
				out = append(out, "F"...)
			}
		default:
			out = append(out, []byte("?")...)
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
