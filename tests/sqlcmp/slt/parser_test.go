//go:build !slt_corpus

package slt

import (
	"strings"
	"testing"
)

func TestParse_StatementOK(t *testing.T) {
	in := `statement ok
CREATE TABLE t (a INT)
`
	recs, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	r := recs[0]
	if r.Kind != RecordStatementOK {
		t.Errorf("kind = %v, want %v", r.Kind, RecordStatementOK)
	}
	if !strings.Contains(r.SQL, "CREATE TABLE") {
		t.Errorf("SQL = %q, missing CREATE TABLE", r.SQL)
	}
}

func TestParse_StatementError(t *testing.T) {
	in := `statement error
GARBAGE
`
	recs, _ := Parse(strings.NewReader(in))
	if len(recs) != 1 {
		t.Fatalf("got %d", len(recs))
	}
	if recs[0].Kind != RecordStatementError {
		t.Errorf("kind = %v", recs[0].Kind)
	}
}

func TestParse_QueryWithExpected(t *testing.T) {
	in := `query I rowsort
SELECT 1
----
1
`
	recs, _ := Parse(strings.NewReader(in))
	if len(recs) != 1 {
		t.Fatalf("got %d", len(recs))
	}
	r := recs[0]
	if r.Kind != RecordQuery {
		t.Errorf("kind = %v", r.Kind)
	}
	if r.TypeString != "I" {
		t.Errorf("typeString = %q", r.TypeString)
	}
	if r.Sort != RowSort {
		t.Errorf("sort = %v", r.Sort)
	}
	if !strings.Contains(r.SQL, "SELECT 1") {
		t.Errorf("SQL = %q", r.SQL)
	}
	if len(r.Expected) != 1 || r.Expected[0][0].Kind != TypeInteger || r.Expected[0][0].Int != 1 {
		t.Errorf("Expected = %+v", r.Expected)
	}
}

func TestParse_QueryWithLabel(t *testing.T) {
	in := `query IT label-foo
SELECT 1, 'a'
----
1 a
`
	recs, _ := Parse(strings.NewReader(in))
	if recs[0].Label != "label-foo" {
		t.Errorf("Label = %q", recs[0].Label)
	}
	if len(recs[0].Expected) != 1 {
		t.Fatalf("Expected = %+v", recs[0].Expected)
	}
	if recs[0].Expected[0][0].Int != 1 || recs[0].Expected[0][1].Text != "a" {
		t.Errorf("cells = %+v", recs[0].Expected[0])
	}
}

func TestParse_HaltAndThreshold(t *testing.T) {
	in := `hash-threshold 5

query I
SELECT 1
----
1

halt
`
	recs, _ := Parse(strings.NewReader(in))
	if len(recs) != 3 {
		t.Fatalf("got %d records: %+v", len(recs), recs)
	}
	if recs[0].Kind != RecordHashThreshold || recs[0].HashThreshold != 5 {
		t.Errorf("hash-threshold: %+v", recs[0])
	}
	if recs[2].Kind != RecordHalt {
		t.Errorf("halt: %+v", recs[2])
	}
}

func TestParse_SkipifOnlyif(t *testing.T) {
	in := `skipif postgresql
onlyif mysql
statement ok
SELECT 1
`
	recs, _ := Parse(strings.NewReader(in))
	if len(recs) != 3 {
		t.Fatalf("got %d", len(recs))
	}
	if recs[0].Kind != RecordSkipIf || recs[0].DBName != "postgresql" {
		t.Errorf("skipif: %+v", recs[0])
	}
	if recs[1].Kind != RecordOnlyIf || recs[1].DBName != "mysql" {
		t.Errorf("onlyif: %+v", recs[1])
	}
}

func TestParse_CommentStripping(t *testing.T) {
	in := `# leading comment
statement ok  # trailing
CREATE TABLE t (a INT)
# between-records
statement ok
INSERT INTO t VALUES (1)
`
	recs, _ := Parse(strings.NewReader(in))
	if len(recs) != 2 {
		t.Fatalf("got %d: %+v", len(recs), recs)
	}
	if !strings.Contains(recs[0].SQL, "CREATE TABLE") {
		t.Errorf("first SQL = %q", recs[0].SQL)
	}
}

func TestParse_PoundInsideString(t *testing.T) {
	in := `statement ok
INSERT INTO t VALUES ('a#b')
`
	recs, _ := Parse(strings.NewReader(in))
	if len(recs) != 1 {
		t.Fatalf("got %d", len(recs))
	}
	if !strings.Contains(recs[0].SQL, "'a#b'") {
		t.Errorf("SQL = %q; pound inside string lost", recs[0].SQL)
	}
}

func TestParse_HashedRows(t *testing.T) {
	in := `query I nosort
SELECT 1
----
3 values hashing to 2b3ba7d20181917b
`
	recs, _ := Parse(strings.NewReader(in))
	if len(recs) != 1 {
		t.Fatalf("got %d", len(recs))
	}
	if len(recs[0].Expected) != 1 {
		t.Fatalf("Expected = %+v", recs[0].Expected)
	}
	if !strings.Contains(recs[0].Expected[0][0].Text, "values hashing to") {
		t.Errorf("marker = %q", recs[0].Expected[0][0].Text)
	}
}

func TestParseValue(t *testing.T) {
	cases := []struct {
		tok   string
		code  string
		want  Value
		isErr bool
	}{
		{"NULL", "T", Value{Kind: TypeNull}, false},
		{"(empty)", "T", Value{Kind: TypeText, Text: ""}, false},
		{"hello", "T", Value{Kind: TypeText, Text: "hello"}, false},
		{"42", "I", Value{Kind: TypeInteger, Int: 42}, false},
		{"1.000", "I", Value{Kind: TypeInteger, Int: 1}, false},
		{"3.140", "R", Value{Kind: TypeReal, Real: 3.14}, false},
		{"abc", "I", Value{}, true},
		{"abc", "R", Value{}, true},
		{"hi", "Q", Value{}, true},
	}
	for _, tc := range cases {
		got, err := ParseValue(tc.tok, tc.code)
		if tc.isErr {
			if err == nil {
				t.Errorf("ParseValue(%q,%q): want error", tc.tok, tc.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseValue(%q,%q): %v", tc.tok, tc.code, err)
			continue
		}
		if got.Kind != tc.want.Kind {
			t.Errorf("ParseValue(%q,%q).Kind = %v, want %v", tc.tok, tc.code, got.Kind, tc.want.Kind)
			continue
		}
		switch got.Kind {
		case TypeText:
			if got.Text != tc.want.Text {
				t.Errorf("text = %q", got.Text)
			}
		case TypeInteger:
			if got.Int != tc.want.Int {
				t.Errorf("int = %d", got.Int)
			}
		case TypeReal:
			if got.Real != tc.want.Real {
				t.Errorf("real = %v", got.Real)
			}
		}
	}
}

// TestParse_MultiRowQuery verifies parsing of queries with multiple result rows.
func TestParse_MultiRowQuery(t *testing.T) {
	in := `query I rowsort
SELECT 1 UNION ALL SELECT 2
----
1
2
`
	recs, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Kind != RecordQuery {
		t.Errorf("kind = %v, want Query", recs[0].Kind)
	}
	if len(recs[0].Expected) != 2 {
		t.Errorf("expected rows = %d, want 2", len(recs[0].Expected))
	}
}

// TestParse_HashThreshold verifies hash-threshold parsing.
func TestParse_HashThreshold(t *testing.T) {
	in := `hash-threshold 10
`
	recs, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Kind != RecordHashThreshold {
		t.Errorf("kind = %v, want HashThreshold", recs[0].Kind)
	}
	if recs[0].HashThreshold != 10 {
		t.Errorf("threshold = %d, want 10", recs[0].HashThreshold)
	}
}
