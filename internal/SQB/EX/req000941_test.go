package EX

import (
	"context"
	"testing"
)

func TestReq000941_AliasWithUnaryMinus(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)

	for _, s := range []string{
		"CREATE TABLE tab1(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"INSERT INTO tab1 VALUES(51,14,96)",
		"INSERT INTO tab1 VALUES(85,5,59)",
		"INSERT INTO tab1 VALUES(91,47,68)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	// REQ000941: MIN(-col0) with table alias should not return NULL
	rows, err := ex.QueryAll(ctx, "SELECT MIN(- col0) FROM tab1 AS cor0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Data[0].Kind == 0 {
		t.Errorf("MIN(- col0) with alias: got %v, want -91", rows[0].Data[0].ToAny())
	}
	if rows[0].Data[0].ToAny() != int64(-91) {
		t.Errorf("MIN(- col0) with alias: got %v, want -91", rows[0].Data[0].ToAny())
	}

	// Full REQ000941 query
	rows, err = ex.QueryAll(ctx, "SELECT DISTINCT - MIN( DISTINCT - col0 ) * - 30 + COUNT( * ) AS col1 FROM tab1 AS cor0 WHERE + col0 IS NOT NULL")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("REQ000941: expected 1 row, got %d", len(rows))
	}
	v := rows[0].Data[0].ToAny()
	if v != int64(-2727) {
		t.Errorf("REQ000941: got %v, want -2727", v)
	}
}

func TestReq000941_UnaryMinusWithAlias(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)

	for _, s := range []string{
		"CREATE TABLE tab2(col0 INTEGER, col1 INTEGER, col2 INTEGER)",
		"INSERT INTO tab2 VALUES(51,14,96)",
		"INSERT INTO tab2 VALUES(85,5,59)",
		"INSERT INTO tab2 VALUES(91,47,68)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	// Plain -col0 with alias
	rows, err := ex.QueryAll(ctx, "SELECT - col0 FROM tab2 AS cor0")
	if err != nil {
		t.Fatal(err)
	}
	expected := []int64{-51, -85, -91}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i, r := range rows {
		v := r.Data[0].ToAny()
		if v != expected[i] {
			t.Errorf("row %d: got %v, want %v", i, v, expected[i])
		}
	}
}
