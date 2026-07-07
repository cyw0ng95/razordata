package OP

import (
	"context"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
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
	// REQ001388: report tables, views, indexes, and triggers.
	names := DT.AllTableNames()
	for _, name := range names {
		s.rows = append(s.rows, Row{
			Cols: []string{"type", "name", "tbl_name", "rootpage", "sql"},
			Data: []Value{DT.NewTextValue("table"), DT.NewTextValue(name), DT.NewTextValue(name), DT.NewIntValue(0), DT.NullValue()},
		})
	}
	// Views
	DT.ViewMu.RLock()
	viewNames := make([]string, 0, len(DT.ViewRegistry))
	for name := range DT.ViewRegistry {
		viewNames = append(viewNames, name)
	}
	DT.ViewMu.RUnlock()
	for _, name := range viewNames {
		s.rows = append(s.rows, Row{
			Cols: []string{"type", "name", "tbl_name", "rootpage", "sql"},
			Data: []Value{DT.NewTextValue("view"), DT.NewTextValue(name), DT.NewTextValue(name), DT.NewIntValue(0), DT.NullValue()},
		})
	}
	// Indexes (one row per index per table)
	DT.StoreMu.Lock()
	for table, idxs := range DT.RegisteredIndexes {
		for _, idx := range idxs {
			s.rows = append(s.rows, Row{
				Cols: []string{"type", "name", "tbl_name", "rootpage", "sql"},
				Data: []Value{DT.NewTextValue("index"), DT.NewTextValue(idx.Name), DT.NewTextValue(table), DT.NewIntValue(0), DT.NullValue()},
			})
		}
	}
	DT.StoreMu.Unlock()
	// Triggers
	triggers := DT.AllTriggers()
	for _, t := range triggers {
		s.rows = append(s.rows, Row{
			Cols: []string{"type", "name", "tbl_name", "rootpage", "sql"},
			Data: []Value{DT.NewTextValue("trigger"), DT.NewTextValue(t.Name), DT.NewTextValue(t.OnTable), DT.NewIntValue(0), DT.NullValue()},
		})
	}
}

func (s *SqliteMaster) Close() error                  { return nil }
func (s *SqliteMaster) WithParams(_ []any) pl.Operator { return s }
