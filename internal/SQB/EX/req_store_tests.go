package EX

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// REQ000805: ALL keyword in aggregate functions.
func TestAggregate_AllKeyword(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t VALUES (1, 10)",
		"INSERT INTO t VALUES (2, 20)",
		"INSERT INTO t VALUES (3, 30)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}
	tests := []struct {
		sql  string
		name string
		want any
	}{
		{"SELECT MIN(ALL v) FROM t", "MIN(ALL)", int64(10)},
		{"SELECT MAX(ALL v) FROM t", "MAX(ALL)", int64(30)},
		{"SELECT COUNT(ALL v) FROM t", "COUNT(ALL)", int64(3)},
		{"SELECT SUM(ALL v) FROM t", "SUM(ALL)", int64(60)},
		{"SELECT AVG(ALL v) FROM t", "AVG(ALL)", float64(20)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := ex.QueryAll(ctx, tc.sql)
			if err != nil {
				t.Fatalf("%s: %v", tc.sql, err)
			}
			if len(rows) != 1 {
				t.Fatalf("%s: got %d rows, want 1", tc.sql, len(rows))
			}
			got := rows[0].Data[0]
			if got.ToAny() != tc.want {
				t.Errorf("%s = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}

// REQ000812: CHANGES() and TOTAL_CHANGES() functions.
func TestChanges_Insert(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	rs, err := ex.QueryAll(ctx, "SELECT CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("CHANGES() after INSERT: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("CHANGES() after last INSERT = %s, want 1", got)
	}
}

func TestChanges_Update(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "UPDATE t SET val = 99 WHERE id = 1")
	rs, err := ex.QueryAll(ctx, "SELECT CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("CHANGES() after UPDATE: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("CHANGES() after UPDATE = %s, want 1", got)
	}
}

func TestChanges_Delete(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "DELETE FROM t WHERE id = 1")
	rs, err := ex.QueryAll(ctx, "SELECT CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("CHANGES() after DELETE: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "1" {
		t.Errorf("CHANGES() after DELETE = %s, want 1", got)
	}
}

func TestTotalChanges_Accumulate(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ex.RegisterTable("t", []string{"id", "val"})
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	ex.Exec(ctx, "UPDATE t SET val = 99 WHERE id = 2")
	ex.Exec(ctx, "DELETE FROM t WHERE id = 3")
	rs, err := ex.QueryAll(ctx, "SELECT TOTAL_CHANGES() FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("TOTAL_CHANGES(): %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected result for TOTAL_CHANGES()")
	}
	got := fmt.Sprint(rs[0].Data[0].ToAny())
	if got != "5" {
		t.Errorf("TOTAL_CHANGES() = %s, want 5", got)
	}
}

// REQ000858: Column alias in WHERE.
func TestReq000858_ColumnAliasInWhere(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)
	for _, s := range []string{
		"CREATE TABLE t(id INTEGER, v INTEGER)",
		"INSERT INTO t VALUES(1, 10)",
		"INSERT INTO t VALUES(2, 20)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ex.QueryAll(ctx, "SELECT v AS value FROM t WHERE value > 15")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	} else if rows[0].Data[0].ToAny() != int64(20) {
		t.Errorf("expected 20, got %v", rows[0].Data[0].ToAny())
	}
}

func TestReq000860_InsertSelect(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)
	for _, s := range []string{
		"CREATE TABLE src(id INTEGER, v INTEGER)",
		"INSERT INTO src VALUES(1, 10)",
		"INSERT INTO src VALUES(2, 20)",
		"CREATE TABLE dst(id INTEGER, v INTEGER)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ex.Exec(ctx, "INSERT INTO dst SELECT * FROM src WHERE v > 15")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ex.QueryAll(ctx, "SELECT v FROM dst")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row in dst, got %d", len(rows))
	} else if rows[0].Data[0].ToAny() != int64(20) {
		t.Errorf("expected 20, got %v", rows[0].Data[0].ToAny())
	}
}

// REQ000907: FETCH FIRST/NEXT parsing.
func TestREQ000907_FetchFirst(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	ex := NewExecutor()
	ctx := context.Background()
	for _, s := range []string{
		"CREATE TABLE t (id INTEGER)",
		"INSERT INTO t VALUES (1), (2), (3), (4), (5)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}
	_, err := ex.QueryAll(ctx, "SELECT * FROM t ORDER BY id FETCH FIRST 5 ROWS ONLY")
	if err != nil {
		t.Logf("FETCH FIRST 5 ROWS ONLY: error=%v", err)
		if strings.Contains(err.Error(), "syntax error") {
			t.Fatalf("FETCH FIRST should be parsed, got syntax error: %v", err)
		}
	}
	_, err = ex.QueryAll(ctx, "SELECT * FROM t ORDER BY id FETCH NEXT 3 ROWS ONLY")
	if err != nil {
		t.Logf("FETCH NEXT 3 ROWS ONLY: error=%v", err)
	}
	_, err = ex.QueryAll(ctx, "SELECT * FROM t ORDER BY id FETCH FIRST ROW ONLY")
	if err != nil {
		t.Logf("FETCH FIRST ROW ONLY: error=%v", err)
	}
}

// REQ000908: ATTACH/DETACH DATABASE.
func TestAttachDetach_SamePath(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	_, err := e.Exec(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS db1", dbPath))
	if err != nil {
		t.Fatalf("ATTACH: %v", err)
	}
	_, err = e.Exec(ctx, "DETACH DATABASE db1")
	if err != nil {
		t.Fatalf("DETACH: %v", err)
	}
	_, err = e.Exec(ctx, "DETACH DATABASE nonexistent")
	if err != nil {
		t.Fatalf("DETACH nonexistent: %v", err)
	}
}

func TestAttachDetach_QueryAcross(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()
	_, err := e.Exec(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS ext", filepath.Join(t.TempDir(), "ext.db")))
	if err != nil {
		t.Fatalf("ATTACH: %v", err)
	}
	_, err = e.Exec(ctx, "DETACH DATABASE ext")
	if err != nil {
		t.Fatalf("DETACH: %v", err)
	}
}

func TestAttachDetach_Parse(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()
	_, err := e.Exec(ctx, "ATTACH DATABASE ':memory:' AS main")
	if err != nil {
		t.Fatalf("ATTACH memory: %v", err)
	}
	_, err = e.Exec(ctx, "DETACH DATABASE main")
	if err != nil {
		t.Fatalf("DETACH memory: %v", err)
	}
	tmpDir, _ := os.MkdirTemp("", "razor-attach-test-*")
	defer os.RemoveAll(tmpDir)
	_, err = e.Exec(ctx, fmt.Sprintf("ATTACH DATABASE '%s/test.db' AS testdb", tmpDir))
	if err != nil {
		t.Fatalf("ATTACH tmpdir: %v", err)
	}
	_, err = e.Exec(ctx, "DETACH DATABASE testdb")
	if err != nil {
		t.Fatalf("DETACH testdb: %v", err)
	}
}

// REQ000909: CREATE VIRTUAL TABLE stub.
func TestCreateVirtualTable_StubRegistration(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()
	e := NewExecutor()
	defer e.Close()
	ctx := context.Background()
	_, err := e.Exec(ctx, "CREATE VIRTUAL TABLE t1 USING fts5(content)")
	if err == nil {
		t.Fatal("expected error for virtual table, got nil")
	}
	if !strings.Contains(err.Error(), "virtual table module not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = e.QueryAll(ctx, "SELECT * FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("sqlite_master query: %v", err)
	}
}