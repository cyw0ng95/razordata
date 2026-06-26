package EX

import (
	"context"
	"fmt"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestThreePartName_ColumnRef(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")

	rs, err := ex.QueryAll(ctx, "SELECT main.t.id FROM t")
	if err != nil {
		t.Fatalf("three-part SELECT: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("got %v, want 1", got)
	}
}

func TestThreePartName_FromTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")

	rs, err := ex.QueryAll(ctx, "SELECT id FROM main.t")
	if err != nil {
		t.Fatalf("three-part FROM: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("got %v, want 1", got)
	}
}

func TestThreePartName_CommaJoin(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t1", []string{"a"})
	ex.RegisterTable("t2", []string{"b"})

	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t1 VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t2 VALUES (2)")

	rs, err := ex.QueryAll(ctx, "SELECT a, b FROM main.t1, main.t2")
	if err != nil {
		t.Fatalf("three-part comma join: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rs))
	}
}

func TestPS_ThreePartQualifiedName(t *testing.T) {
	tests := []struct {
		sql   string
		db    string
		table string
		name  string
	}{
		{"SELECT a.b.c FROM t", "a", "b", "c"},
		{"SELECT db.t.col FROM t", "db", "t", "col"},
	}
	for _, tt := range tests {
		parser := PS.NewParser(tt.sql)
		stmt, err := parser.Parse()
		if err != nil {
			t.Fatalf("parse %q: %v", tt.sql, err)
		}
		sel, ok := stmt.(*PS.Select)
		if !ok {
			t.Fatalf("expected Select, got %T", stmt)
		}
		qn, ok := sel.Cols[0].(*PS.QualifiedName)
		if !ok {
			t.Fatalf("expected QualifiedName, got %T", sel.Cols[0])
		}
		if qn.Database != tt.db {
			t.Errorf("Database = %q, want %q", qn.Database, tt.db)
		}
		if qn.Table != tt.table {
			t.Errorf("Table = %q, want %q", qn.Table, tt.table)
		}
		if qn.Name != tt.name {
			t.Errorf("Name = %q, want %q", qn.Name, tt.name)
		}
	}
}
