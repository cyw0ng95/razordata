package EX

import (
	"context"
	"testing"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

func TestPhase1_DropTableIfExists(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	
	// DROP TABLE IF EXISTS on non-existent table — should not error
	_, err := ex.Exec(ctx, "DROP TABLE IF EXISTS nonexistent")
	if err != nil {
		t.Errorf("DROP TABLE IF EXISTS nonexistent: %v", err)
	}
	
	// CREATE and DROP
	ex.Exec(ctx, "CREATE TABLE t1 (id INT)")
	_, err = ex.Exec(ctx, "DROP TABLE IF EXISTS t1")
	if err != nil {
		t.Errorf("DROP TABLE IF EXISTS t1: %v", err)
	}
}

func TestPhase1_CreateIndexIfNotExists(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	
	ex.Exec(ctx, "CREATE TABLE t1 (id INT, name TEXT)")
	
	// CREATE INDEX IF NOT EXISTS — first time should succeed
	_, err := ex.Exec(ctx, "CREATE INDEX IF NOT EXISTS idx1 ON t1 (id)")
	if err != nil {
		t.Errorf("CREATE INDEX IF NOT EXISTS: %v", err)
	}
	
	// CREATE INDEX IF NOT EXISTS — second time should not error
	_, err = ex.Exec(ctx, "CREATE INDEX IF NOT EXISTS idx1 ON t1 (id)")
	if err != nil {
		t.Errorf("CREATE INDEX IF NOT EXISTS (dup): %v", err)
	}
	
	// Verify AST parses correctly
	p := PS.NewParser("CREATE INDEX IF NOT EXISTS idx2 ON t1 (name)")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ci := stmt.(*PS.CreateIndexStmt)
	if !ci.IfExists {
		t.Error("expected IfExists=true")
	}
}

func TestPhase1_DropIndexIfExists(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	
	ex.Exec(ctx, "CREATE TABLE t1 (id INT, name TEXT)")
	ex.Exec(ctx, "CREATE INDEX idx1 ON t1 (id)")
	
	// DROP INDEX IF EXISTS — should succeed
	_, err := ex.Exec(ctx, "DROP INDEX IF EXISTS idx1")
	if err != nil {
		t.Errorf("DROP INDEX IF EXISTS: %v", err)
	}
	
	// DROP INDEX IF EXISTS on non-existent — should not error
	_, err = ex.Exec(ctx, "DROP INDEX IF EXISTS nonexistent")
	if err != nil {
		t.Errorf("DROP INDEX IF EXISTS nonexistent: %v", err)
	}
}

func TestPhase1_AlterTableRenameColumn(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	
	ex.Exec(ctx, "CREATE TABLE t1 (id INT, name TEXT)")
	
	// RENAME COLUMN
	_, err := ex.Exec(ctx, "ALTER TABLE t1 RENAME COLUMN name TO fullname")
	if err != nil {
		t.Fatalf("RENAME COLUMN: %v", err)
	}
	
	// Verify schema updated
	schema := Schema("t1")
	found := false
	for _, c := range schema {
		if c == "fullname" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'fullname' in schema, got %v", schema)
	}
	
	// RENAME COLUMN on non-existent column should error
	_, err = ex.Exec(ctx, "ALTER TABLE t1 RENAME COLUMN nonexistent TO x")
	if err == nil {
		t.Error("expected error renaming nonexistent column")
	}
}

func TestPhase1_PragmaNoOp(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	
	// PRAGMA should not error
	_, err := ex.Exec(ctx, "PRAGMA cache_size")
	if err != nil {
		t.Errorf("PRAGMA cache_size: %v", err)
	}
	
	_, err = ex.Exec(ctx, "PRAGMA journal_mode = WAL")
	if err != nil {
		t.Errorf("PRAGMA journal_mode = WAL: %v", err)
	}
}
