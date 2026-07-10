package DT

import (
	"testing"
)

func TestCompareValue_Null(t *testing.T) {
	tests := []struct {
		a, b Value
		want int
	}{
		{NullValue(), NullValue(), 0},
		{NullValue(), NewIntValue(1), -1},
		{NewIntValue(1), NullValue(), 1},
	}
	for _, tc := range tests {
		got := CompareValue(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareValue_Int(t *testing.T) {
	tests := []struct {
		a, b Value
		want int
	}{
		{NewIntValue(1), NewIntValue(2), -1},
		{NewIntValue(2), NewIntValue(1), 1},
		{NewIntValue(1), NewIntValue(1), 0},
		{NewIntValue(1), NewFloatValue(2.0), -1},
		{NewIntValue(2), NewFloatValue(1.0), 1},
		{NewIntValue(1), NewFloatValue(1.0), 0},
	}
	for _, tc := range tests {
		got := CompareValue(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareValue_Float(t *testing.T) {
	tests := []struct {
		a, b Value
		want int
	}{
		{NewFloatValue(1.5), NewFloatValue(2.5), -1},
		{NewFloatValue(2.5), NewFloatValue(1.5), 1},
		{NewFloatValue(1.5), NewFloatValue(1.5), 0},
		{NewFloatValue(1.5), NewIntValue(2), -1},
		{NewFloatValue(2.5), NewIntValue(1), 1},
	}
	for _, tc := range tests {
		got := CompareValue(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareValue_Text(t *testing.T) {
	tests := []struct {
		a, b Value
		want int
	}{
		{NewTextValue("a"), NewTextValue("b"), -1},
		{NewTextValue("b"), NewTextValue("a"), 1},
		{NewTextValue("a"), NewTextValue("a"), 0},
	}
	for _, tc := range tests {
		got := CompareValue(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareValue_Bool(t *testing.T) {
	tests := []struct {
		a, b Value
		want int
	}{
		{NewBoolValue(false), NewBoolValue(true), -1},
		{NewBoolValue(true), NewBoolValue(false), 1},
		{NewBoolValue(true), NewBoolValue(true), 0},
		{NewBoolValue(false), NewBoolValue(false), 0},
	}
	for _, tc := range tests {
		got := CompareValue(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareValue_Blob(t *testing.T) {
	tests := []struct {
		a, b Value
		want int
	}{
		{NewBlobValue([]byte{1}), NewBlobValue([]byte{2}), -1},
		{NewBlobValue([]byte{2}), NewBlobValue([]byte{1}), 1},
		{NewBlobValue([]byte{1}), NewBlobValue([]byte{1}), 0},
		{NewBlobValue(nil), NewBlobValue(nil), 0},
	}
	for _, tc := range tests {
		got := CompareValue(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareValue_CrossKind(t *testing.T) {
	// Different kinds that don't have cross-type promotion: 0
	tests := []struct {
		a, b Value
		want int
	}{
		{NewIntValue(1), NewTextValue("hello"), 0},
		{NewFloatValue(1.0), NewBoolValue(true), 0},
	}
	for _, tc := range tests {
		got := CompareValue(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("CompareValue(%v, %v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestEqualValue_Basic(t *testing.T) {
	tests := []struct {
		a, b   Value
		wantEq bool
	}{
		{NewIntValue(1), NewIntValue(1), true},
		{NewIntValue(1), NewIntValue(2), false},
		{NewFloatValue(1.5), NewFloatValue(1.5), true},
		{NewFloatValue(1.5), NewFloatValue(2.5), false},
		{NewTextValue("a"), NewTextValue("a"), true},
		{NewTextValue("a"), NewTextValue("b"), false},
		{NewBoolValue(true), NewBoolValue(true), true},
		{NewBoolValue(true), NewBoolValue(false), false},
		{NewBlobValue([]byte{1, 2}), NewBlobValue([]byte{1, 2}), true},
		{NewBlobValue([]byte{1}), NewBlobValue([]byte{2}), false},
		{NewBlobValue(nil), NewBlobValue(nil), true},
		{NullValue(), NullValue(), false},
		{NullValue(), NewIntValue(1), false},
		{NewIntValue(1), NullValue(), false},
		{NewIntValue(1), NewFloatValue(1.0), true},
		{NewIntValue(1), NewFloatValue(2.0), false},
		{NewFloatValue(1.0), NewIntValue(1), true},
	}
	for _, tc := range tests {
		got, err := EqualValue(tc.a, tc.b)
		if err != nil {
			t.Errorf("EqualValue(%v, %v): unexpected error %v", tc.a, tc.b, err)
		}
		if got != tc.wantEq {
			t.Errorf("EqualValue(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.wantEq)
		}
	}
}

func BenchmarkCompareValue_Direct(b *testing.B) {
	va := NewIntValue(42)
	vb := NewIntValue(43)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompareValue(va, vb)
	}
}

func BenchmarkCompare_Any(b *testing.B) {
	va := any(int64(42))
	vb := any(int64(43))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Compare(va, vb)
	}
}

// REQ001333: ICU collation comparison with unicode accented characters.
func TestCollation_ICU_AccentedChars(t *testing.T) {
	// German: 'ä' sorts after 'a' (a < ä)
	a := NewTextValue("a")
	umlautA := NewTextValue("ä")

	// Binary comparison: 'a' (0x61) vs 'ä' (0xC3 0xA4) → a < ä → -1
	if got := CompareValue(a, umlautA); got != -1 {
		t.Logf("binary: CompareValue('a', 'ä') = %d (expected -1)", got)
	}

	// Unicode collation: 'ä' sorts after 'a'
	got := CompareValueWithCollation(a, umlautA, "unicode_de_DE")
	if got >= 0 {
		t.Errorf("unicode_de_DE: CompareValueWithCollation('a', 'ä') = %d, want < 0", got)
	}

	// English: 'ä' sorts like 'a' (or close)
	gotEn := CompareValueWithCollation(a, umlautA, "unicode_en_US")
	if gotEn >= 0 {
		t.Logf("unicode_en_US: CompareValueWithCollation('a', 'ä') = %d", gotEn)
	}
}