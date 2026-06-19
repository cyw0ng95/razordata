package SY

import (
	"context"
	"errors"
	executor "github.com/cyw0ng95/razordata/internal/SQL/EX"
	AP "github.com/cyw0ng95/razordata/internal/SYS/AP"
	"path/filepath"
	"testing"
)

func TestQueryStreaming(t *testing.T) {
	executor.UnregisterAll()
	t.Cleanup(executor.UnregisterAll)

	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "stream1.db.razor")
	eng, err := Open(ctx, dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close(ctx)

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

	cols := rows.GetCols()
	if len(cols) != 2 || cols[0] != "id" || cols[1] != "name" {
		t.Fatalf("Cols = %v, want [id name]", cols)
	}

	count := 0
	for {
		row, err := rows.Next()
		if err != nil {
			if errors.Is(err, AP.ErrNoRows) {
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
