//go:build slt_corpus

package slt

import (
	"context"
	"testing"
	"time"
)

// TestUnaryRepeat runs the same unary-minus SQL three times through
// QueryRaw on the same engine to verify that stmtCache + RE.Rewrite
// does not cause sign inversion on alternating executions.
// REQ001712: constantFoldUnaryMinus used to mutate NumberLiteral.Val
// in place, causing the cached AST to flip sign on each execution.
func TestUnaryRepeat(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}

	driver := NewRazorDriver()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := driver.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer driver.Close(context.Background())

	tests := []struct {
		sql  string
		want string
	}{
		{"SELECT - 73", "-73"},
		{"SELECT ( - + 71 ) AS col1", "-71"},
		{"SELECT + - ( 74 )", "-74"},
		{"SELECT ALL 16 + + - 21 AS col2", "-5"},
		{"SELECT - 35 * 98 AS col1", "-3430"},
		{"SELECT 56 * + - 34", "-1904"},
		{"SELECT - 60 - - 41 col0", "-19"},
	}

	for _, tt := range tests {
		if err := driver.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		for attempt := 1; attempt <= 3; attempt++ {
			rs, err := driver.QueryRaw(ctx, tt.sql)
			if err != nil {
				t.Errorf("[%s] attempt %d ERROR: %v", tt.sql, attempt, err)
				break
			}
			got := ""
			for _, row := range rs.Rows {
				for c, cell := range row {
					if c > 0 {
						got += "|"
					}
					got += cell.String()
				}
			}
			if got != tt.want {
				t.Errorf("[%s] attempt %d FAIL: got %s, want %s", tt.sql, attempt, got, tt.want)
			}
		}
	}
}
