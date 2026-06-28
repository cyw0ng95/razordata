package UT

import (
	"strings"
	"testing"
)

func TestNewDecimal_Basic(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		precision int
		scale     int
		want      string
		wantErr   bool
	}{
		{"simple-int", "123", 5, 0, "123", false},
		{"with-scale", "123.45", 5, 2, "123.45", false},
		{"negative", "-99.99", 5, 2, "-99.99", false},
		{"scale-zero", "1000", 10, 0, "1000", false},
		{"zero", "0", 5, 2, "0.00", false},
		{"invalid", "abc", 5, 2, "", true},
		{"scale-gt-precision", "1.0", 2, 5, "", true},
		{"negative-scale", "1.0", 5, -1, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewDecimal(tt.input, tt.precision, tt.scale)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewDecimal() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil {
				got := d.String()
				if got != tt.want {
					t.Errorf("String() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestNewDecimal_Overflow(t *testing.T) {
	// Precision=3, Scale=1 => max integer part = 99
	_, err := NewDecimal("123.4", 3, 1)
	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Errorf("expected overflow error, got %v", err)
	}

	_, err = NewDecimal("-123.4", 3, 1)
	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Errorf("expected overflow error for negative, got %v", err)
	}
}

func TestDecimal_Add(t *testing.T) {
	d1, _ := NewDecimal("1.23", 5, 2)
	d2, _ := NewDecimal("2.34", 5, 2)

	result, err := d1.Add(d2)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got, want := result.String(), "3.57"; got != want {
		t.Errorf("Add() = %q, want %q", got, want)
	}
}

func TestDecimal_Sub(t *testing.T) {
	d1, _ := NewDecimal("5.50", 5, 2)
	d2, _ := NewDecimal("2.25", 5, 2)

	result, err := d1.Sub(d2)
	if err != nil {
		t.Fatalf("Sub() error = %v", err)
	}
	if got, want := result.String(), "3.25"; got != want {
		t.Errorf("Sub() = %q, want %q", got, want)
	}
}

func TestDecimal_Mul(t *testing.T) {
	d1, _ := NewDecimal("1.50", 4, 2)
	d2, _ := NewDecimal("2.00", 4, 2)

	result, err := d1.Mul(d2)
	if err != nil {
		t.Fatalf("Mul() error = %v", err)
	}
	if got, want := result.String(), "3.0000"; got != want {
		t.Errorf("Mul() = %q, want %q", got, want)
	}
}

func TestDecimal_Div(t *testing.T) {
	d1, _ := NewDecimal("10.00", 4, 2)
	d2, _ := NewDecimal("3.00", 2, 0)

	result, err := d1.Div(d2)
	if err != nil {
		t.Fatalf("Div() error = %v", err)
	}
	// 10/3 = 3.333... rounded to scale
	if !strings.HasPrefix(result.String(), "3.33") {
		t.Errorf("Div() = %q, want prefix 3.33", result.String())
	}
}

func TestDecimal_DivByZero(t *testing.T) {
	d1, _ := NewDecimal("10.00", 4, 2)
	d2, _ := NewDecimal("0.00", 2, 0)

	_, err := d1.Div(d2)
	if err == nil {
		t.Errorf("expected division by zero error")
	}
}

func TestDecimal_Cmp(t *testing.T) {
	d1, _ := NewDecimal("1.00", 4, 2)
	d2, _ := NewDecimal("2.00", 4, 2)
	d3, _ := NewDecimal("1.00", 4, 2)

	if d1.Cmp(d2) != -1 {
		t.Errorf("1.00 < 2.00 should return -1")
	}
	if d2.Cmp(d1) != 1 {
		t.Errorf("2.00 > 1.00 should return 1")
	}
	if d1.Cmp(d3) != 0 {
		t.Errorf("1.00 == 1.00 should return 0")
	}
}

func TestDecimal_FromInt(t *testing.T) {
	d, err := NewDecimalFromInt(12345, 7, 0)
	if err != nil {
		t.Fatalf("NewDecimalFromInt() error = %v", err)
	}
	if d.String() != "12345" {
		t.Errorf("String() = %q, want 12345", d.String())
	}
}

func TestDecimal_FromFloat(t *testing.T) {
	d, err := NewDecimalFromFloat(3.14159, 6, 4)
	if err != nil {
		t.Fatalf("NewDecimalFromFloat() error = %v", err)
	}
	if d.String() != "3.1416" {
		t.Errorf("String() = %q, want 3.1416", d.String())
	}
}

func TestFormatDecimal(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		precision int
		scale     int
		want      string
		wantErr   bool
	}{
		{"string", "1.23", 5, 2, "1.23", false},
		{"int", int64(100), 5, 2, "100.00", false},
		{"float", 3.14, 5, 2, "3.14", false},
		{"overflow", "999.999", 3, 1, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatDecimal(tt.in, tt.precision, tt.scale)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FormatDecimal() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("FormatDecimal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEvalDecimalCast(t *testing.T) {
	// int64 -> DECIMAL(5,2)
	got, err := EvalDecimalCast(int64(100), 5, 2)
	if err != nil {
		t.Fatalf("EvalDecimalCast() error = %v", err)
	}
	if got != "100.00" {
		t.Errorf("EvalDecimalCast(int) = %q, want 100.00", got)
	}

	// float64 -> DECIMAL(5,2)
	got, err = EvalDecimalCast(3.14159, 5, 2)
	if err != nil {
		t.Fatalf("EvalDecimalCast() error = %v", err)
	}
	if got != "3.14" {
		t.Errorf("EvalDecimalCast(float) = %q, want 3.14", got)
	}

	// string -> DECIMAL(5,2)
	got, err = EvalDecimalCast("99.99", 5, 2)
	if err != nil {
		t.Fatalf("EvalDecimalCast() error = %v", err)
	}
	if got != "99.99" {
		t.Errorf("EvalDecimalCast(string) = %q, want 99.99", got)
	}

	// nil -> nil
	got, err = EvalDecimalCast(nil, 5, 2)
	if err != nil {
		t.Fatalf("EvalDecimalCast(nil) error = %v", err)
	}
	if got != nil {
		t.Errorf("EvalDecimalCast(nil) = %v, want nil", got)
	}
}

func TestDecimal_NegativeScale(t *testing.T) {
	_, err := NewDecimal("1.0", 5, -1)
	if err == nil {
		t.Errorf("expected error for negative scale")
	}
}

func TestDecimal_ScaleGreaterThanPrecision(t *testing.T) {
	_, err := NewDecimal("1.0", 2, 5)
	if err == nil {
		t.Errorf("expected error for scale > precision")
	}
}

func TestDecimal_ToFloat64(t *testing.T) {
	d, _ := NewDecimal("3.14", 5, 2)
	if got := d.ToFloat64(); got != 3.14 {
		t.Errorf("ToFloat64() = %v, want 3.14", got)
	}
}
