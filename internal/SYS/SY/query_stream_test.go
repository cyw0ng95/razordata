package SY

import (
	"context"
	"testing"

	executor "github.com/cyw0ng95/razordata/internal/SQB/EX"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

func TestQueryStreaming(t *testing.T) {
	executor.UnregisterAll()
	t.Cleanup(executor.UnregisterAll)

	initSharedEngine(t)
	resetSharedEngine(t)
	eng := sharedEng
	ctx := context.Background()

	sess, err := eng.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, err = sess.Exec(ctx, "CREATE TABLE qs1 (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	inserts := []string{
		"INSERT INTO qs1 VALUES (1, 'alice'), (2, 'bob'), (3, 'carol')",
		"INSERT INTO qs1 VALUES (4, 'dave'), (5, 'eve'), (6, 'frank')",
		"INSERT INTO qs1 VALUES (7, 'grace'), (8, 'henry'), (9, 'ivy')",
		"INSERT INTO qs1 VALUES (10, 'jack'), (11, 'kim'), (12, 'leo')",
		"INSERT INTO qs1 VALUES (13, 'mike'), (14, 'nora'), (15, 'oscar')",
	}
	for _, sql := range inserts {
		_, err = sess.Exec(ctx, sql)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	rows, err := sess.Query(ctx, "SELECT id, name FROM qs1 ORDER BY id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	cols := rows.Cols()
	if len(cols) != 2 || cols[0] != "id" || cols[1] != "name" {
		t.Fatalf("Cols = %v, want [id name]", cols)
	}

	count := 0
	for {
		row, err := rows.Next()
		if err != nil {
			if AP.IsKind(err, AP.KindNotFound) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		if len(row.Data) != 2 {
			t.Fatalf("Row[%d] data len = %d, want 2", count, len(row.Data))
		}
		count++
	}
	if count != 15 {
		t.Fatalf("Got %d rows, want 15", count)
	}
}
