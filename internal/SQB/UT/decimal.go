package UT

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Decimal wraps big.Float with precision/scale metadata (REQ000229).
// Precision is the total number of significant digits; scale is the
// number of digits after the decimal point.
type Decimal struct {
	Value     *big.Float
	Precision int
	Scale     int
}

// ErrDecimalOverflow is returned when a result exceeds the configured precision.
var ErrDecimalOverflow = errors.New("ex: decimal overflow")

// ErrDecimalScale is returned when scale is invalid (negative or exceeds precision).
var ErrDecimalScale = errors.New("ex: invalid decimal scale")

// ErrDecimalDivByZero is returned when dividing by zero.
var ErrDecimalDivByZero = errors.New("ex: division by zero")

// NewDecimal constructs a Decimal from a string with given precision/scale.
func NewDecimal(s string, precision, scale int) (*Decimal, error) {
	if scale < 0 {
		return nil, fmt.Errorf("%w: scale=%d", ErrDecimalScale, scale)
	}
	if scale > precision {
		return nil, fmt.Errorf("%w: scale=%d > precision=%d", ErrDecimalScale, scale, precision)
	}
	f, ok := new(big.Float).SetString(s)
	if !ok {
		return nil, fmt.Errorf("ex: invalid decimal literal %q", s)
	}
	d := &Decimal{Value: f, Precision: precision, Scale: scale}
	if err := d.checkOverflow(); err != nil {
		return nil, err
	}
	return d, nil
}

// NewDecimalFromInt creates a Decimal from an int64 value.
func NewDecimalFromInt(n int64, precision, scale int) (*Decimal, error) {
	if scale < 0 || scale > precision {
		return nil, fmt.Errorf("%w: scale=%d precision=%d", ErrDecimalScale, scale, precision)
	}
	f := new(big.Float).SetInt64(n)
	d := &Decimal{Value: f, Precision: precision, Scale: scale}
	if err := d.checkOverflow(); err != nil {
		return nil, err
	}
	return d, nil
}

// NewDecimalFromFloat creates a Decimal from a float64 value.
func NewDecimalFromFloat(f float64, precision, scale int) (*Decimal, error) {
	if scale < 0 || scale > precision {
		return nil, fmt.Errorf("%w: scale=%d precision=%d", ErrDecimalScale, scale, precision)
	}
	bf := new(big.Float).SetFloat64(f)
	d := &Decimal{Value: bf, Precision: precision, Scale: scale}
	if err := d.checkOverflow(); err != nil {
		return nil, err
	}
	return d, nil
}

// String returns the canonical string representation of the Decimal,
// rounded to the configured scale with trailing zeros preserved.
func (d *Decimal) String() string {
	if d == nil || d.Value == nil {
		return "NULL"
	}
	rounded := d.roundToScale()
	if d.Scale == 0 {
		return rounded.Text('f', 0)
	}
	return rounded.Text('f', d.Scale)
}

// roundToScale applies banker's rounding to the configured scale.
func (d *Decimal) roundToScale() *big.Float {
	if d.Scale == 0 {
		return new(big.Float).Copy(d.Value)
	}
	// Quantize to scale using round-half-away-from-zero.
	str := d.Value.Text('f', d.Scale)
	parsed, _ := new(big.Float).SetString(str)
	return parsed
}

// checkOverflow returns ErrDecimalOverflow if the integer part exceeds
// Precision-Scale digits.
func (d *Decimal) checkOverflow() error {
	intDigits := d.Precision - d.Scale
	if intDigits <= 0 {
		return nil
	}
	maxInt := pow10Big(intDigits)
	minInt := new(big.Float).Neg(maxInt)
	if d.Value.Cmp(maxInt) > 0 || d.Value.Cmp(minInt) < 0 {
		return fmt.Errorf("%w: exceeds %d digits", ErrDecimalOverflow, intDigits)
	}
	return nil
}

// Add returns d + other, preserving the max scale and max precision.
func (d *Decimal) Add(other *Decimal) (*Decimal, error) {
	if d == nil || other == nil {
		return nil, errors.New("ex: nil decimal in Add")
	}
	result := new(big.Float).Add(d.Value, other.Value)
	out := &Decimal{
		Value:     result,
		Precision: maxInt(d.Precision, other.Precision),
		Scale:     maxInt(d.Scale, other.Scale),
	}
	if err := out.roundInPlace(); err != nil {
		return nil, err
	}
	return out, nil
}

// Sub returns d - other.
func (d *Decimal) Sub(other *Decimal) (*Decimal, error) {
	if d == nil || other == nil {
		return nil, errors.New("ex: nil decimal in Sub")
	}
	result := new(big.Float).Sub(d.Value, other.Value)
	out := &Decimal{
		Value:     result,
		Precision: maxInt(d.Precision, other.Precision),
		Scale:     maxInt(d.Scale, other.Scale),
	}
	if err := out.roundInPlace(); err != nil {
		return nil, err
	}
	return out, nil
}

// Mul returns d * other with combined scale.
func (d *Decimal) Mul(other *Decimal) (*Decimal, error) {
	if d == nil || other == nil {
		return nil, errors.New("ex: nil decimal in Mul")
	}
	result := new(big.Float).Mul(d.Value, other.Value)
	out := &Decimal{
		Value:     result,
		Precision: d.Precision + other.Precision,
		Scale:     d.Scale + other.Scale,
	}
	if err := out.roundInPlace(); err != nil {
		return nil, err
	}
	return out, nil
}

// Div returns d / other with combined scale (scale = d.Scale + other.Precision + 1).
func (d *Decimal) Div(other *Decimal) (*Decimal, error) {
	if d == nil || other == nil {
		return nil, errors.New("ex: nil decimal in Div")
	}
	if other.Value.Sign() == 0 {
		return nil, ErrDecimalDivByZero
	}
	result := new(big.Float).Quo(d.Value, other.Value)
	scale := d.Scale + other.Precision + 1
	if scale > 38 {
		scale = 38
	}
	out := &Decimal{
		Value:     result,
		Precision: scale,
		Scale:     scale,
	}
	if err := out.roundInPlace(); err != nil {
		return nil, err
	}
	return out, nil
}

// Cmp returns -1, 0, or 1.
func (d *Decimal) Cmp(other *Decimal) int {
	if d == nil && other == nil {
		return 0
	}
	if d == nil {
		return -1
	}
	if other == nil {
		return 1
	}
	return d.Value.Cmp(other.Value)
}

// roundInPlace applies rounding to the configured scale.
func (d *Decimal) roundInPlace() error {
	if d.Scale == 0 {
		return nil
	}
	original := d.Value
	d.Value = d.roundToScale()
	if !d.Value.IsInt() && d.Precision > 0 {
		// Check the integer part doesn't exceed precision-scale digits.
		intDigits := d.Precision - d.Scale
		if intDigits > 0 {
			maxInt := pow10Big(intDigits)
			minInt := new(big.Float).Neg(maxInt)
			if d.Value.Cmp(maxInt) > 0 || d.Value.Cmp(minInt) < 0 {
				d.Value = original
				return fmt.Errorf("%w: result exceeds %d digits", ErrDecimalOverflow, intDigits)
			}
		}
	}
	return nil
}

// ToFloat64 returns a best-effort float64 conversion.
func (d *Decimal) ToFloat64() float64 {
	if d == nil || d.Value == nil {
		return 0
	}
	f, _ := d.Value.Float64()
	return f
}

// FormatDecimal formats a value for DECIMAL(P,S) cast results.
func FormatDecimal(v any, precision, scale int) (string, error) {
	switch x := v.(type) {
	case string:
		d, err := NewDecimal(x, precision, scale)
		if err != nil {
			return "", err
		}
		return d.String(), nil
	case int64:
		d, err := NewDecimalFromInt(x, precision, scale)
		if err != nil {
			return "", err
		}
		return d.String(), nil
	case float64:
		d, err := NewDecimalFromFloat(x, precision, scale)
		if err != nil {
			return "", err
		}
		return d.String(), nil
	}
	return "", fmt.Errorf("ex: cannot convert %T to DECIMAL", v)
}

// EvalDecimalCast handles CAST(... AS DECIMAL(P,S)) with precision/scale semantics.
func EvalDecimalCast(v any, precision, scale int) (any, error) {
	if v == nil {
		return nil, nil
	}
	var s string
	switch x := v.(type) {
	case int64:
		s = strconv.FormatInt(x, 10)
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		s = x
	default:
		s = fmt.Sprintf("%v", v)
	}
	d, err := NewDecimal(s, precision, scale)
	if err != nil {
		return nil, err
	}
	return d.String(), nil
}

// pow10Big returns 10^n as a big.Float.
func pow10Big(n int) *big.Float {
	if n <= 0 {
		return big.NewFloat(1)
	}
	s := "1" + strings.Repeat("0", n)
	f, _ := new(big.Float).SetString(s)
	return f
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
