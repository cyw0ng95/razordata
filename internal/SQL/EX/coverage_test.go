package EX

import (
	"context"
	"fmt"
	"testing"
)

func TestCoverage_Aggregate_WithParams(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	// Verify WithParams doesn't panic
	agg := NewAggregate(nil, nil, nil)
	agg.WithParams(nil)
}

func TestCoverage_AnalyzeWithStore(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.Exec(context.Background(), "CREATE TABLE t (id INT, v INT)")
	ex.Exec(context.Background(), "INSERT INTO t VALUES (1, 10)")
	ex.Exec(context.Background(), "INSERT INTO t VALUES (2, 20)")
	_, err := ex.Exec(context.Background(), "ANALYZE t")
	if err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}
}

func TestCoverage_Analyze_WithParams(t *testing.T) {
	a := NewAnalyze(nil)
	a2 := a.WithParams([]any{42})
	if a2 == nil {
		t.Fatal("WithParams returned nil")
	}
}

func TestCoverage_Vacuum_WithParams(t *testing.T) {
	v := NewVacuum(nil)
	v2 := v.WithParams([]any{42})
	if v2 == nil {
		t.Fatal("WithParams returned nil")
	}
}

func TestCoverage_newUniqueForCatalog(t *testing.T) {
	unique := []UniqueKey{
		{Cols: []int{0, 1}},
		{Cols: []int{2}},
	}
	result := newUniqueForCatalog(unique, []string{"a", "b", "c"})
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

func TestCoverage_newUniqueForCatalogEmpty(t *testing.T) {
	result := newUniqueForCatalog(nil, nil)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestCoverage_Compound_Intersect(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE id > 0 INTERSECT SELECT id FROM t WHERE id < 3")
	if err != nil {
		t.Fatalf("INTERSECT: %v", err)
	}
}

func TestCoverage_Compound_Except(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE id < 3 EXCEPT SELECT id FROM t WHERE id > 1")
	if err != nil {
		t.Fatalf("EXCEPT: %v", err)
	}
}

func TestCoverage_Compound_UnionAll(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE id = 1 UNION ALL SELECT id FROM t WHERE id = 2")
	if err != nil {
		t.Fatalf("UNION ALL: %v", err)
	}
}

func TestCoverage_Compound_Union(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t UNION SELECT id FROM t")
	if err != nil {
		t.Fatalf("UNION: %v", err)
	}
}

func TestCoverage_Compound_UnionOrderBy(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t UNION SELECT id FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("UNION ORDER BY: %v", err)
	}
}

func TestCoverage_Compound_UnionLimit(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t UNION ALL SELECT id FROM t LIMIT 1")
	if err != nil {
		t.Fatalf("UNION LIMIT: %v", err)
	}
}

func TestCoverage_sumDistinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 20)")
	rows, err := ex.QueryAll(ctx, "SELECT SUM(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("SUM DISTINCT: %v", err)
	}
	val, _ := rows[0].Data[0].ToAny().(int64)
	if val != 30 {
		t.Errorf("SUM DISTINCT = %d, want 30", val)
	}
}

func TestCoverage_avgDistinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 10)")
	_, err := ex.QueryAll(ctx, "SELECT AVG(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("AVG DISTINCT: %v", err)
	}
}

func TestCoverage_countDistinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 20)")
	_, err := ex.QueryAll(ctx, "SELECT COUNT(DISTINCT v) FROM t")
	if err != nil {
		t.Fatalf("COUNT DISTINCT: %v", err)
	}
}

func TestCoverage_sumWithNull(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 20)")
	_, err := ex.QueryAll(ctx, "SELECT SUM(v) FROM t")
	if err != nil {
		t.Fatalf("SUM with NULL: %v", err)
	}
}

func TestCoverage_avgWithNull(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 20)")
	_, err := ex.QueryAll(ctx, "SELECT AVG(v) FROM t")
	if err != nil {
		t.Fatalf("AVG with NULL: %v", err)
	}
}

func TestCoverage_minMaxWithNull(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, NULL)")
	_, err := ex.QueryAll(ctx, "SELECT MIN(v), MAX(v) FROM t")
	if err != nil {
		t.Fatalf("MIN/MAX with NULL: %v", err)
	}
}

func TestCoverage_groupConcatWithNull(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'a')")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 'c')")
	_, err := ex.QueryAll(ctx, "SELECT GROUP_CONCAT(v) FROM t")
	if err != nil {
		t.Fatalf("GROUP_CONCAT with NULL: %v", err)
	}
}

func TestCoverage_groupConcatDistinctSep(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'a')")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'a')")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 'b')")
	_, err := ex.QueryAll(ctx, "SELECT GROUP_CONCAT(DISTINCT v, '|') FROM t")
	if err != nil {
		t.Fatalf("GROUP_CONCAT DISTINCT sep: %v", err)
	}
}

func TestCoverage_aggGroupBy(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "grp", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x', 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'x', 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 'y', 30)")
	_, err := ex.QueryAll(ctx, "SELECT grp, COUNT(*), SUM(v), AVG(v) FROM t GROUP BY grp")
	if err != nil {
		t.Fatalf("GROUP BY agg: %v", err)
	}
}

func TestCoverage_aggGroupByHaving(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "grp", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x', 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'x', 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 'y', 30)")
	_, err := ex.QueryAll(ctx, "SELECT grp, COUNT(*) FROM t GROUP BY grp HAVING COUNT(*) > 1")
	if err != nil {
		t.Fatalf("GROUP BY HAVING: %v", err)
	}
}

func TestCoverage_scalarIn(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	rows, err := ex.QueryAll(ctx, "SELECT 1 IN (1, 2, 3)")
	if err != nil {
		t.Fatalf("scalar IN: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestCoverage_scalarInMiss(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	_, err := ex.QueryAll(ctx, "SELECT 5 IN (1, 2, 3)")
	if err != nil {
		t.Fatalf("scalar IN miss: %v", err)
	}
}

func TestCoverage_deleteWhere(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	_, err := ex.Exec(ctx, "DELETE FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("DELETE WHERE: %v", err)
	}
}

func TestCoverage_deleteAll(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	_, err := ex.Exec(ctx, "DELETE FROM t")
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
}

func TestCoverage_updateWhere(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	_, err := ex.Exec(ctx, "UPDATE t SET v = 99 WHERE id = 1")
	if err != nil {
		t.Fatalf("UPDATE WHERE: %v", err)
	}
}

func TestCoverage_distinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 20)")
	rows, err := ex.QueryAll(ctx, "SELECT DISTINCT v FROM t")
	if err != nil {
		t.Fatalf("DISTINCT: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}

func TestCoverage_orderByExpr(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "a", "b"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10, 5)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 5, 20)")
	rows, err := ex.QueryAll(ctx, "SELECT id FROM t ORDER BY a + b")
	if err != nil {
		t.Fatalf("ORDER BY expr: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestCoverage_caseWhen(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	_, err := ex.QueryAll(ctx, "SELECT CASE WHEN v > 15 THEN 'big' ELSE 'small' END FROM t")
	if err != nil {
		t.Fatalf("CASE WHEN: %v", err)
	}
}

func TestCoverage_castIntToText(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 42)")
	_, err := ex.QueryAll(ctx, "SELECT CAST(v AS TEXT) FROM t")
	if err != nil {
		t.Fatalf("CAST: %v", err)
	}
}

func TestCoverage_limitOffset(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	for i := int64(1); i <= 10; i++ {
		ex.Exec(ctx, fmt.Sprintf("INSERT INTO t VALUES (%d)", i))
	}
	rows, err := ex.QueryAll(ctx, "SELECT id FROM t LIMIT 3 OFFSET 2")
	if err != nil {
		t.Fatalf("LIMIT OFFSET: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
}

func TestCoverage_nullComparison(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	_, err := ex.QueryAll(ctx, "SELECT v = NULL FROM t")
	if err != nil {
		t.Fatalf("NULL comparison: %v", err)
	}
}

func TestCoverage_isNullWhere(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	_, err := ex.Exec(ctx, "DELETE FROM t WHERE v IS NULL")
	if err != nil {
		t.Fatalf("IS NULL: %v", err)
	}
}

func TestCoverage_isNotNullWhere(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	_, err := ex.Exec(ctx, "DELETE FROM t WHERE v IS NOT NULL")
	if err != nil {
		t.Fatalf("IS NOT NULL: %v", err)
	}
}

func TestCoverage_notIn(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	_, err := ex.Exec(ctx, "DELETE FROM t WHERE v NOT IN (10, 30)")
	if err != nil {
		t.Fatalf("NOT IN: %v", err)
	}
}

func TestCoverage_notLike(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'hello')")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'world')")
	_, err := ex.Exec(ctx, "DELETE FROM t WHERE v NOT LIKE 'hel%'")
	if err != nil {
		t.Fatalf("NOT LIKE: %v", err)
	}
}

func TestCoverage_notBetween(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	_, err := ex.Exec(ctx, "DELETE FROM t WHERE v NOT BETWEEN 10 AND 20")
	if err != nil {
		t.Fatalf("NOT BETWEEN: %v", err)
	}
}

func TestCoverage_like(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'hello')")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'world')")
	_, err := ex.QueryAll(ctx, "SELECT v FROM t WHERE v LIKE 'hel%'")
	if err != nil {
		t.Fatalf("LIKE: %v", err)
	}
}

// REQ000567: LIKE ... ESCAPE
func TestCoverage_likeEscape(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, '100%')")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, '100_')")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, '200%')")
	ex.Exec(ctx, "INSERT INTO t VALUES (4, 'plain')")
	// ESCAPE '\\' — treat % as literal
	rows, err := ex.QueryAll(ctx, "SELECT v FROM t WHERE v LIKE '100\\%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("LIKE ESCAPE: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0] != NewTextValue("100%") {
		t.Errorf("expected '100%%', got %v", rows[0].Data[0])
	}
	// ESCAPE '\' — treat _ as literal
	rows, err = ex.QueryAll(ctx, "SELECT v FROM t WHERE v LIKE '100\\_' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("LIKE ESCAPE underscore: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Data[0] != NewTextValue("100_") {
		t.Errorf("expected '100_', got %v", rows[0].Data[0])
	}
	// ESCAPE without special chars — no match
	rows, err = ex.QueryAll(ctx, "SELECT v FROM t WHERE v LIKE 'hello' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("LIKE ESCAPE no special: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
	// NOT LIKE with ESCAPE — should match rows 2, 3, 4
	rows, err = ex.QueryAll(ctx, "SELECT v FROM t WHERE v NOT LIKE '100\\%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("NOT LIKE ESCAPE: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// LIKE with wildcard % after escaped character
	rows, err = ex.QueryAll(ctx, "SELECT v FROM t WHERE v LIKE '100\\%%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("LIKE ESCAPE wildcard after: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestCoverage_between(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	_, err := ex.QueryAll(ctx, "SELECT v FROM t WHERE v BETWEEN 15 AND 25")
	if err != nil {
		t.Fatalf("BETWEEN: %v", err)
	}
}

func TestCoverage_coalesce(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "a", "b"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, NULL, NULL)")
	_, err := ex.QueryAll(ctx, "SELECT COALESCE(a, b, 0) FROM t")
	if err != nil {
		t.Fatalf("COALESCE: %v", err)
	}
}

func TestCoverage_nullif(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 20)")
	_, err := ex.QueryAll(ctx, "SELECT NULLIF(v, 10) FROM t")
	if err != nil {
		t.Fatalf("NULLIF: %v", err)
	}
}

func TestCoverage_scalarSubquery(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	_, err := ex.QueryAll(ctx, "SELECT (SELECT MAX(v) FROM t) FROM t")
	if err != nil {
		t.Fatalf("scalar subquery: %v", err)
	}
}

func TestCoverage_existsSubquery(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ex.RegisterTableWithPK("s", []string{"id", "tid"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	ex.Exec(ctx, "INSERT INTO s VALUES (1, 1)")
	ex.Exec(ctx, "INSERT INTO s VALUES (2, 2)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE EXISTS (SELECT 1 FROM s WHERE s.tid = t.id)")
	if err != nil {
		t.Fatalf("EXISTS: %v", err)
	}
}

func TestCoverage_inSubquery(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ex.RegisterTableWithPK("s", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	ex.Exec(ctx, "INSERT INTO s VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO s VALUES (2, 30)")
	_, err := ex.QueryAll(ctx, "SELECT id FROM t WHERE v IN (SELECT v FROM s)")
	if err != nil {
		t.Fatalf("IN subquery: %v", err)
	}
}

func TestCoverage_windowRowNumber(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	_, err := ex.QueryAll(ctx, "SELECT ROW_NUMBER() OVER (ORDER BY id) FROM t")
	if err != nil {
		t.Fatalf("ROW_NUMBER: %v", err)
	}
}

func TestCoverage_windowRank(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "grp", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x', 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'x', 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 'y', 30)")
	_, err := ex.QueryAll(ctx, "SELECT RANK() OVER (PARTITION BY grp ORDER BY v) FROM t")
	if err != nil {
		t.Fatalf("RANK: %v", err)
	}
}

func TestCoverage_windowDenseRank(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 20)")
	_, err := ex.QueryAll(ctx, "SELECT DENSE_RANK() OVER (ORDER BY v) FROM t")
	if err != nil {
		t.Fatalf("DENSE_RANK: %v", err)
	}
}

func TestCoverage_windowSum(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "grp", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x', 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 'x', 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 'y', 30)")
	_, err := ex.QueryAll(ctx, "SELECT SUM(v) OVER (PARTITION BY grp) FROM t")
	if err != nil {
		t.Fatalf("window SUM: %v", err)
	}
}

func TestCoverage_createTrigger(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	_, err := ex.Exec(ctx, "CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END")
	if err != nil {
		t.Fatalf("CREATE TRIGGER: %v", err)
	}
}

func TestCoverage_dropTrigger(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END")
	_, err := ex.Exec(ctx, "DROP TRIGGER trg")
	if err != nil {
		t.Fatalf("DROP TRIGGER: %v", err)
	}
}

func TestCoverage_createView(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	_, err := ex.Exec(ctx, "CREATE VIEW v AS SELECT * FROM t")
	if err != nil {
		t.Fatalf("CREATE VIEW: %v", err)
	}
}

func TestCoverage_dropView(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "CREATE VIEW v AS SELECT * FROM t")
	_, err := ex.Exec(ctx, "DROP VIEW v")
	if err != nil {
		t.Fatalf("DROP VIEW: %v", err)
	}
}

func TestCoverage_dropViewIfExists(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	_, err := ex.Exec(ctx, "DROP VIEW IF EXISTS nonexistent")
	if err != nil {
		t.Fatalf("DROP VIEW IF EXISTS nonexistent: %v", err)
	}
}

func TestCoverage_dropTriggerIfExists(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	_, err := ex.Exec(ctx, "DROP TRIGGER IF EXISTS nonexistent")
	if err != nil {
		t.Fatalf("DROP TRIGGER IF EXISTS nonexistent: %v", err)
	}
}

func TestCoverage_ctas(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("src", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO src VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO src VALUES (2, 20)")
	_, err := ex.Exec(ctx, "CREATE TABLE dst AS SELECT * FROM src")
	if err != nil {
		t.Fatalf("CTAS: %v", err)
	}
}

func TestCoverage_explain(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	rows, err := ex.QueryAll(ctx, "EXPLAIN SELECT * FROM t")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least 1 row from EXPLAIN")
	}
}

func TestCoverage_pragma(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	_, err := ex.Exec(ctx, "PRAGMA cache_size")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
}

func TestCoverage_pragmaWithValue(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	_, err := ex.Exec(ctx, "PRAGMA journal_mode = WAL")
	if err != nil {
		t.Fatalf("PRAGMA =: %v", err)
	}
}

func TestCoverage_reindex(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()
	_, err := ex.Exec(ctx, "REINDEX")
	if err != nil {
		t.Fatalf("REINDEX: %v", err)
	}
}

func TestCoverage_truncate(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2)")
	_, err := ex.Exec(ctx, "TRUNCATE TABLE t")
	if err != nil {
		t.Fatalf("TRUNCATE: %v", err)
	}
}

func TestCoverage_multiAggregate(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	ex.Exec(ctx, "INSERT INTO t VALUES (3, 30)")
	_, err := ex.QueryAll(ctx, "SELECT COUNT(*), SUM(v), AVG(v), MIN(v), MAX(v), GROUP_CONCAT(v) FROM t")
	if err != nil {
		t.Fatalf("multi aggregate: %v", err)
	}
}

func TestCoverage_countWhere(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	_, err := ex.QueryAll(ctx, "SELECT COUNT(*) FROM t WHERE v > 15")
	if err != nil {
		t.Fatalf("COUNT WHERE: %v", err)
	}
}

func TestCoverage_updateAll(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, 20)")
	_, err := ex.Exec(ctx, "UPDATE t SET v = 99")
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
}

func TestCoverage_multiInsert(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	_, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 10), (2, 20), (3, 30)")
	if err != nil {
		t.Fatalf("multi INSERT: %v", err)
	}
}

func TestCoverage_insertOrReplace(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	_, err := ex.Exec(ctx, "INSERT OR REPLACE INTO t VALUES (1, 99)")
	if err != nil {
		t.Fatalf("INSERT OR REPLACE: %v", err)
	}
}

func TestCoverage_insertOrIgnore(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	_, err := ex.Exec(ctx, "INSERT OR IGNORE INTO t VALUES (1, 99)")
	if err != nil {
		t.Fatalf("INSERT OR IGNORE: %v", err)
	}
}

func TestCoverage_insertReturning(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	_, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 10) RETURNING *")
	if err != nil {
		t.Fatalf("INSERT RETURNING: %v", err)
	}
}

func TestCoverage_updateReturning(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	_, err := ex.Exec(ctx, "UPDATE t SET v = 99 WHERE id = 1 RETURNING *")
	if err != nil {
		t.Fatalf("UPDATE RETURNING: %v", err)
	}
}

func TestCoverage_deleteReturning(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, 10)")
	_, err := ex.Exec(ctx, "DELETE FROM t WHERE id = 1 RETURNING *")
	if err != nil {
		t.Fatalf("DELETE RETURNING: %v", err)
	}
}

func TestCoverage_emptyTableAgg(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	_, err := ex.QueryAll(ctx, "SELECT COUNT(*), SUM(v), AVG(v), MIN(v), MAX(v) FROM t")
	if err != nil {
		t.Fatalf("empty agg: %v", err)
	}
}

func TestCoverage_emptyTableGroupBy(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "grp", "v"}, "id")
	ctx := context.Background()
	_, err := ex.QueryAll(ctx, "SELECT grp, COUNT(*) FROM t GROUP BY grp")
	if err != nil {
		t.Fatalf("empty GROUP BY: %v", err)
	}
}

func TestCoverage_emptyTableLimit(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	_, err := ex.QueryAll(ctx, "SELECT id FROM t LIMIT 5")
	if err != nil {
		t.Fatalf("empty LIMIT: %v", err)
	}
}

func TestCoverage_emptyTableDistinct(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	_, err := ex.QueryAll(ctx, "SELECT DISTINCT v FROM t")
	if err != nil {
		t.Fatalf("empty DISTINCT: %v", err)
	}
}

func TestCoverage_emptyTableOrderBy(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	_, err := ex.QueryAll(ctx, "SELECT id FROM t ORDER BY v")
	if err != nil {
		t.Fatalf("empty ORDER BY: %v", err)
	}
}

func TestCoverage_emptyTableUnionAll(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")
	ctx := context.Background()
	_, err := ex.QueryAll(ctx, "SELECT id FROM t UNION ALL SELECT id FROM t")
	if err != nil {
		t.Fatalf("empty UNION ALL: %v", err)
	}
}

func TestCoverage_allNullAggregate(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "v"}, "id")
	ctx := context.Background()
	ex.Exec(ctx, "INSERT INTO t VALUES (1, NULL)")
	ex.Exec(ctx, "INSERT INTO t VALUES (2, NULL)")
	_, err := ex.QueryAll(ctx, "SELECT COUNT(*), SUM(v), AVG(v) FROM t")
	if err != nil {
		t.Fatalf("all NULL agg: %v", err)
	}
}

func TestCoverage_havingEmpty(t *testing.T) {
	ResetForTest(t)
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "grp", "v"}, "id")
	ctx := context.Background()
	rows, err := ex.QueryAll(ctx, "SELECT grp, COUNT(*) FROM t GROUP BY grp HAVING COUNT(*) > 1")
	if err != nil {
		t.Fatalf("HAVING empty: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}
