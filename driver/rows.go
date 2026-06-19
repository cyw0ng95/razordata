package driver

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
func newRows(apRows *AP.Rows) *Rows {
	r := &Rows{columns: apRows.GetCols()}
	if apRows == nil {
		return r
	}
	for {
		row, err := apRows.Next()
		if err != nil {
			break
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
	for i := range dest {
		if i < len(row.Data) {
			dest[i] = toDriverValue(row.Data[i])
		} else {
			dest[i] = nil
		}
	}
	return nil
}
