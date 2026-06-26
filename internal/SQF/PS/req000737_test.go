package PS

import (
	"testing"
)

func TestREQ000737_CreateVirtualTable(t *testing.T) {
	tests := []struct {
		sql        string
		wantName   string
		wantModule string
		wantArgs   int
	}{
		{"CREATE VIRTUAL TABLE t USING fts5(content)", "t", "fts5", 1},
		{"CREATE VIRTUAL TABLE search USING fts5(content, title)", "search", "fts5", 2},
		{"CREATE VIRTUAL TABLE geo USING rtree(id, x1, x2, y1, y2)", "geo", "rtree", 5},
	}
	for _, tc := range tests {
		p := NewParser(tc.sql)
		stmt, err := p.Parse()
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.sql, err)
			continue
		}
		v, ok := stmt.(*CreateVirtualTableStmt)
		if !ok {
			t.Errorf("%q: got %T, want *CreateVirtualTableStmt", tc.sql, stmt)
			continue
		}
		if v.Name != tc.wantName {
			t.Errorf("%q: Name=%q, want %q", tc.sql, v.Name, tc.wantName)
		}
		if v.Module != tc.wantModule {
			t.Errorf("%q: Module=%q, want %q", tc.sql, v.Module, tc.wantModule)
		}
		if len(v.Args) != tc.wantArgs {
			t.Errorf("%q: %d args, want %d: %v", tc.sql, len(v.Args), tc.wantArgs, v.Args)
		}
	}
}
