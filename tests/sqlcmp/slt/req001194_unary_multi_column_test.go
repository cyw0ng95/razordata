//go:build slt_corpus

package slt

import (
    "context"
    "testing"
)

func TestREQ001194_MultiColumnUnaryAggregate(t *testing.T) {
    driver := NewRazorDriver()
    ctx := context.Background()
    if err := driver.Connect(ctx); err != nil {
        t.Fatalf("Connect: %v", err)
    }
    defer driver.Close(ctx)

    tests := []struct {
        sql  string
        want []string
    }{
		{"SELECT - MAX( - 76 ), - 15", []string{"76", "-15"}},
		{"SELECT - MAX( - 76 ), - 15 AS col2", []string{"76", "-15"}},
		{"SELECT - 15, - MAX( - 76 )", []string{"-15", "76"}},
		{"SELECT DISTINCT 32 * + + 59 * + ( + + 41 ) - - 9", []string{"77417"}},
		{"SELECT DISTINCT + 68, 1, - 15", []string{"68", "1", "-15"}},
		{"SELECT - 15", []string{"-15"}},
		{"SELECT - MAX( - 76 )", []string{"76"}},
		{"SELECT 1, - 15", []string{"1", "-15"}},
		{"SELECT DISTINCT + 68", []string{"68"}},
    }
    for _, tt := range tests {
        t.Run(tt.sql, func(t *testing.T) {
            rs, err := driver.Query(ctx, tt.sql)
            if err != nil {
                t.Fatalf("query: %v", err)
            }
            if len(rs.Rows) != 1 {
                t.Fatalf("expected 1 row, got %d", len(rs.Rows))
            }
            row := rs.Rows[0]
            if len(row) != len(tt.want) {
                t.Fatalf("expected %d columns, got %d", len(tt.want), len(row))
            }
            for i, v := range row {
                got := v.String()
                if got != tt.want[i] {
                    t.Errorf("col %d: got %q, want %q", i, got, tt.want[i])
                }
            }
        })
    }
}
