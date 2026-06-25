package DS

import (
	"database/sql/driver"
	"io"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// Rows is a materialized database/sql result set. The AP.Rows are
// fully drained into a slice on construction; this is simple and
// matches what most apps expect from sqlite3-style drivers, but it
// buffers memory for large result sets. A future optimization could
// stream rows without materializing.
type Rows struct {
	columns []string
	data    []AP.Row
	pos     int
}

// newRows drains an AP.Rows into a fully materialized slice.
// REQ000862: deep-copy each row's Data because the streaming
// iterator reuses an internal buffer (stream.BoxRow). Without the
// copy, all stored rows would share the same backing array.
func newRows(apRows *AP.Rows) *Rows {
	r := &Rows{columns: apRows.Cols()}
	if apRows == nil {
		return r
	}
	for {
		row, err := apRows.Next()
		if err != nil {
			break
		}
		// Deep-copy Data so the backing array is independent
		// of the streaming iterator's internal buffer.
		if row.Data != nil {
			data := make([]AP.Value, len(row.Data))
			copy(data, row.Data)
			row.Data = data
		}
		r.data = append(r.data, row)
	}
	apRows.Close()
	return r
}

// Columns returns the column names.
func (r *Rows) Columns() []string { return r.columns }

// Close releases the buffered rows.
func (r *Rows) Close() error {
	if r == nil {
		return nil
	}
	r.data = nil
	r.columns = nil
	return nil
}

// Next fills dest with the next row's values, or returns io.EOF
// when the result set is exhausted.
func (r *Rows) Next(dest []driver.Value) error {
	if r == nil {
		return io.EOF
	}
	if r.pos >= len(r.data) {
		return io.EOF
	}
	row := r.data[r.pos]
	r.pos++
	// REQ000867: inline Kind switch — avoids toDriverValue call overhead.
	for i := range dest {
		if i < len(row.Data) {
			v := row.Data[i]
			switch v.Kind {
			case AP.KindNull:
				dest[i] = nil
			case AP.KindInt:
				dest[i] = v.I64
			case AP.KindFloat:
				dest[i] = v.F64
			case AP.KindText:
				dest[i] = v.S
			case AP.KindBlob:
				dest[i] = v.B
			case AP.KindBool:
				if v.Bo {
					dest[i] = int64(1)
				} else {
					dest[i] = int64(0)
				}
			default:
				dest[i] = nil
			}
		} else {
			dest[i] = nil
		}
	}
	return nil
}
