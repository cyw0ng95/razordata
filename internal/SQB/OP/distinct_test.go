package OP

import (
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

func TestDistinctKey_NoCollision(t *testing.T) {
	// Int vs Float same numeric value
	intRow := pl.Row{Data: []pl.Value{{Kind: KindInt, I64: 1}}}
	floatRow := pl.Row{Data: []pl.Value{{Kind: KindFloat, F64: 1.0}}}
	if DistinctKey(intRow) == DistinctKey(floatRow) {
		t.Errorf("Int(1) and Float(1.0) must not collide: %q == %q",
			DistinctKey(intRow), DistinctKey(floatRow))
	}

	// Null vs empty Int
	nullRow := pl.Row{Data: []pl.Value{{Kind: KindNull}}}
	zeroIntRow := pl.Row{Data: []pl.Value{{Kind: KindInt, I64: 0}}}
	if DistinctKey(nullRow) == DistinctKey(zeroIntRow) {
		t.Errorf("Null and Int(0) must not collide: %q == %q",
			DistinctKey(nullRow), DistinctKey(zeroIntRow))
	}

	// Bool vs Text
	boolTrueRow := pl.Row{Data: []pl.Value{{Kind: KindBool, Bo: true}}}
	textRow := pl.Row{Data: []pl.Value{{Kind: KindText, S: "1"}}}
	if DistinctKey(boolTrueRow) == DistinctKey(textRow) {
		t.Errorf("Bool(true) and Text('1') must not collide: %q == %q",
			DistinctKey(boolTrueRow), DistinctKey(textRow))
	}

	// Bool(true) vs Bool(false)
	boolFalseRow := pl.Row{Data: []pl.Value{{Kind: KindBool, Bo: false}}}
	if DistinctKey(boolTrueRow) == DistinctKey(boolFalseRow) {
		t.Errorf("Bool(true) and Bool(false) must not collide: %q == %q",
			DistinctKey(boolTrueRow), DistinctKey(boolFalseRow))
	}

	// Int vs Float negative values
	negIntRow := pl.Row{Data: []pl.Value{{Kind: KindInt, I64: -1}}}
	negFloatRow := pl.Row{Data: []pl.Value{{Kind: KindFloat, F64: -1.0}}}
	if DistinctKey(negIntRow) == DistinctKey(negFloatRow) {
		t.Errorf("Int(-1) and Float(-1.0) must not collide: %q == %q",
			DistinctKey(negIntRow), DistinctKey(negFloatRow))
	}

	// Text with embedded separator byte
	sepRow := pl.Row{Data: []pl.Value{{Kind: KindText, S: "\x01separator"}}}
	plainRow := pl.Row{Data: []pl.Value{{Kind: KindText, S: "separator"}}}
	if DistinctKey(sepRow) == DistinctKey(plainRow) {
		t.Errorf("Text with \\x01 and without must not collide: %q == %q",
			DistinctKey(sepRow), DistinctKey(plainRow))
	}

	// Multi-column: int+float vs float+int (different order)
	col1 := pl.Row{Data: []pl.Value{{Kind: KindInt, I64: 1}, {Kind: KindFloat, F64: 2.0}}}
	col2 := pl.Row{Data: []pl.Value{{Kind: KindFloat, F64: 1.0}, {Kind: KindInt, I64: 2}}}
	if DistinctKey(col1) == DistinctKey(col2) {
		t.Errorf("Int+Float and Float+Int order must not collide: %q == %q",
			DistinctKey(col1), DistinctKey(col2))
	}

	// Empty vs non-empty text
	emptyTextRow := pl.Row{Data: []pl.Value{{Kind: KindText, S: ""}}}
	nonEmptyTextRow := pl.Row{Data: []pl.Value{{Kind: KindText, S: "a"}}}
	if DistinctKey(emptyTextRow) == DistinctKey(nonEmptyTextRow) {
		t.Errorf("Empty text and non-empty text must not collide: %q == %q",
			DistinctKey(emptyTextRow), DistinctKey(nonEmptyTextRow))
	}
}

func TestDistinctKey_EmptyRow(t *testing.T) {
	row := pl.Row{Data: nil}
	if DistinctKey(row) != "" {
		t.Errorf("empty row should produce empty key, got %q", DistinctKey(row))
	}
	row = pl.Row{Data: []pl.Value{}}
	if DistinctKey(row) != "" {
		t.Errorf("zero-column row should produce empty key, got %q", DistinctKey(row))
	}
}

func TestDistinctKey_Deterministic(t *testing.T) {
	row := pl.Row{Data: []pl.Value{
		{Kind: KindInt, I64: 42},
		{Kind: KindText, S: "hello"},
		{Kind: KindBool, Bo: true},
		{Kind: KindNull},
		{Kind: KindFloat, F64: 3.14},
		{Kind: KindBlob, B: []byte{0, 1, 2}},
	}}
	first := DistinctKey(row)
	for i := 0; i < 100; i++ {
		if DistinctKey(row) != first {
			t.Fatalf("DistinctKey not deterministic: iteration %d changed", i)
		}
	}
}

func TestDistinctKey_Blob(t *testing.T) {
	blobRow := pl.Row{Data: []pl.Value{{Kind: KindBlob, B: []byte{0, 1, 2, 3}}}}
	emptyBlobRow := pl.Row{Data: []pl.Value{{Kind: KindBlob, B: []byte{}}}}
	if DistinctKey(blobRow) == DistinctKey(emptyBlobRow) {
		t.Errorf("Blob with content and empty blob must not collide: %q == %q",
			DistinctKey(blobRow), DistinctKey(emptyBlobRow))
	}
}