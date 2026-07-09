package DS

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/cyw0ng95/razordata/internal/SYS/TX" // REQ001428: register tx constructor
)

func TestDS_OpenCloseOnly(t *testing.T) {
	db, err := sql.Open("razor", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
}

func TestDS_InMemoryBasic(t *testing.T) {
	db, err := sql.Open("razor", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	res, err := db.Exec("INSERT INTO t VALUES (1, 'hello'), (2, 'world')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if n != 2 {
		t.Fatalf("RowsAffected = %d, want 2", n)
	}

	rows, err := db.Query("SELECT id, val FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	got := 0
	for rows.Next() {
		var id int64
		var val string
		if err := rows.Scan(&id, &val); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if got != 2 {
		t.Fatalf("got %d rows, want 2", got)
	}
}

func TestDS_FileMode(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "test.razor")

	db1, err := sql.Open("razor", dsn)
	if err != nil {
		t.Fatalf("sql.Open #1: %v", err)
	}
	if _, err := db1.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, val INTEGER)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	if _, err := db1.Exec("INSERT INTO t VALUES (10, 100), (20, 200)"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}

	db2, err := sql.Open("razor", dsn)
	if err != nil {
		t.Fatalf("sql.Open #2: %v", err)
	}
	defer db2.Close()

	var n int64
	if err := db2.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 2 {
		t.Fatalf("after reopen, count = %d, want 2", n)
	}
}

func TestDS_Transaction(t *testing.T) {
	db, err := sql.Open("razor", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO t VALUES (1, 'committed')"); err != nil {
		t.Fatalf("INSERT in tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	var n int64
	if err := db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("Count after commit: %v", err)
	}
	if n != 1 {
		t.Fatalf("after commit count = %d, want 1", n)
	}

	tx, err = db.Begin()
	if err != nil {
		t.Fatalf("Begin #2: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO t VALUES (2, 'rolled back')"); err != nil {
		t.Fatalf("INSERT in tx #2: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("Count after rollback: %v", err)
	}
	if n != 1 {
		t.Fatalf("after rollback count = %d, want 1 (unchanged)", n)
	}
}

func TestDS_ParameterizedQuery(t *testing.T) {
	db, err := sql.Open("razor", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)"); err != nil {
		t.Fatalf("CREATE: %v", err)
	}

	stmt, err := db.Prepare("INSERT INTO t VALUES (?, ?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer stmt.Close()

	for i := 1; i <= 3; i++ {
		if _, err := stmt.Exec(int64(i), "x"); err != nil {
			t.Fatalf("Exec %d: %v", i, err)
		}
	}

	var n int64
	if err := db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}
}

func TestDS_DSNParse(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{":memory:", ":memory:"},
		{"", ":memory:"},
		{"  :memory:  ", ":memory:"},
		{"./test.db", "./test.db"},
	}
	for _, tc := range cases {
		got := parseDSN(tc.in)
		if got.Path != tc.want {
			t.Errorf("parseDSN(%q).Path = %q, want %q", tc.in, got.Path, tc.want)
		}
	}
}
