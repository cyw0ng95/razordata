package EV

import (
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	"testing"
)

func TestREQ001194_UnaryOperator(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		{"SELECT - 56", int64(-56)},
		{"SELECT + 68", int64(68)},
		{"SELECT - - 96", int64(96)},
		{"SELECT + 30 * 3 - - 96", int64(186)},
		{"SELECT - 15", int64(-15)},
		{"SELECT - + 15", int64(-15)},
		{"SELECT 11 * 31 * - - ( 70 )", int64(23870)},
		{"SELECT + - 3", int64(-3)},
		{"SELECT - 0", int64(0)},
		{"SELECT + 0", int64(0)},
		{"SELECT - - 0", int64(0)},
		{"SELECT + 100", int64(100)},
		{"SELECT - 100", int64(-100)},
		{"SELECT + + 50", int64(50)},
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
