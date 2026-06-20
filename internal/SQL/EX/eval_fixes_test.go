// Tests for REQ000605/606/607/619/620/621 — eval correctness bugs.
package EX

import (
	"context"
	"testing"
)

// TestEval_BandNullThreeValuedLogic exercises REQ000605: SQL
// AND/OR must be three-valued, not two-valued.
func TestEval_BandNullThreeValuedLogic(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	cases := []struct {
		sql  string
		want string // row value rendered as string
	}{
		{"SELECT NULL AND TRUE FROM t", "<nil>"},
		{"SELECT TRUE AND NULL FROM t", "<nil>"},
		{"SELECT NULL AND FALSE FROM t", "false"},
		{"SELECT FALSE AND NULL FROM t", "false"},
		{"SELECT NULL AND NULL FROM t", "<nil>"},
		{"SELECT NULL OR TRUE FROM t", "true"},
		{"SELECT TRUE OR NULL FROM t", "true"},
		{"SELECT NULL OR FALSE FROM t", "<nil>"},
		{"SELECT FALSE OR NULL FROM t", "<nil>"},
		{"SELECT NULL OR NULL FROM t", "<nil>"},
	}
	for _, tc := range cases {
		rows, err := ex.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.sql, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("%s: expected 1 row, got %d", tc.sql, len(rows))
			continue
		}
		got := formatValue(rows[0].Data[0])
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.sql, got, tc.want)
		}
	}
}

// TestEval_SubstrNullHandling exercises REQ000606: SUBSTR(NULL, ...)
// must return NULL, not "<nil>" (the fmt.Sprint result).
func TestEval_SubstrNullHandling(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT SUBSTR(NULL, 1, 3) FROM t", "<nil>"},
		{"SELECT SUBSTR('hello', NULL, 2) FROM t", "<nil>"},
		{"SELECT SUBSTR(NULL, NULL) FROM t", "<nil>"},
		{"SELECT SUBSTR('hello', 2, 3) FROM t", "ell"},
		{"SELECT SUBSTR('hello', 1) FROM t", "hello"},
	}
	for _, tc := range cases {
		rows, err := ex.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.sql, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("%s: expected 1 row, got %d", tc.sql, len(rows))
			continue
		}
		got := formatValue(rows[0].Data[0])
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.sql, got, tc.want)
		}
	}
}

// TestEval_CastBoolean exercises REQ000607: CAST AS BOOLEAN must
// parse strings semantically ("false", "0", "" are false; the
// truthy() fallback that returned true for any non-empty string
// was wrong).
func TestEval_CastBoolean(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT CAST('false' AS BOOLEAN) FROM t", "false"},
		{"SELECT CAST('FALSE' AS BOOLEAN) FROM t", "false"},
		{"SELECT CAST('0' AS BOOLEAN) FROM t", "false"},
		{"SELECT CAST('' AS BOOLEAN) FROM t", "false"},
		{"SELECT CAST('true' AS BOOLEAN) FROM t", "true"},
		{"SELECT CAST('TRUE' AS BOOLEAN) FROM t", "true"},
		{"SELECT CAST('1' AS BOOLEAN) FROM t", "true"},
		{"SELECT CAST('abc' AS BOOLEAN) FROM t", "true"},
		{"SELECT CAST(0 AS BOOLEAN) FROM t", "false"},
		{"SELECT CAST(1 AS BOOLEAN) FROM t", "true"},
		{"SELECT CAST(NULL AS BOOLEAN) FROM t", "<nil>"}, // nil stays nil through cast
	}
	for _, tc := range cases {
		rows, err := ex.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.sql, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("%s: expected 1 row, got %d", tc.sql, len(rows))
			continue
		}
		got := formatValue(rows[0].Data[0])
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.sql, got, tc.want)
		}
	}
}

// TestEval_SignNull exercises REQ000619: SIGN(NULL) must return NULL.
func TestEval_SignNull(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	rows, err := ex.QueryAll(ctx, "SELECT SIGN(NULL) FROM t")
	if err != nil {
		t.Fatalf("SIGN(NULL): %v", err)
	}
	if got := formatValue(rows[0].Data[0]); got != "<nil>" {
		t.Errorf("SIGN(NULL): got %s, want <nil>", got)
	}
	// Sanity: SIGN of a real number still works.
	rows, err = ex.QueryAll(ctx, "SELECT SIGN(-5) FROM t")
	if err != nil {
		t.Fatalf("SIGN(-5): %v", err)
	}
	if got := formatValue(rows[0].Data[0]); got != "-1" {
		t.Errorf("SIGN(-5): got %s, want -1", got)
	}
	rows, err = ex.QueryAll(ctx, "SELECT SIGN(0) FROM t")
	if err != nil {
		t.Fatalf("SIGN(0): %v", err)
	}
	if got := formatValue(rows[0].Data[0]); got != "0" {
		t.Errorf("SIGN(0): got %s, want 0", got)
	}
}

// TestEval_InstrNull exercises REQ000620: INSTR(NULL, ...) must
// return NULL on either side.
func TestEval_InstrNull(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	cases := []struct {
		sql  string
		want string
	}{
		{"SELECT INSTR(NULL, 'x') FROM t", "<nil>"},
		{"SELECT INSTR('abc', NULL) FROM t", "<nil>"},
		{"SELECT INSTR(NULL, NULL) FROM t", "<nil>"},
		{"SELECT INSTR('hello world', 'world') FROM t", "7"},
		{"SELECT INSTR('hello', 'z') FROM t", "0"},
	}
	for _, tc := range cases {
		rows, err := ex.QueryAll(ctx, tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.sql, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("%s: expected 1 row, got %d", tc.sql, len(rows))
			continue
		}
		got := formatValue(rows[0].Data[0])
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.sql, got, tc.want)
		}
	}
}

// TestEval_OctetLength exercises REQ000621: OCTET_LENGTH of a
// blob returns the raw byte count, not fmt.Sprint's "[104 101 ...]"
// representation.
func TestEval_OctetLength(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")

	// String literal: byte count, not rune count.
	rows, err := ex.QueryAll(ctx, "SELECT OCTET_LENGTH('hello') FROM t")
	if err != nil {
		t.Fatalf("OCTET_LENGTH: %v", err)
	}
	if got := formatValue(rows[0].Data[0]); got != "5" {
		t.Errorf("OCTET_LENGTH('hello'): got %s, want 5", got)
	}
	// Empty string.
	rows, err = ex.QueryAll(ctx, "SELECT OCTET_LENGTH('') FROM t")
	if err != nil {
		t.Fatalf("OCTET_LENGTH: %v", err)
	}
	if got := formatValue(rows[0].Data[0]); got != "0" {
		t.Errorf("OCTET_LENGTH(''): got %s, want 0", got)
	}
	// NULL → NULL.
	rows, err = ex.QueryAll(ctx, "SELECT OCTET_LENGTH(NULL) FROM t")
	if err != nil {
		t.Fatalf("OCTET_LENGTH: %v", err)
	}
	if got := formatValue(rows[0].Data[0]); got != "<nil>" {
		t.Errorf("OCTET_LENGTH(NULL): got %s, want <nil>", got)
	}
}

// formatValue renders a row value as a string for comparison in
// tests. nil becomes "<nil>", booleans become "true"/"false",
// numerics use their default formatting.
func formatValue(v any) string {
	if v == nil {
		return "<nil>"
	}
	if b, ok := v.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}
	return fmtSprint(v)
}

func fmtSprint(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if i, ok := v.(int64); ok {
		return i64toa(i)
	}
	return ""
}

func i64toa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := 20
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// silence unused-import warnings if any of the helpers above
// are not used by a particular build.
var _ = context.Background
var _ = TestEval_BandNullThreeValuedLogic
