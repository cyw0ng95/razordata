package EX

import (
	"context"
	"testing"
)

func TestREQ001062_ReplaceIntoRowCount(t *testing.T) {
	ResetForTest(t)
	ctx := context.Background()
	ex := NewExecutor()

	// Create a table
	_, err := ex.Exec(ctx, "CREATE TABLE t (id INT PRIMARY KEY, val TEXT)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	// Insert initial row
	_, err = ex.Exec(ctx, "INSERT INTO t VALUES (1, 'a')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	tests := []struct {
		name       string
		sql        string
		wantAffected int64
	}{
		{"REPLACE INTO existing", "REPLACE INTO t VALUES (1, 'b')", 2},
		{"REPLACE INTO new", "REPLACE INTO t VALUES (2, 'c')", 1},
		{"INSERT OR REPLACE existing", "INSERT OR REPLACE INTO t VALUES (1, 'd')", 2},
		{"INSERT OR REPLACE new", "INSERT OR REPLACE INTO t VALUES (3, 'e')", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ex.Exec(ctx, tt.sql)
			if err != nil {
				t.Fatalf("Exec(%q): %v", tt.sql, err)
			}
			if res.RowsAffected != tt.wantAffected {
				t.Errorf("Exec(%q) RowsAffected = %d, want %d", tt.sql, res.RowsAffected, tt.wantAffected)
			}
		})
	}
}