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
	// REQ000867: inline the hot path — switch on AP.Value.Kind
	// directly, avoiding the toDriverValue function call overhead.
	// The int64 case (90% of cells in join benchmarks) is a single
	// field load with no type assertion.
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
