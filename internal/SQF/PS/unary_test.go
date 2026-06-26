package PS

import (
	"testing"
)

func TestUnaryNot(t *testing.T) {
	tests := []string{
		"SELECT NOT 1",
		"SELECT NOT 0",
		"SELECT NOT NULL",
		"SELECT NOT (1 = 1)",
		"SELECT -5",
		"SELECT ~5",
		"SELECT NOT NOT 1",
	}
	for _, sql := range tests {
		t.Run(sql, func(t *testing.T) {
			parser := NewParser(sql)
			stmt, err := parser.Parse()
			if err != nil {
				t.Fatalf("Parse error for %q: %v", sql, err)
			}
			t.Logf("%q => %+v", sql, stmt)
		})
	}
}
