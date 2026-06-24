package EX

import (
	"context"
	"math"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// REQ000384: abs(X) — returns absolute value, NULL→NULL, string→0.0, MIN_INT64→error

func TestREQ384_Abs_UnitTests(t *testing.T) {
	t.Run("positive int", func(t *testing.T) {
		result, err := evalAbs([]PS.Expr{&PS.NumberLiteral{Val: 42}}, nil, nil)
		if err != nil {
			t.Fatalf("abs(42): %v", err)
		}
		if result != int64(42) {
			t.Errorf("abs(42) = %v, want 42", result)
		}
	})
	t.Run("negative int", func(t *testing.T) {
		result, err := evalAbs([]PS.Expr{&PS.NumberLiteral{Val: -42}}, nil, nil)
		if err != nil {
			t.Fatalf("abs(-42): %v", err)
		}
		if result != int64(42) {
			t.Errorf("abs(-42) = %v, want 42", result)
		}
	})
	t.Run("zero", func(t *testing.T) {
		result, err := evalAbs([]PS.Expr{&PS.NumberLiteral{Val: 0}}, nil, nil)
		if err != nil {
			t.Fatalf("abs(0): %v", err)
		}
		if result != int64(0) {
			t.Errorf("abs(0) = %v, want 0", result)
		}
	})
	t.Run("negative float", func(t *testing.T) {
		result, err := evalAbs([]PS.Expr{&PS.FloatLiteral{Val: -3.14}}, nil, nil)
		if err != nil {
			t.Fatalf("abs(-3.14): %v", err)
		}
		if result != float64(3.14) {
			t.Errorf("abs(-3.14) = %v, want 3.14", result)
		}
	})
	t.Run("null", func(t *testing.T) {
		result, err := evalAbs([]PS.Expr{&PS.NullLiteral{}}, nil, nil)
		if err != nil {
			t.Fatalf("abs(NULL): %v", err)
		}
		if result != nil {
			t.Errorf("abs(NULL) = %v, want nil", result)
		}
	})
	t.Run("string to 0.0", func(t *testing.T) {
		result, err := evalAbs([]PS.Expr{&PS.StringLiteral{Val: "hello"}}, nil, nil)
		if err != nil {
			t.Fatalf("abs('hello'): %v", err)
		}
		if result != 0.0 {
			t.Errorf("abs('hello') = %v, want 0.0", result)
		}
	})
	t.Run("min int64 overflow", func(t *testing.T) {
		result, err := evalAbs([]PS.Expr{&PS.NumberLiteral{Val: math.MinInt64}}, nil, nil)
		if err == nil {
			t.Errorf("abs(MIN_INT64) = %v, want error", result)
		}
	})
	t.Run("wrong arg count", func(t *testing.T) {
		_, err := evalAbs(nil, nil, nil)
		if err == nil {
			t.Error("abs() with no args: want error")
		}
	})
}

func TestREQ384_Abs_EndToEnd(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	for _, s := range []string{
		"INSERT INTO t VALUES (1, 5)",
		"INSERT INTO t VALUES (2, -5)",
		"INSERT INTO t VALUES (3, NULL)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT abs(v) FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(5)) {
		t.Errorf("abs(5) = %v, want 5", rows[0].Data[0])
	}
	if !rows[1].Data[0].Equal(NewIntValue(5)) {
		t.Errorf("abs(-5) = %v, want 5", rows[1].Data[0])
	}
	if !rows[2].Data[0].IsNull() {
		t.Errorf("abs(NULL) = %v, want nil", rows[2].Data[0])
	}
}

func TestREQ384_Abs_StringToZero(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTable("t", []string{"v"})
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES ('hello')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := ex.QueryAll(ctx, "SELECT abs(v) FROM t")
	if err != nil {
		t.Fatalf("abs string: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].Data[0].Equal(NewFloatValue(0.0)) {
		t.Errorf("abs('hello') = %v, want 0.0", rows[0].Data[0])
	}
}

// REQ000447: count(DISTINCT x), avg(DISTINCT x), sum(DISTINCT x) semantics

func TestREQ447_CountDistinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	for _, s := range []string{
		"INSERT INTO t VALUES (1, 1)",
		"INSERT INTO t VALUES (2, 0)",
		"INSERT INTO t VALUES (3, NULL)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT count(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("count DISTINCT: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(2)) {
		t.Errorf("count(DISTINCT v) over {1, 0, NULL} = %v, want 2", rows[0].Data[0])
	}
}

func TestREQ447_SumDistinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	for _, s := range []string{
		"INSERT INTO t VALUES (1, 1)",
		"INSERT INTO t VALUES (2, 0)",
		"INSERT INTO t VALUES (3, NULL)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT sum(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("sum DISTINCT: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].Data[0].Equal(NewIntValue(1)) {
		t.Errorf("sum(DISTINCT v) over {1, 0, NULL} = %v, want 1", rows[0].Data[0])
	}
}

func TestREQ447_AvgDistinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()

	for _, s := range []string{
		"INSERT INTO t VALUES (1, 1)",
		"INSERT INTO t VALUES (2, 0)",
		"INSERT INTO t VALUES (3, NULL)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT avg(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("avg DISTINCT: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	v := rows[0].Data[0]
	f, ok := v.ToAny().(float64)
	if !ok {
		t.Errorf("avg(DISTINCT v) = %v (%T), want float64", v, v)
	} else if f != 0.5 {
		t.Errorf("avg(DISTINCT v) over {1, 0, NULL} = %v, want 0.5", f)
	}
}
