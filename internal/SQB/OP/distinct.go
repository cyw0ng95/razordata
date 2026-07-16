package OP

import (
	"context"
	"encoding/binary"
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
	d.pos = 0
	return d.child.Close()
}

// Reset reinitializes Distinct cursor. Does NOT close the child. REQ001464.
func (d *Distinct) Reset(ctx context.Context) error { d.buf = nil; d.seen = nil; d.pos = 0; return nil }

// Child returns the wrapped child operator.
func (d *Distinct) Child() pl.Operator { return d.child }

// DistinctKey builds a string key for a Row that uniquely identifies the
// row's values across all types. REQ001284: each value is prefixed with a
// Kind tag (I/F/T/B/N/L) to prevent collisions between different types that
// produce the same string representation (e.g., Int(1) vs Float(1.0)).
func DistinctKey(row pl.Row) string {
	if len(row.Data) == 0 {
		return ""
	}
	bp := distinctKeyBufPool.Get().(*[]byte)
	out := (*bp)[:0]
	for i, d := range row.Data {
		if i > 0 {
			out = append(out, 1) // \x01 column separator
		}
		switch d.Kind {
		case KindNull:
			out = append(out, 'N')
		case KindInt:
			out = append(out, 'I')
			if d.I64 < 0 {
				out = append(out, '-')
				out = binary.AppendUvarint(out, uint64(-d.I64))
			} else {
				out = binary.AppendUvarint(out, uint64(d.I64))
			}
		case KindFloat:
			out = append(out, 'F')
			out = strconv.AppendFloat(out, d.F64, 'g', -1, 64)
		case KindText:
			out = append(out, 'T', 1) // marker + \x01 sentinel
			out = binary.AppendUvarint(out, uint64(len(d.S)))
			out = append(out, d.S...)
		case KindBlob:
			out = append(out, 'L', 1) // marker + \x01 sentinel
			out = binary.AppendUvarint(out, uint64(len(d.B)))
			out = append(out, d.B...)
		case KindBool:
			out = append(out, 'B')
			if d.Bo {
				out = append(out, '1')
			} else {
				out = append(out, '0')
			}
		default:
			out = append(out, 'O')
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
