package PS

import (
	"testing"
)

func TestParseNotBetween(t *testing.T) {
	p := NewParser("SELECT * FROM t1 WHERE d NOT BETWEEN 110 AND 150")
	_, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	t.Log("Parse OK")
}
