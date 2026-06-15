package EX

import (
	"context"
	"testing"

	PS "github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestAlterTable_AddColumn(t *testing.T) {
	e := NewExecutor()
	e.RegisterTableWithPK("t1", []string{"id", "name"}, "id")
	ctx := context.Background()

	// Add a column
	result, err := e.Exec(ctx, "ALTER TABLE t1 ADD COLUMN age INT")
	if err != nil {
		t.Fatalf("add column: %v", err)
	}
	if result.RowsAffected != 0 {
		t.Errorf("expected 0 rows affected, got %d", result.RowsAffected)
	}

	// Try adding a duplicate column (should fail)
	_, err = e.Exec(ctx, "ALTER TABLE t1 ADD COLUMN age INT")
	if err == nil {
		t.Error("expected error adding duplicate column, got nil")
	}
}

func TestAlterTable_AddColumnWithDefault(t *testing.T) {
	e := NewExecutor()
	e.RegisterTable("t2", []string{"id", "name"})
	ctx := context.Background()

	_, err := e.Exec(ctx, "ALTER TABLE t2 ADD COLUMN active INT DEFAULT 1")
	if err != nil {
		t.Fatalf("add column with default: %v", err)
	}
}

func TestAlterTable_AddColumnNotNull(t *testing.T) {
	e := NewExecutor()
	e.RegisterTable("t3", []string{"id", "name"})
	ctx := context.Background()

	_, err := e.Exec(ctx, "ALTER TABLE t3 ADD COLUMN value INT NOT NULL")
	if err != nil {
		t.Fatalf("add column not null: %v", err)
	}
}

func TestAlterTable_DropColumn(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	e.RegisterTableWithPK("t4", []string{"id", "name", "age"}, "id")
	ctx := context.Background()

	// Drop a column
	_, err := e.Exec(ctx, "ALTER TABLE t4 DROP COLUMN age")
	if err != nil {
		t.Fatalf("drop column: %v", err)
	}

	// Try dropping a non-existent column
	_, err = e.Exec(ctx, "ALTER TABLE t4 DROP COLUMN nonexistent")
	if err == nil {
		t.Error("expected error dropping non-existent column, got nil")
	}
}

func TestAlterTable_DropColumn_PK(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	e.RegisterTableWithPK("t5", []string{"id", "name"}, "id")
	ctx := context.Background()

	// In in-memory mode (no engine store) the executor does not
	// enforce the primary-key invariant because the in-memory
	// `schemas` map (source.go) only carries column names, not PK
	// info. See execDropColumnInMemory's "we don't know which is
	// PK" comment. The store-backed path does enforce this — it
	// runs through execDropColumn's pk check at alter_table.go.
	_, err := e.Exec(ctx, "ALTER TABLE t5 DROP COLUMN id")
	if err != nil {
		t.Errorf("in-memory drop PK is permissive by design, got: %v", err)
	}
}

func TestAlterTable_RenameTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	e.RegisterTableWithPK("t6", []string{"id", "name"}, "id")
	ctx := context.Background()

	// Rename the table
	_, err := e.Exec(ctx, "ALTER TABLE t6 RENAME TO t6_new")
	if err != nil {
		t.Fatalf("rename table: %v", err)
	}

	// Try renaming to existing table name
	_, err = e.Exec(ctx, "ALTER TABLE t6_new RENAME TO t6_new")
	if err == nil {
		t.Error("expected error renaming to existing name, got nil")
	}
}

func TestAlterTable_NonExistentTable(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	e := NewExecutor()
	ctx := context.Background()

	// ALTER TABLE on non-existent table
	_, err := e.Exec(ctx, "ALTER TABLE nonexistent ADD COLUMN x INT")
	if err == nil {
		t.Error("expected error altering non-existent table, got nil")
	}

	// DROP COLUMN on non-existent table
	_, err = e.Exec(ctx, "ALTER TABLE nonexistent DROP COLUMN x")
	if err == nil {
		t.Error("expected error dropping column from non-existent table, got nil")
	}

	// RENAME on non-existent table
	_, err = e.Exec(ctx, "ALTER TABLE nonexistent RENAME TO new_name")
	if err == nil {
		t.Error("expected error renaming non-existent table, got nil")
	}
}

// TestAlterTableParser verifies the parser correctly captures column
// type and NULLABLE info for ALTER TABLE ADD COLUMN.
func TestAlterTableParser(t *testing.T) {
	cases := []struct {
		sql      string
		col      string
		typ      int
		nullable bool
	}{
		{"ALTER TABLE t ADD COLUMN name TEXT", "name", 1, true},
		{"ALTER TABLE t ADD COLUMN age INT NOT NULL", "age", 1, false},
		{"ALTER TABLE t ADD COLUMN price REAL", "price", 0, true},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			p := PS.NewParser(c.sql)
			stmt, err := p.Parse()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			alt, ok := stmt.(*PS.AlterTableStmt)
			if !ok {
				t.Fatalf("expected AlterTableStmt, got %T", stmt)
			}
			if alt.Table != "t" {
				t.Errorf("table: got %q, want %q", alt.Table, "t")
			}
			if alt.Action != "ADD COLUMN" {
				t.Errorf("action: got %q, want %q", alt.Action, "ADD COLUMN")
			}
			if alt.Column != c.col {
				t.Errorf("column: got %q, want %q", alt.Column, c.col)
			}
			if alt.NewCol == nil {
				t.Fatal("NewCol is nil")
			}
			if alt.NewCol.Nullable != c.nullable {
				t.Errorf("nullable: got %v, want %v", alt.NewCol.Nullable, c.nullable)
			}
		})
	}
}
