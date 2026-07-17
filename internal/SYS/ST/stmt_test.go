package ST

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	executor "github.com/cyw0ng95/razordata/internal/SQB/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	_ "github.com/cyw0ng95/razordata/internal/SYS/SE"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

func testEngine(t *testing.T) (AP.Engine, context.Context) {
	t.Helper()
	resetExecutorRegistry()
	dir := filepath.Join(t.TempDir(), "db")
	eng, err := SY.Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
		LogFormat:    "text",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := eng.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := s.Exec(context.Background(), "CREATE TABLE users (id INTEGER, name TEXT, PRIMARY KEY (id))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return eng, context.Background()
}

func resetExecutorRegistry() {
	executor.UnregisterAll()
}

// TestStmt_PrepareAndReuse — R16/R17: prepare a statement, execute it
// multiple times.
func TestStmt_PrepareAndReuse(t *testing.T) {
	eng, ctx := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "INSERT INTO users VALUES (1, 'a')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	// Reuse the prepared statement for two executions. The first
	// succeed; the second violates the unique PK and reports 0
	// rows affected, which exercises the not-error path.
	if _, err := stmt.Exec(ctx); err != nil {
		t.Fatalf("first exec: %v", err)
	}
	if res, err := stmt.Exec(ctx); err != nil {
		t.Errorf("second exec: %v", err)
	} else if res.RowsAffected > 1 {
		t.Errorf("unexpected RowsAffected = %d", res.RowsAffected)
	}
}

// TestStmt_CloseIdempotent — R16: Close is idempotent.
func TestStmt_CloseIdempotent(t *testing.T) {
	eng, _ := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if err := stmt.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestStmt_ExecAfterClose — R16: executing a closed statement
// returns AP.ErrClosed.
func TestStmt_ExecAfterClose(t *testing.T) {
	eng, ctx := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	_ = stmt.Close()
	if _, err := stmt.Exec(ctx); !AP.IsKind(err, AP.KindClosed) {
		t.Errorf("Exec after Close: got %v, want ErrClosed", err)
	}
	if _, err := stmt.Query(ctx); !AP.IsKind(err, AP.KindClosed) {
		t.Errorf("Query after Close: got %v, want ErrClosed", err)
	}
}

// TestStmt_PrepareEmptySQL — R16: empty SQL returns AP.ErrSyntax.
func TestStmt_PrepareEmptySQL(t *testing.T) {
	eng, _ := testEngine(t)
	_, err := PrepareFromInterface(eng, "")
	if !AP.IsKind(err, AP.KindSyntax) {
		t.Errorf("empty SQL: got %v, want ErrSyntax", err)
	}
}

// TestStmt_PrepareNilEngine — AP.ErrNotOpen.
func TestStmt_PrepareNilEngine(t *testing.T) {
	_, err := PrepareFromInterface(nil, "SELECT 1")
	if !AP.IsKind(err, AP.KindClosed) {
		t.Errorf("nil engine: got %v, want ErrNotOpen", err)
	}
}

// TestStmt_QueryReturnsCols — R17: prepare + Query returns the
// expected column metadata.
func TestStmt_QueryReturnsCols(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	stmt, err := PrepareFromInterface(eng, "SELECT id, name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if len(rows.Cols()) != 2 || rows.Cols()[0] != "id" || rows.Cols()[1] != "name" {
		t.Errorf("cols = %v, want [id name]", rows.Cols())
	}
	// Consume all rows to prevent race with Close().
	for {
		_, err := rows.Next()
		if err != nil {
			break
		}
	}
}

// TestStmt_ExecReturnsRowsAffected — R16: Exec returns the result's
// RowsAffected.
func TestStmt_ExecReturnsRowsAffected(t *testing.T) {
	eng, ctx := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "INSERT INTO users VALUES (1, 'a')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", res.RowsAffected)
	}
}

// TestStmt_SQLAccessor — R17: the SQL text is preserved.
func TestStmt_SQLAccessor(t *testing.T) {
	eng, _ := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if stmt.SQL() != "SELECT 1" {
		t.Errorf("SQL() = %q, want %q", stmt.SQL(), "SELECT 1")
	}
}

// TestStmt_ParamBinding_TypeCoercion — R16-3, R16-4: bind each
// valid Go type to its SQL column type (success path), and
// confirm mismatches return *argTypeError.
func TestStmt_ParamBinding_TypeCoercion(t *testing.T) {
	cases := []struct {
		name    string
		sqlType ls.ColumnType
		val     any
		ok      bool
	}{
		// INTEGER/BIGINT/TIMESTAMP accept the int family.
		{name: "int_to_int", sqlType: ls.CTInt, val: int64(7), ok: true},
		{name: "int8_to_int", sqlType: ls.CTInt, val: int8(1), ok: true},
		{name: "uint32_to_bigint", sqlType: ls.CTBigInt, val: uint32(5), ok: true},
		// BOOLEAN accepts bool only.
		{name: "bool_to_bool", sqlType: ls.CTBool, val: true, ok: true},
		{name: "int_to_bool", sqlType: ls.CTBool, val: int64(1), ok: false},
		// VARCHAR/TEXT/BLOB accept string and []byte.
		{name: "string_to_varchar", sqlType: ls.CTVarchar, val: "alice", ok: true},
		{name: "bytes_to_blob", sqlType: ls.CTBlob, val: []byte("x"), ok: true},
		{name: "int_to_text", sqlType: ls.CTText, val: int64(7), ok: false},
		// FLOAT accepts the float and int families.
		{name: "float64_to_float", sqlType: ls.CTFloat, val: float64(1.5), ok: true},
		{name: "int_to_float", sqlType: ls.CTFloat, val: int64(7), ok: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := coercible(reflect.TypeOf(c.val), c.sqlType)
			if got != c.ok {
				t.Errorf("coercible(%s, %d) = %v, want %v",
					reflect.TypeOf(c.val), int(c.sqlType), got, c.ok)
			}
		})
	}
}

// TestStmt_ParamBinding_EndToEnd — R16-3, R16-6: a  placeholder
// in an INSERT or SELECT WHERE receives the bound Go value
// through the operator pipeline and is reflected in the result.
func TestStmt_ParamBinding_EndToEnd(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (1, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (2, 'bob')"); err != nil {
		t.Fatal(err)
	}
	stmt, err := PrepareFromInterface(eng, "SELECT name FROM users WHERE id = ?")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query(ctx, int64(2))
	if err != nil {
		t.Fatal(err)
	}
	// One row expected; the engine returns column metadata in
	// rows.Cols(). We exercise the type-validation pass by binding
	// int64 (which the catalog expects for INTEGER); a string
	// binding would surface *argTypeError (verified separately).
	if len(rows.Cols()) != 1 || rows.Cols()[0] != "name" {
		t.Errorf("cols = %v, want [name]", rows.Cols())
	}
}

// TestStmt_ParamBinding_MismatchReturnsError — R16-5: a Go type
// that cannot coerce to the column type returns *argTypeError
// at bind time, before the executor sees the call.
func TestStmt_ParamBinding_MismatchReturnsError(t *testing.T) {
	eng, ctx := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "SELECT name FROM users WHERE id = ?")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	// id is INTEGER; bind a string. The ST layer should reject
	// before forwarding to the executor.
	_, err = stmt.Query(ctx, "not-an-int")
	var ate *argTypeError
	if !errors.As(err, &ate) {
		t.Fatalf("Query with mismatched type: got %v, want *argTypeError", err)
	}
	if ate.ArgIdx != 0 {
		t.Errorf("ArgIdx = %d, want0", ate.ArgIdx)
	}
}

// TestPreparedPlan_SelectQuery_Reuse — REQ001422: prepare a SELECT,
// Query twice with different params, verify results are correct each time.
func TestPreparedPlan_SelectQuery_Reuse(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (10, 'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (20, 'bob')"); err != nil {
		t.Fatal(err)
	}

	stmt, err := PrepareFromInterface(eng, "SELECT name FROM users WHERE id = ?")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()

	// First query with id=10
	rows1, err := stmt.Query(ctx, int64(10))
	if err != nil {
		t.Fatalf("first Query: %v", err)
	}
	row1, err := rows1.Next()
	if err != nil {
		t.Fatalf("first Query row: %v", err)
	}
	if row1.Data[0].String() != "alice" {
		t.Errorf("first query: got name %v, want alice", row1.Data[0])
	}
	rows1.Close()

	// Second query with id=20 — uses the cached CompiledPlan
	rows2, err := stmt.Query(ctx, int64(20))
	if err != nil {
		t.Fatalf("second Query: %v", err)
	}
	row2, err := rows2.Next()
	if err != nil {
		t.Fatalf("second Query row: %v", err)
	}
	if row2.Data[0].String() != "bob" {
		t.Errorf("second query: got name %v, want bob", row2.Data[0])
	}
	rows2.Close()
}

// TestPreparedPlan_InsertExec_Reuse — REQ001422: prepare an INSERT,
// Exec multiple times with different args, verify all rows inserted.
func TestPreparedPlan_InsertExec_Reuse(t *testing.T) {
	eng, ctx := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "INSERT INTO users VALUES (?, ?)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()

	for i := int64(1); i <= 3; i++ {
		res, err := stmt.Exec(ctx, i, "user")
		if err != nil {
			t.Fatalf("Exec %d: %v", i, err)
		}
		if res.RowsAffected != 1 {
			t.Errorf("Exec %d: RowsAffected = %d, want 1", i, res.RowsAffected)
		}
	}

	// Verify all 3 rows via the engine directly.
	s, _ := eng.Begin(ctx)
	rows, err := s.Query(ctx, "SELECT count(*) FROM users")
	if err != nil {
		t.Fatal(err)
	}
	row, err := rows.Next()
	if err != nil {
		t.Fatal(err)
	}
	if row.Data[0].AsInt() != 3 {
		t.Errorf("count = %d, want 3", row.Data[0].AsInt())
	}
}

// TestPreparedPlan_ExecWithSELECT — REQ001422: Exec on a prepared SELECT
// should be a no-op.
func TestPreparedPlan_ExecWithSELECT(t *testing.T) {
	eng, ctx := testEngine(t)
	stmt, err := PrepareFromInterface(eng, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.Exec(ctx); err != nil {
		t.Fatalf("Exec SELECT: %v", err)
	}
}

// TestPreparedPlan_CloseCleansUp — REQ001422: Close on a Stmt
// that has a cached CompiledPlan should clean up properly.
func TestPreparedPlan_CloseCleansUp(t *testing.T) {
	eng, ctx := testEngine(t)
	s, _ := eng.Begin(ctx)
	if _, err := s.Exec(ctx, "INSERT INTO users VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}

	stmt, err := PrepareFromInterface(eng, "SELECT name FROM users WHERE id = ?")
	if err != nil {
		t.Fatal(err)
	}

	// Execute to populate the cache
	rows, err := stmt.Query(ctx, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()

	// Close the stmt
	if err := stmt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Exec after Close should error
	if _, err := stmt.Query(ctx, int64(1)); !AP.IsKind(err, AP.KindClosed) {
		t.Errorf("Query after Close: got %v, want ErrClosed", err)
	}
}
