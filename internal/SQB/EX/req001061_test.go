package EX

import (
	"context"
	"testing"
)

func TestREQ001061_DMLOnView(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutor()

	// Create a table
	_, err := ex.Exec(ctx, "CREATE TABLE t (id INT, val TEXT)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	// Create a non-updatable view (with aggregation)
	_, err = ex.Exec(ctx, "CREATE VIEW v_agg AS SELECT count(*) FROM t")
	if err != nil {
		t.Fatalf("CREATE VIEW (agg): %v", err)
	}

	// Create an updatable view (simple single-table)
	_, err = ex.Exec(ctx, "CREATE VIEW v_simple AS SELECT * FROM t")
	if err != nil {
		t.Fatalf("CREATE VIEW (simple): %v", err)
	}

	tests := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		{"UPDATE agg view", "UPDATE v_agg SET val='x'", true},
		{"DELETE from agg view", "DELETE FROM v_agg", true},
		{"UPDATE simple view", "UPDATE v_simple SET val='x'", false},
		{"DELETE from simple view", "DELETE FROM v_simple", false},
		{"UPDATE table", "UPDATE t SET val='x'", false},
		{"DELETE from table", "DELETE FROM t", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ex.Exec(ctx, tt.sql)
			if (err != nil) != tt.wantErr {
				t.Errorf("Exec(%q) error = %v, wantErr %v", tt.sql, err, tt.wantErr)
			}
		})
	}
}