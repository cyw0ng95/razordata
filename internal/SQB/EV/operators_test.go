package EV

import (
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	"testing"
)

func TestOperators_Bitwise(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		{"SELECT 5 & 3", int64(1)},
		{"SELECT 5 | 3", int64(7)},
		{"SELECT 5 ^ 3", int64(6)},
		{"SELECT ~5", int64(-6)},
		{"SELECT 5 % 3", int64(2)},
		{"SELECT 10 % 3", int64(1)},
		{"SELECT 'Hello' || ' World'", "Hello World"},
		{"SELECT 'a' || 'b' || 'c'", "abc"},
		{"SELECT 12 & 10 | 3", int64(12&10 | 3)}, // 12&10=8, 8|3=11
		{"SELECT 255 ^ 255", int64(0)},
		{"SELECT ~0", int64(-1)},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			parser := PS.NewParser(tt.sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			sel := stmt.(*PS.Select)
			got, err := EvalValue(sel.Cols[0], nil, nil)
			if err != nil {
				t.Fatalf("Eval error: %v", err)
			}
			if got.ToAny() != tt.want {
				t.Fatalf("Got %v (%T), want %v (%T)", got.ToAny(), got.ToAny(), tt.want, tt.want)
			}
		})
	}
}
