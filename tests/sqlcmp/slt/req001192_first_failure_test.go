//go:build slt_corpus

package slt

import (
    "context"
    "database/sql"
    "fmt"
    "testing"

    "github.com/cyw0ng95/razordata/driver"
    "github.com/cyw0ng95/razordata/internal/SYS/AP"
    v1 "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

func TestREQ001192_Select5FirstFailure(t *testing.T) {
    dir := t.TempDir()
	opts := AP.Options{
		Dir:              dir + "/engine",
		MemTableSize:     1 << 20,
		BufferPoolMB:     64,
		MaxMemoryPerQuery: 512 << 20,
		JoinBufferSize:    256 << 20,
		MaxResultRows:     100_000,
	}

    eng, err := v1.Open(context.Background(), dir, opts)
    if err != nil {
        t.Fatalf("open: %v", err)
    }
    defer eng.Close(context.Background())

    dsn := dir + "/db.razor"
    driver.RegisterEngine(dsn, eng)
    defer driver.CloseEngine(dsn)

    db, err := sql.Open("razor", dsn)
    if err != nil {
        t.Fatal(err)
    }
    defer db.Close()
    db.SetMaxOpenConns(1)

    ctx := context.Background()

    setupTable := func(name string, rows [][3]any) {
        stmt := fmt.Sprintf("CREATE TABLE %s (a%s INTEGER PRIMARY KEY, b%s INTEGER, x%s VARCHAR(40))", name, name[1:], name[1:], name[1:])
        if _, err := db.ExecContext(ctx, stmt); err != nil {
            t.Fatalf("CREATE %s: %v", name, err)
        }
        for _, r := range rows {
			stmt = fmt.Sprintf("INSERT INTO %s VALUES(%v,%v,'%s')", name, r[0], r[1], r[2])
            if _, err := db.ExecContext(ctx, stmt); err != nil {
                t.Fatalf("INSERT %s: %v", name, err)
            }
        }
    }

    setupTable("t41", [][3]any{
        {1, 6, "table t41 row 1"}, {2, 1, "table t41 row 2"}, {3, 3, "table t41 row 3"},
        {4, 2, "table t41 row 4"}, {5, 9, "table t41 row 5"}, {6, 5, "table t41 row 6"},
        {7, 8, "table t41 row 7"}, {8, 7, "table t41 row 8"}, {9, 10, "table t41 row 9"},
        {10, 4, "table t41 row 10"},
    })
    setupTable("t54", [][3]any{
        {1, 6, "table t54 row 1"}, {2, 7, "table t54 row 2"}, {3, 2, "table t54 row 3"},
        {4, 9, "table t54 row 4"}, {5, 8, "table t54 row 5"}, {6, 5, "table t54 row 6"},
        {7, 4, "table t54 row 7"}, {8, 1, "table t54 row 8"}, {9, 10, "table t54 row 9"},
        {10, 3, "table t54 row 10"},
    })
    setupTable("t27", [][3]any{
        {1, 8, "table t27 row 1"}, {2, 3, "table t27 row 2"}, {3, 6, "table t27 row 3"},
        {4, 7, "table t27 row 4"}, {5, 4, "table t27 row 5"}, {6, 2, "table t27 row 6"},
        {7, 10, "table t27 row 7"}, {8, 9, "table t27 row 8"}, {9, 5, "table t27 row 9"},
        {10, 1, "table t27 row 10"},
    })
    setupTable("t32", [][3]any{
        {1, 10, "table t32 row 1"}, {2, 2, "table t32 row 2"}, {3, 6, "table t32 row 3"},
        {4, 3, "table t32 row 4"}, {5, 4, "table t32 row 5"}, {6, 1, "table t32 row 6"},
        {7, 7, "table t32 row 7"}, {8, 5, "table t32 row 8"}, {9, 9, "table t32 row 9"},
        {10, 8, "table t32 row 10"},
    })

    query := "SELECT x27,x41,x54,x32 FROM t41,t54,t27,t32 WHERE a54=b41 AND b27=a32 AND b32=a41 AND a54=2"

    rows, err := db.QueryContext(ctx, query)
    if err != nil {
        t.Fatalf("query: %v", err)
    }
    defer rows.Close()

	var count int
	var vals []any
	for rows.Next() {
		var x27, x41, x54, x32 string
		if err := rows.Scan(&x27, &x41, &x54, &x32); err != nil {
			t.Fatalf("scan: %v", err)
		}
		vals = append(vals, x27, x41, x54, x32)
		count++
	}
	t.Logf("got %d rows, vals: %v", count, vals)
    if count == 0 {
        t.Errorf("expected 1 row, got 0")
    } else if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
    }
}
