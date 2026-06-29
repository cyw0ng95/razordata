package EX

import (
	"context"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// SqliteMaster is a virtual table that returns table metadata
// (REQ000727). It implements Operator and returns rows matching
// the sqlite_master schema: type, NewTextValue(name), tbl_name, rootpage, sql.
type SqliteMaster struct {
	rows []Row
	idx  int
}

// NewSqliteMaster creates a sqlite_master virtual table operator.
func NewSqliteMaster() *SqliteMaster {
	return &SqliteMaster{}
}

func (s *SqliteMaster) Next(_ context.Context) (Row, error) {
	if s.rows == nil {
		s.loadRows()
	}
	if s.idx >= len(s.rows) {
		return Row{}, ErrNoRows
	}
	row := s.rows[s.idx]
	s.idx++
	return row, nil
}

func (s *SqliteMaster) loadRows() {
	names := DT.AllTableNames()
	for _, name := range names {
		s.rows = append(s.rows, Row{
			Cols: []string{"type", "name", "tbl_name", "rootpage", "sql"},
			Data: []Value{NewTextValue("table"), NewTextValue(name), NewTextValue(name), NewIntValue(0), NullValue()},
		})
	}
}

func (s *SqliteMaster) Close() error                { return nil }
func (s *SqliteMaster) WithParams(_ []any) Operator { return s }
