package PS

import (
	"testing"
)

func TestParse_Interval_InvalidUnit(t *testing.T) {
	input := "SELECT INTERVAL '7' FOO FROM t"
	p := NewParser(input)
	_, err := p.Parse()
	if err == nil {
		t.Error("expected error for invalid interval unit FOO")
	}
}

func TestParse_Interval_ValidUnits(t *testing.T) {
	units := []string{"YEAR", "MONTH", "DAY", "HOUR", "MINUTE", "SECOND"}
	for _, unit := range units {
		input := "SELECT INTERVAL '1' " + unit + " FROM t"
		p := NewParser(input)
		_, err := p.Parse()
		if err != nil {
			t.Errorf("valid unit %s: unexpected error: %v", unit, err)
		}
	}
}
