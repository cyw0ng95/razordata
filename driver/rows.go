package driver

import (
	"database/sql/driver"
	"io"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

type Rows struct {
	columns []string
	apRows  *AP.Rows
}

func newRows(apRows *AP.Rows) *Rows {
	if apRows == nil {
		return &Rows{}
	}
	return &Rows{columns: apRows.Cols(), apRows: apRows}
}

func (r *Rows) Columns() []string { return r.columns }

func (r *Rows) Close() error {
	if r == nil {
		return nil
	}
	if r.apRows != nil {
		_ = r.apRows.Close()
		r.apRows = nil
	}
	r.columns = nil
	return nil
}

func (r *Rows) Next(dest []driver.Value) error {
	if r == nil || r.apRows == nil {
		return io.EOF
	}
	row, err := r.apRows.Next()
	if err != nil {
		return io.EOF
	}
	for i := range dest {
		if i < len(row.Data) {
			dest[i] = toDriverValue(row.Data[i])
		} else {
			dest[i] = nil
		}
	}
	return nil
}
