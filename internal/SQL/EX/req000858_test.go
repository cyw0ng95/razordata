package EX

import (
	"context"
	"testing"
)

func TestReq000858_ColumnAliasInWhere(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)

	for _, s := range []string{
		"CREATE TABLE t(id INTEGER, v INTEGER)",
		"INSERT INTO t VALUES(1, 10)",
		"INSERT INTO t VALUES(2, 20)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	// Column alias in WHERE: SELECT v AS value FROM t WHERE value > 15
	rows, err := ex.QueryAll(ctx, "SELECT v AS value FROM t WHERE value > 15")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	} else if rows[0].Data[0].ToAny() != int64(20) {
		t.Errorf("expected 20, got %v", rows[0].Data[0].ToAny())
	}
}

func TestReq000860_InsertSelect(t *testing.T) {
	ctx := context.Background()
	ex := NewExecutorWithEngine(nil)

	for _, s := range []string{
		"CREATE TABLE src(id INTEGER, v INTEGER)",
		"INSERT INTO src VALUES(1, 10)",
		"INSERT INTO src VALUES(2, 20)",
		"CREATE TABLE dst(id INTEGER, v INTEGER)",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	// INSERT INTO dst SELECT * FROM src WHERE v > 15
	_, err := ex.Exec(ctx, "INSERT INTO dst SELECT * FROM src WHERE v > 15")
	if err != nil {
		t.Fatal(err)
	}

	rows, err := ex.QueryAll(ctx, "SELECT v FROM dst")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row in dst, got %d", len(rows))
	} else if rows[0].Data[0].ToAny() != int64(20) {
		t.Errorf("expected 20, got %v", rows[0].Data[0].ToAny())
	}
}
