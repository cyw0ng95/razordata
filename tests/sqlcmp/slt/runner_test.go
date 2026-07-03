//go:build !slt_corpus

package slt

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRunner_StatementOK(t *testing.T) {
	d := &mockDriver{execErr: nil, queryOut: &ResultSet{}}
	r := NewRunner(d, nil, "")
	recs, _ := Parse(strings.NewReader("statement ok\nCREATE TABLE t (a INT)\n"))
	stats := r.Run(context.Background(), recs)
	if stats.Passed != 1 || stats.Failed != 0 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestRunner_StatementError(t *testing.T) {
	d := &mockDriver{execErr: errTest}
	r := NewRunner(d, nil, "")
	recs, _ := Parse(strings.NewReader("statement error\nGARBAGE\n"))
	stats := r.Run(context.Background(), recs)
	if stats.Passed != 1 {
		t.Errorf("expected pass (engine rejected), got %+v", stats)
	}
}

func TestRunner_StatementOK_EngineRejects(t *testing.T) {
	d := &mockDriver{execErr: errTest}
	r := NewRunner(d, nil, "")
	recs, _ := Parse(strings.NewReader("statement ok\nCREATE TABLE t (a INT)\n"))
	stats := r.Run(context.Background(), recs)
	if stats.Failed != 1 {
		t.Errorf("expected fail (engine rejected an ok-stmt), got %+v", stats)
	}
}

func TestRunner_ClassifierSkips(t *testing.T) {
	d := &mockDriver{execErr: errUnsupported}
	c := fixedClassifier{verdict: VerdictSkipped}
	r := NewRunner(d, c, "")
	recs, _ := Parse(strings.NewReader("statement ok\nSELECT * FROM nope\n"))
	stats := r.Run(context.Background(), recs)
	if stats.Skipped != 1 || stats.Failed != 0 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestRunner_QueryPass(t *testing.T) {
	d := &mockDriver{
		queryOut: &ResultSet{
			Columns: []string{"x"},
			Rows:    [][]Value{{Value{Kind: TypeInteger, Int: 42}}},
		},
	}
	r := NewRunner(d, nil, "")
	recs, _ := Parse(strings.NewReader("query I\nSELECT 42\n----\n42\n"))
	stats := r.Run(context.Background(), recs)
	if stats.Passed != 1 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestRunner_QueryFail(t *testing.T) {
	d := &mockDriver{
		queryOut: &ResultSet{
			Columns: []string{"x"},
			Rows:    [][]Value{{Value{Kind: TypeInteger, Int: 99}}},
		},
	}
	r := NewRunner(d, nil, "")
	recs, _ := Parse(strings.NewReader("query I\nSELECT 42\n----\n42\n"))
	stats := r.Run(context.Background(), recs)
	if stats.Failed != 1 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestRunner_LabelEnforced(t *testing.T) {
	counter := 0
	d := &countingDriver{
		queryFn: func(_ context.Context, _ string) (*ResultSet, error) {
			counter++
			// First call returns [1], second returns [2] — same
			// label, mismatched results.
			v := int64(1)
			if counter == 2 {
				v = 2
			}
			return &ResultSet{
				Columns: []string{"x"},
				Rows:    [][]Value{{Value{Kind: TypeInteger, Int: v}}},
			}, nil
		},
	}
	r := NewRunner(d, nil, "")
	src := `query I label-xy
SELECT 1
----
1

query I label-xy
SELECT 2
----
1
`
	recs, _ := Parse(strings.NewReader(src))
	stats := r.Run(context.Background(), recs)
	if stats.Failed != 1 {
		t.Errorf("expected label mismatch to fail, got %+v", stats)
	}
}

func TestRunner_HaltShortCircuits(t *testing.T) {
	d := &mockDriver{queryOut: &ResultSet{
		Columns: []string{"x"},
		Rows:    [][]Value{{Value{Kind: TypeInteger, Int: 1}}},
	}}
	r := NewRunner(d, nil, "")
	src := `query I
SELECT 1
----
1

halt

query I
SELECT 2
----
2
`
	recs, _ := Parse(strings.NewReader(src))
	stats := r.Run(context.Background(), recs)
	if stats.Passed != 1 {
		t.Errorf("halt did not short-circuit: %+v", stats)
	}
}

func TestDiff_ExactMatch(t *testing.T) {
	d := &mockDriver{queryOut: &ResultSet{
		Columns: []string{"x"},
		Rows: [][]Value{
			{Value{Kind: TypeInteger, Int: 1}},
			{Value{Kind: TypeInteger, Int: 2}},
		},
	}}
	r := NewRunner(d, nil, "")
	src := "query I rowsort\nSELECT x\n----\n1\n2\n"
	recs, _ := Parse(strings.NewReader(src))
	stats := r.Run(context.Background(), recs)
	if stats.Failed != 0 {
		t.Errorf("row sort diff failed: %+v", stats)
	}
}

func TestDiff_HashedMatch(t *testing.T) {
	// Build 3 integers, hash them, and use the marker.
	d := &mockDriver{queryOut: &ResultSet{
		Columns: []string{"x"},
		Rows: [][]Value{
			{Value{Kind: TypeInteger, Int: 1}},
			{Value{Kind: TypeInteger, Int: 2}},
			{Value{Kind: TypeInteger, Int: 3}},
		},
	}}
	r := NewRunner(d, nil, "")
	// Build the expected hash the same way diffHashed does.
	var flat []Value
	for _, row := range d.queryOut.Rows {
		flat = append(flat, row...)
	}
	want := HashSorted(flat)
	t.Logf("want = %s", want)
	src := "query I rowsort\nSELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3\n----\n3 values hashing to " + want + "\n"
	recs, _ := Parse(strings.NewReader(src))
	stats := r.Run(context.Background(), recs)
	if stats.Failed != 0 {
		t.Errorf("hashed row diff failed: %+v", stats)
	}
}

// --- mocks ---

type mockDriver struct {
	execErr  error
	queryOut *ResultSet
	queryErr error
}

func (m *mockDriver) Connect(_ context.Context) error        { return nil }
func (m *mockDriver) Close(_ context.Context) error          { return nil }
func (m *mockDriver) Exec(_ context.Context, _ string) error { return m.execErr }
func (m *mockDriver) Query(_ context.Context, _ string) (*ResultSet, error) {
	return m.queryOut, m.queryErr
}

type countingDriver struct {
	queryFn func(ctx context.Context, sql string) (*ResultSet, error)
}

func (c *countingDriver) Connect(_ context.Context) error        { return nil }
func (c *countingDriver) Close(_ context.Context) error          { return nil }
func (c *countingDriver) Exec(_ context.Context, _ string) error { return nil }
func (c *countingDriver) Query(ctx context.Context, sql string) (*ResultSet, error) {
	return c.queryFn(ctx, sql)
}

// TestRunner_SplitRange verifies RAZOR_SLT_RANGE gating.
func TestRunner_SplitRange(t *testing.T) {
	// Build 5 query records, each expecting different values.
	script := `query I
SELECT 1
----
1

query I
SELECT 2
----
2

query I
SELECT 3
----
3

query I
SELECT 4
----
4

query I
SELECT 5
----
5
`
	// mockDriver that returns results matching SELECT N queries.
	queryResult := func(ctx context.Context, sql string) (*ResultSet, error) {
		var val int64
		_, _ = fmt.Sscanf(sql, "SELECT %d", &val)
		return &ResultSet{Rows: [][]Value{{Value{Kind: TypeInteger, Int: val}}}}, nil
	}

	tests := []struct {
		name     string
		env      string
		wantPass int
		wantSkip int
		wantFail int
	}{
		{"no range", "", 5, 0, 0},
		{"range 2:4", "2:4", 3, 1, 0},
		{"range 1:1", "1:1", 1, 0, 0},
		{"range 5:5", "5:5", 1, 4, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("RAZOR_SLT_RANGE", tt.env)
			}
				d := &countingDriver{queryFn: queryResult}
			r := NewRunner(d, nil, "")
			recs, _ := Parse(strings.NewReader(script))
			stats := r.Run(context.Background(), recs)
			if stats.Passed != tt.wantPass {
				t.Errorf("passed: got %d, want %d", stats.Passed, tt.wantPass)
			}
			if stats.Skipped != tt.wantSkip {
				t.Errorf("skipped: got %d, want %d", stats.Skipped, tt.wantSkip)
			}
			if stats.Failed != tt.wantFail {
				t.Errorf("failed: got %d, want %d", stats.Failed, tt.wantFail)
			}
		})
	}
}

type fixedClassifier struct{ verdict Verdict }

func (f fixedClassifier) Classify(_ error) Verdict { return f.verdict }

var errTest = errString("test error")

type errString string

func (e errString) Error() string { return string(e) }

var errUnsupported = errString("razordata: syntax error")
