package EX

import (
	"context"
)

// SqliteMaster is a virtual table that returns table metadata
// (REQ000727). It implements Operator and returns rows matching
// the sqlite_master schema: type, name, tbl_name, rootpage, sql.
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
	names := allTableNames()
	for _, name := range names {
		s.rows = append(s.rows, Row{
			Cols: []string{"type", "name", "tbl_name", "rootpage", "sql"},
			Data: []any{"table", name, name, int64(0), nil},
		})
	}
}

func (s *SqliteMaster) Close() error                { return nil }
func (s *SqliteMaster) WithParams(_ []any) Operator { return s }
