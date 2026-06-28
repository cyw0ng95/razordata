package UT

import (
	"testing"
	"time"
)

func TestParseDateTime(t *testing.T) {
	tests := []struct {
		input  string
		wantOK bool
	}{
		{"2024-01-15", true},
		{"2024-01-15 10:30:00", true},
		{"2024-01-15T10:30:00", true},
		{"10:30:00", true},
		{"2024-01-15 10:30:00.123", true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		_, ok := ParseDateTime(tt.input)
		if ok != tt.wantOK {
			t.Errorf("ParseDateTime(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
		}
	}
}

func TestFormatDateTimeValue(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		typ  int
		want string
	}{
		{73, "2024-01-15 10:30:00"}, // TIMESTAMP
		{75, "2024-01-15"},          // DATE
		{76, "10:30:00"},            // TIME
		{99, "2024-01-15 10:30:00"}, // default
	}

	for _, tt := range tests {
		got := FormatDateTimeValue(ts, tt.typ)
		if got != tt.want {
			t.Errorf("FormatDateTimeValue(ts, %d) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

func TestDateAdd(t *testing.T) {
	base := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		amount int
		unit   string
		want   string
	}{
		{1, "YEAR", "2025-01-15 10:30:00"},
		{1, "MONTH", "2024-02-15 10:30:00"},
		{1, "DAY", "2024-01-16 10:30:00"},
		{1, "HOUR", "2024-01-15 11:30:00"},
		{1, "MINUTE", "2024-01-15 10:31:00"},
		{1, "SECOND", "2024-01-15 10:30:01"},
	}

	for _, tt := range tests {
		got := DateAdd(base, tt.amount, tt.unit)
		gotStr := got.Format("2006-01-02 15:04:05")
		if gotStr != tt.want {
			t.Errorf("DateAdd(%v, %d, %q) = %q, want %q", base, tt.amount, tt.unit, gotStr, tt.want)
		}
	}
}

func TestDateDiff(t *testing.T) {
	a := time.Date(2024, 1, 16, 10, 30, 0, 0, time.UTC)
	b := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		unit string
		want int64
	}{
		{"DAY", 1},
		{"HOUR", 24},
		{"MINUTE", 1440},
		{"SECOND", 86400},
	}

	for _, tt := range tests {
		got := DateDiff(a, b, tt.unit)
		if got != tt.want {
			t.Errorf("DateDiff(a, b, %q) = %d, want %d", tt.unit, got, tt.want)
		}
	}
}

func TestEvalDateTimeFunc(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want string
	}{
		{"DATE", []any{"2024-01-15 10:30:00"}, "2024-01-15"},
		{"TIME", []any{"2024-01-15 10:30:00"}, "10:30:00"},
		{"DATETIME", []any{"2024-01-15 10:30:00"}, "2024-01-15 10:30:00"},
	}

	for _, tt := range tests {
		got, err := EvalDateTimeFunc(tt.name, tt.args)
		if err != nil {
			t.Fatalf("EvalDateTimeFunc(%q) error: %v", tt.name, err)
		}
		if got != tt.want {
			t.Errorf("EvalDateTimeFunc(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestEvalExtractField(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)

	tests := []struct {
		field string
		want  int64
	}{
		{"YEAR", 2024},
		{"MONTH", 1},
		{"DAY", 15},
		{"HOUR", 10},
		{"MINUTE", 30},
		{"SECOND", 45},
	}

	for _, tt := range tests {
		got, err := EvalExtractField(tt.field, ts)
		if err != nil {
			t.Fatalf("EvalExtractField(%q) error: %v", tt.field, err)
		}
		if got != tt.want {
			t.Errorf("EvalExtractField(%q) = %d, want %d", tt.field, got, tt.want)
		}
	}
}

func TestParseInterval(t *testing.T) {
	tests := []struct {
		input   string
		wantOK  bool
		wantAmt int
		wantUnit string
	}{
		{"7 DAY", true, 7, "DAY"},
		{"30 DAYS", true, 30, "DAYS"},
		{"2 HOUR", true, 2, "HOUR"},
		{"invalid", false, 0, ""},
		{"", false, 0, ""},
	}

	for _, tt := range tests {
		amt, unit, ok := ParseInterval(tt.input)
		if ok != tt.wantOK {
			t.Errorf("ParseInterval(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
		}
		if ok && (amt != tt.wantAmt || unit != tt.wantUnit) {
			t.Errorf("ParseInterval(%q) = (%d, %q), want (%d, %q)", tt.input, amt, unit, tt.wantAmt, tt.wantUnit)
		}
	}
}

func TestDateTimeArithmetic(t *testing.T) {
	base := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	interval := &IntervalValue{Amount: 7, Unit: "DAY"}

	got, err := DateTimeArithmetic(base, interval, "+")
	if err != nil {
		t.Fatalf("DateTimeArithmetic error: %v", err)
	}
	if got != "2024-01-22 10:30:00" {
		t.Errorf("DateTimeArithmetic(+7 DAY) = %q, want %q", got, "2024-01-22 10:30:00")
	}

	got, err = DateTimeArithmetic(base, interval, "-")
	if err != nil {
		t.Fatalf("DateTimeArithmetic error: %v", err)
	}
	if got != "2024-01-08 10:30:00" {
		t.Errorf("DateTimeArithmetic(-7 DAY) = %q, want %q", got, "2024-01-08 10:30:00")
	}
}
