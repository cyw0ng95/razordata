package EX

import (
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
	"testing"
)

func TestNumericOverflow(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		// Normal cases
		{"SELECT 5 * 3", int64(15)},
		{"SELECT -5 * 3", int64(-15)},
		{"SELECT 0 * 100", int64(0)},
		// Overflow cases — must return nil, not panic/wrap
		{"SELECT 9223372036854775807 * 2", nil},
		{"SELECT 1000000000 * 10000000000", nil},
		{"SELECT (-9223372036854775807) * -1", int64(9223372036854775807)}, // -MaxInt64 * -1 = MaxInt64, no overflow
		{"SELECT 9999999999 * 9999999999", nil},
		// Large but representable products
		{"SELECT 1000000 * 1000000", int64(1000000000000)},
		{"SELECT 46341 * 46341", int64(2147488281)},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			sel := stmt.(*PS.Select)
			got, err := Eval(sel.Cols[0], nil, nil)
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}
