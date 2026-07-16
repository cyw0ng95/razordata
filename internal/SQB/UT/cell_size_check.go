package UT

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// CellSizeCheck is the executor for PRAGMA cell_size_check.
// Verifies that no BLOB/text cell overflows the declared page size.
// Returns empty result set if all checks pass. REQ001387.
type CellSizeCheck struct {
	store Store
	done  bool
	pos   int
	rows  []Row
}

// NewCellSizeCheck creates the operator.
func NewCellSizeCheck() *CellSizeCheck {
	return &CellSizeCheck{}
}

// NewCellSizeCheckWithStore creates the operator with engine access.
func NewCellSizeCheckWithStore(store Store) *CellSizeCheck {
	return &CellSizeCheck{store: store}
}

func (c *CellSizeCheck) Next(ctx context.Context) (Row, error) {
	if c.done {
		if c.pos < len(c.rows) {
			row := c.rows[c.pos]
			c.pos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}
	c.done = true

	// Walk all in-memory table data and check cell sizes.
	DT.TablesMu.RLock()
	tableNames := make([]string, 0, len(DT.Tables))
	for name := range DT.Tables {
		tableNames = append(tableNames, name)
	}
	DT.TablesMu.RUnlock()

	for _, tableName := range tableNames {
		DT.TablesMu.RLock()
		rows, hasRows := DT.Tables[tableName]
		DT.TablesMu.RUnlock()
		if !hasRows {
			continue
		}
		DT.TablesMu.RLock()
		schema, hasSchema := DT.Schemas[tableName]
		DT.TablesMu.RUnlock()
		if !hasSchema {
			continue
		}

		for ri, row := range rows {
			for ci := 0; ci < len(row.Data) && ci < len(schema); ci++ {
				val := row.Data[ci]
				if val.Kind == DT.KindText && len(val.S) > 0 {
					// Text cells are checked against page size limit.
					// Default SQLite page size is 4096 bytes.
					if len(val.S) > 4096 {
						c.rows = append(c.rows, Row{
							Cols: []string{"table", "page", "error"},
							Data: []Value{
								DT.NewTextValue(tableName),
								DT.NewTextValue(""),
								DT.NewTextValue(
									"cell_size_check: row " + itoa(ri) +
										" col " + itoa(ci) +
										": text cell size " + itoa(len(val.S)) +
										" exceeds page size 4096",
								),
							},
						})
					}
				}
				if val.Kind == DT.KindBlob && len(val.B) > 0 {
					if len(val.B) > 4096 {
						c.rows = append(c.rows, Row{
							Cols: []string{"table", "page", "error"},
							Data: []Value{
								DT.NewTextValue(tableName),
								DT.NewTextValue(""),
								DT.NewTextValue(
									"cell_size_check: row " + itoa(ri) +
										" col " + itoa(ci) +
										": blob cell size " + itoa(len(val.B)) +
										" exceeds page size 4096",
								),
							},
						})
					}
				}
			}
		}
	}

	return Row{}, ErrNoRows
}

func (c *CellSizeCheck) Close() error { return nil }

func (c *CellSizeCheck) RowsAffected() int64 { return int64(len(c.rows)) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
