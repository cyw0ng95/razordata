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

func TestREQ001192_Select5FourTableJoin(t *testing.T) {
	dir := t.TempDir()
	opts := AP.Options{
		Dir:     dir + "/engine",
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

	setupTable("t29", [][3]any{
		{1, 4, "table t29 row 1"}, {2, 2, "table t29 row 2"}, {3, 9, "table t29 row 3"},
		{4, 8, "table t29 row 4"}, {5, 10, "table t29 row 5"}, {6, 3, "table t29 row 6"},
		{7, 7, "table t29 row 7"}, {8, 6, "table t29 row 8"}, {9, 5, "table t29 row 9"},
		{10, 1, "table t29 row 10"},
	})
	setupTable("t31", [][3]any{
		{1, 1, "table t31 row 1"}, {2, 6, "table t31 row 2"}, {3, 4, "table t31 row 3"},
		{4, 8, "table t31 row 4"}, {5, 2, "table t31 row 5"}, {6, 9, "table t31 row 6"},
		{7, 7, "table t31 row 7"}, {8, 3, "table t31 row 8"}, {9, 5, "table t31 row 9"},
		{10, 10, "table t31 row 10"},
	})
	setupTable("t51", [][3]any{
		{1, 5, "table t51 row 1"}, {2, 3, "table t51 row 2"}, {3, 10, "table t51 row 3"},
		{4, 7, "table t51 row 4"}, {5, 6, "table t51 row 5"}, {6, 2, "table t51 row 6"},
		{7, 9, "table t51 row 7"}, {8, 4, "table t51 row 8"}, {9, 8, "table t51 row 9"},
		{10, 1, "table t51 row 10"},
	})
	setupTable("t55", [][3]any{
		{1, 1, "table t55 row 1"}, {2, 3, "table t55 row 2"}, {3, 7, "table t55 row 3"},
		{4, 9, "table t55 row 4"}, {5, 5, "table t55 row 5"}, {6, 4, "table t55 row 6"},
		{7, 10, "table t55 row 7"}, {8, 8, "table t55 row 8"}, {9, 6, "table t55 row 9"},
		{10, 2, "table t55 row 10"},
	})

	query := "SELECT x29,x31,x51,x55 FROM t51,t29,t31,t55 WHERE a51=b31 AND a29=6 AND a29=b51 AND b55=a31"

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
	}

	t.Logf("got %d rows", count)
	if count == 0 {
		t.Errorf("expected 1 row, got 0")
	} else if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}