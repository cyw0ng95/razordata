package EX

import (
	"context"
	"testing"
)

func TestScalarIN_ReturnsOneRow(t *testing.T) {
	e := NewExecutor()
	ctx := context.Background()

	cases := []struct {
		sql    string
		want   int
		val    interface{}
	}{
		{"SELECT 1 IN (2)", 1, false},
		{"SELECT 1 NOT IN (2)", 1, true},
		{"SELECT 1 IN (1)", 1, true},
		{"SELECT 1 NOT IN (1)", 1, false},
		{"SELECT NULL IN (1)", 1, nil},
		{"SELECT 1 IN (NULL)", 1, nil},
		{"SELECT 1 NOT IN (NULL)", 1, nil},
		{"SELECT NULL NOT IN (1)", 1, nil},
		{"SELECT NULL IN (NULL)", 1, nil},
	}
	for _, tc := range cases {
		rows, err := e.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Fatalf("%s: query error: %v", tc.sql, err)
		}
		if len(rows) != tc.want {
			t.Errorf("%s: got %d rows, want %d; data=%v", tc.sql, len(rows), tc.want, rows)
			continue
		}
		if len(rows) > 0 && len(rows[0].Data) > 0 {
			got := rows[0].Data[0]
			if got != tc.val {
				t.Errorf("%s: val got %v (type %T), want %v (type %T)", tc.sql, got, got, tc.val, tc.val)
			}
		}
	}
}
