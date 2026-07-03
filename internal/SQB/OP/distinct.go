package OP

import (
	"context"
	"strconv"
	"sync"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// Type aliases.
type Row = pl.Row
type Value = pl.Value
type ValueKind = pl.ValueKind
type Operator = pl.Operator

// Value kind constants.
const (
	KindNull  = AP.KindNull
	KindInt   = AP.KindInt
	KindFloat = AP.KindFloat
	KindText  = AP.KindText
	KindBlob  = AP.KindBlob
	KindBool  = AP.KindBool
)

// ErrNoRows is returned by Next() when no more rows exist.
var ErrNoRows = pl.ErrNoRows

// REQ001017: shared buffer pool for distinctKey to reduce allocation pressure
// in UNION/EXCEPT/INTERSECT and GROUP BY operations.
var distinctKeyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 64)
		return &b
	},
}

type Distinct struct {
	child pl.Operator
	seen  map[string]bool
	buf   []pl.Row
	pos   int
}

func NewDistinct(child pl.Operator) *Distinct {
	return &Distinct{child: child, seen: make(map[string]bool)}
}

func (d *Distinct) Next(ctx context.Context) (pl.Row, error) {
	if d.buf == nil {
		// Re-initialize seen map if it was cleared by Close().
		// REQ001xxx: Close() sets d.seen = nil, but the operator
		// may be reused after Close() (e.g., via adaptive exec).
		// Without this, d.seen[key] = true panics on nil map.
		if d.seen == nil {
			d.seen = make(map[string]bool)
		}
		for {
			row, err := d.child.Next(ctx)
			if err != nil {
				if err == ErrNoRows {
					break
				}
				return pl.Row{}, err
			}
			key := DistinctKey(row)
			if !d.seen[key] {
				d.seen[key] = true
				d.buf = append(d.buf, row)
			}
		}
	}
	if d.pos >= len(d.buf) {
		return pl.Row{}, ErrNoRows
	}
	r := d.buf[d.pos]
	d.pos++
	return r, nil
}

func (d *Distinct) Close() error {
	d.buf = nil
	d.seen = nil
	return d.child.Close()
}

// Child returns the wrapped child operator.
func (d *Distinct) Child() pl.Operator { return d.child }

func DistinctKey(row pl.Row) string {
	if len(row.Data) == 0 {
		return ""
	}
	bp := distinctKeyBufPool.Get().(*[]byte)
	out := (*bp)[:0]
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
			if d.Bo {
				out = append(out, "T"...)
			} else {
				out = append(out, "F"...)
			}
		default:
			out = append(out, "O"...)
		}
	}
	key := string(out)
	*bp = out
	distinctKeyBufPool.Put(bp)
	return key
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
