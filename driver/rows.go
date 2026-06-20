package driver

import (
	"database/sql/driver"
	"io"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

type Rows struct {
	columns []string
	data    []AP.Row
	pos     int
}

func newRows(apRows *AP.Rows) *Rows {
	if apRows == nil {
		return &Rows{}
	}
	r := &Rows{columns: apRows.Cols()}
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

func (r *Rows) Columns() []string { return r.columns }

func (r *Rows) Close() error {
	if r == nil {
		return nil
	}
	r.data = nil
	r.columns = nil
	return nil
}

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
