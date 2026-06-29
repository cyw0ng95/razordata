package EV

import (
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
	"testing"
)

func TestSpecialForms_NoParens(t *testing.T) {
	tests := []struct {
		sql  string
		want any
	}{
		// COALESCE without parens (special form)
		{"SELECT COALESCE NULL, 42", int64(42)},
		// NULLIF without parens (special form)
		{"SELECT NULLIF 5, 5", nil},
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
