package EX

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
		{"2024-01-15 10:30:45", true},
		{"2024-01-15T10:30:45", true},
		{"10:30:45", true},
		{"2024-13-01", false},
		{"not-a-date", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, ok := ParseDateTime(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ParseDateTime(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
		})
	}
}

func TestFormatDateTimeValue(t *testing.T) {
	ts := time.Date(2024, 6, 15, 14, 30, 45, 0, time.UTC)
	tests := []struct {
		typ  int
		want string
	}{
		{73, "2024-06-15 14:30:45"}, // TIMESTAMP
		{75, "2024-06-15"},          // DATE
		{76, "14:30:45"},            // TIME
		{0, "2024-06-15 14:30:45"},  // default
	}
	for _, tt := range tests {
		got := FormatDateTimeValue(ts, tt.typ)
		if got != tt.want {
			t.Errorf("FormatDateTimeValue(ts, %d) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

func TestJulianDay(t *testing.T) {
	// Known value: 2000-01-01 12:00:00 UTC = JD 2451545.0
	jd := julianDay(time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC))
	if jd < 2451544.9 || jd > 2451545.1 {
		t.Errorf("julianDay(2000-01-01 12:00) = %f, want ~2451545.0", jd)
	}
}

func TestDateAdd(t *testing.T) {
	base := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		amount int
		unit   string
		want   string
	}{
		{7, "DAY", "2024-01-22 10:30:00"},
		{1, "MONTH", "2024-02-15 10:30:00"},
		{1, "YEAR", "2025-01-15 10:30:00"},
		{2, "HOUR", "2024-01-15 12:30:00"},
		{30, "MINUTE", "2024-01-15 11:00:00"},
		{-1, "DAY", "2024-01-14 10:30:00"},
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
	a := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	b := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		unit string
		want int64
	}{
		{"DAY", 166},
		{"MONTH", 5},
		{"YEAR", 0},
	}
	for _, tt := range tests {
		got := DateDiff(a, b, tt.unit)
		if got != tt.want {
			t.Errorf("DateDiff(a, b, %q) = %d, want %d", tt.unit, got, tt.want)
		}
	}
}

func TestDateTimeFuncs(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want string
	}{
		{"DATE", []any{"2024-06-15 14:30:00"}, "2024-06-15"},
		{"TIME", []any{"2024-06-15 14:30:00"}, "14:30:00"},
		{"DATETIME", []any{"2024-06-15"}, "2024-06-15 00:00:00"},
		{"STRFTIME", []any{"%Y-%m", "2024-06-15 14:30:00"}, "2024-06"},
		{"STRFTIME", []any{"%H:%M", "2024-06-15 14:30:00"}, "14:30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evalDateTimeFunc(tt.name, tt.args)
			if err != nil {
				t.Fatalf("evalDateTimeFunc(%q) error: %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("evalDateTimeFunc(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestExtractFunc(t *testing.T) {
	ts := "2024-06-15 14:30:45"
	tests := []struct {
		field string
		want  int64
	}{
		{"YEAR", 2024},
		{"MONTH", 6},
		{"DAY", 15},
		{"HOUR", 14},
		{"MINUTE", 30},
		{"SECOND", 45},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			got, err := evalDateTimeFunc("EXTRACT", []any{tt.field, ts})
			if err != nil {
				t.Fatalf("EXTRACT(%s) error: %v", tt.field, err)
			}
			if got != tt.want {
				t.Errorf("EXTRACT(%s) = %v, want %v", tt.field, got, tt.want)
			}
		})
	}
}

func TestParseInterval(t *testing.T) {
	tests := []struct {
		input    string
		wantAmt  int
		wantUnit string
		wantOK   bool
	}{
		{"7 DAY", 7, "DAY", true},
		{"1 MONTH", 1, "MONTH", true},
		{"3 HOUR", 3, "HOUR", true},
		{"30 MINUTE", 30, "MINUTE", true},
		{"bad", 0, "", false},
		{"", 0, "", false},
		{"abc DAY", 0, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			amt, unit, ok := ParseInterval(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ParseInterval(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && tt.wantOK {
				if amt != tt.wantAmt || unit != tt.wantUnit {
					t.Errorf("ParseInterval(%q) = (%d, %q), want (%d, %q)", tt.input, amt, unit, tt.wantAmt, tt.wantUnit)
				}
			}
		})
	}
}

func TestDateTimeArithmetic(t *testing.T) {
	base := "2024-06-15 10:00:00"
	interval := &IntervalValue{Amount: 7, Unit: "DAY"}

	got, err := DateTimeArithmetic(base, interval, "+")
	if err != nil {
		t.Fatalf("DateTimeArithmetic error: %v", err)
	}
	if got != "2024-06-22 10:00:00" {
		t.Errorf("DateTimeArithmetic(+7 DAY) = %q, want %q", got, "2024-06-22 10:00:00")
	}

	got, err = DateTimeArithmetic(base, interval, "-")
	if err != nil {
		t.Fatalf("DateTimeArithmetic error: %v", err)
	}
	if got != "2024-06-08 10:00:00" {
		t.Errorf("DateTimeArithmetic(-7 DAY) = %q, want %q", got, "2024-06-08 10:00:00")
	}
}

func TestToTime(t *testing.T) {
	tests := []struct {
		input any
		want  bool
	}{
		{"2024-06-15", true},
		{"2024-06-15 10:30:00", true},
		{int64(1718438400), true},
		{float64(1718438400), true},
		{nil, false},
		{123, false}, // int, not int64
	}
	for _, tt := range tests {
		_, ok := toTime(tt.input)
		if ok != tt.want {
			t.Errorf("toTime(%T(%v)) ok = %v, want %v", tt.input, tt.input, ok, tt.want)
		}
	}
}
