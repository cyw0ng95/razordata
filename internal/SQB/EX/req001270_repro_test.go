//go:build !slt_corpus

package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestREQ001270_CommaJoin_Reproduction(t *testing.T) {
	ResetForTest(t)
	ex, _ := newEngineExecutor(t)

	ctx := context.Background()

	// Create 4 tables
	for _, tbl := range []string{"t4", "t6", "t7", "t8"} {
		col := tbl[1:]
		ex.RegisterTable(tbl, []string{"a" + col, "b" + col, "c" + col, "d" + col, "e" + col, "x" + col})
	}

	// Insert test data: 3 rows per table
	for _, tbl := range []string{"t4", "t6", "t7", "t8"} {
		for i := 1; i <= 3; i++ {
			sql := fmt.Sprintf("INSERT INTO %s VALUES (%d, %d, %d, %d, %d, 'row%d')",
				tbl, i, i*10, i*100, i*50, i*5, i)
			if _, err := ex.Exec(ctx, sql); err != nil {
				t.Fatalf("insert %s: %v", sql, err)
			}
		}
	}

	// 2-table variants
	queries2 := []struct {
		name string
		sql  string
	}{
		{
			"FROM_t6_t4",
			"SELECT * FROM t6, t4 WHERE (b4=20 OR 30=b4) AND d6=50",
		},
		{
			"FROM_t6_t8",
			"SELECT * FROM t6, t8 WHERE 15=e8 AND d6=50",
		},
	}
	for _, q := range queries2 {
		rows, err := ex.QueryAll(ctx, q.sql)
		if err != nil {
			t.Fatalf("%s: query error: %v", q.name, err)
		}
		t.Logf("%s: %d rows", q.name, len(rows))
	}

	// 3-table variants
	queries3 := []struct {
		name string
		sql  string
	}{
		{
			"FROM_t6_t4_t8",
			"SELECT * FROM t6, t4, t8 WHERE (b4=20 OR 30=b4) AND 15=e8 AND d6=50",
		},
		{
			"FROM_t6_t8_t4",
			"SELECT * FROM t6, t8, t4 WHERE (b4=20 OR 30=b4) AND 15=e8 AND d6=50",
		},
	}
	for _, q := range queries3 {
		rows, err := ex.QueryAll(ctx, q.sql)
		if err != nil {
			t.Fatalf("%s: query error: %v", q.name, err)
		}
		t.Logf("%s: %d rows", q.name, len(rows))
	}

	// 4-table variants
	queries4 := []struct {
		name string
		sql  string
	}{
		{
			"FROM_t6_t4_t8_t7",
			"SELECT d4, b7, x8, c6*47 FROM t6, t4, t8, t7 WHERE (b4=20 OR 30=b4) AND 15=e8 AND d6=50 AND e7 in (15, 25, 35, 40)",
		},
		{
			"FROM_t6_t8_t4_t7",
			"SELECT d4, b7, x8, c6*47 FROM t6, t8, t4, t7 WHERE (b4=20 OR 30=b4) AND 15=e8 AND d6=50 AND e7 in (15, 25, 35, 40)",
		},
	}
	for _, q := range queries4 {
		rows, err := ex.QueryAll(ctx, q.sql)
		if err != nil {
			t.Fatalf("%s: query error: %v", q.name, err)
		}
		t.Logf("%s: %d rows", q.name, len(rows))
	}
}
