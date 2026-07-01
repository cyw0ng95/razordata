package EX

import (
	"context"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestEvalRound_Int64FastPath verifies REQ000772: ROUND(int_col)
// returns the int64 value directly without going through float64.
func TestEvalRound_Int64FastPath(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(42))}})
	DT.TablesMu.Unlock()

	e := NewExecutor()
	ctx := context.Background()

	// ROUND(int) — int64 fast path.
	rows, err := e.QueryAll(ctx, "SELECT ROUND(id) FROM t")
	if err != nil {
		t.Fatalf("ROUND: %v", err)
	}
	if len(rows) != 1 || len(rows[0].Data) != 1 {
		t.Fatalf("expected 1 row × 1 col, got %d×%d", len(rows), len(rows[0].Data))
	}
	// DT.Result must be int64 (same type as input).
	v, ok := rows[0].Data[0].ToAny().(int64)
	if !ok {
		t.Fatalf("ROUND(int64) returned %s, want int64", rows[0].Data[0].Kind)
	}
	if v != 42 {
		t.Errorf("ROUND(42) = %v, want 42", rows[0].Data[0])
	}
}

// TestEvalSign_Int64FastPath verifies REQ000772: SIGN(int_col)
// branches on int64 directly without float64 conversion.
func TestEvalSign_Int64FastPath(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	for _, v := range []int64{-5, 0, 7} {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(v)}})
	}
	DT.TablesMu.Unlock()

	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "SELECT SIGN(id) FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("SIGN: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	want := []int64{-1, 0, 1}
	for i, r := range rows {
		got, ok := r.Data[0].ToAny().(int64)
		if !ok {
			t.Errorf("row %d: SIGN returned %T, want int64", i, r.Data[0])
		}
		if got != want[i] {
			t.Errorf("row %d: SIGN(%d) = %d, want %d", i, []int64{-5, 0, 7}[i], got, want[i])
		}
	}
}

// TestEvalRound_NullPreserved verifies REQ000772: ROUND(NULL)
// still returns NULL via the int64 fast path.
func TestEvalRound_NullPreserved(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NullValue()}})
	DT.TablesMu.Unlock()

	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "SELECT ROUND(id) FROM t")
	if err != nil {
		t.Fatalf("ROUND NULL: %v", err)
	}
	if rows[0].Data[0].IsNull() == false {
		t.Errorf("ROUND(NULL) = %v, want nil", rows[0].Data[0])
	}
}

// TestEvalSign_NullPreserved verifies REQ000772: SIGN(NULL)
// still returns NULL via the int64 fast path.
func TestEvalSign_NullPreserved(t *testing.T) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NullValue()}})
	DT.TablesMu.Unlock()

	e := NewExecutor()
	ctx := context.Background()

	rows, err := e.QueryAll(ctx, "SELECT SIGN(id) FROM t")
	if err != nil {
		t.Fatalf("SIGN NULL: %v", err)
	}
	if rows[0].Data[0].IsNull() == false {
		t.Errorf("SIGN(NULL) = %v, want nil", rows[0].Data[0])
	}
}

// BenchmarkEvalRound_Int64 measures the int64 fast path.
// REQ000772: should be measurably faster than the float64 path.
func BenchmarkEvalRound_Int64(b *testing.B) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	for i := 0; i < 1000; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(i))}})
	}
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()
	// Pre-warm.
	_, _ = ex.QueryAll(ctx, "SELECT ROUND(id) FROM t")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ex.QueryAll(ctx, "SELECT ROUND(id) FROM t")
	}
}

func BenchmarkEvalSign_Int64(b *testing.B) {
	UnregisterAll()
	defer UnregisterAll()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	for i := 0; i < 1000; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(i))}})
	}
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()
	_, _ = ex.QueryAll(ctx, "SELECT SIGN(id) FROM t")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ex.QueryAll(ctx, "SELECT SIGN(id) FROM t")
	}
}

// Ensure the package's PS import is used (otherwise removed
// by some linters). Trivial test so the import survives.
func TestPSImportUsed_REQ772(t *testing.T) {
	_ = PS.NewParser
}