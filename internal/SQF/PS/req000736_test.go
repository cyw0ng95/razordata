package PS

import (
	"testing"
)

func TestREQ000736_NullsFirstLast(t *testing.T) {
	tests := []struct {
		sql       string
		wantOrder int8
	}{
		{"SELECT * FROM t ORDER BY col NULLS FIRST", 1},
		{"SELECT * FROM t ORDER BY col NULLS LAST", -1},
		{"SELECT * FROM t ORDER BY col ASC NULLS FIRST", 1},
		{"SELECT * FROM t ORDER BY col DESC NULLS LAST", -1},
		{"SELECT * FROM t ORDER BY col ASC", 0},
		{"SELECT * FROM t ORDER BY col", 0},
		{"SELECT * FROM t ORDER BY col ASC NULLS LAST", -1},
	}
	for _, tc := range tests {
		p := NewParser(tc.sql)
		stmt, err := p.Parse()
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.sql, err)
			continue
		}
		sel, ok := stmt.(*Select)
		if !ok {
			t.Errorf("%q: not a Select", tc.sql)
			continue
		}
		if len(sel.OrderBy) != 1 {
			t.Errorf("%q: expected 1 OrderBy, got %d", tc.sql, len(sel.OrderBy))
			continue
		}
		if sel.OrderBy[0].NullsOrder != tc.wantOrder {
			t.Errorf("%q: NullsOrder=%d, want %d", tc.sql, sel.OrderBy[0].NullsOrder, tc.wantOrder)
		}
	}
}
