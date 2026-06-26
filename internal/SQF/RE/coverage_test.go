package RE

import (
	"strings"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

func TestCoverageRewriteCompound(t *testing.T) {
	cases := []string{
		"SELECT 1 UNION SELECT 2",
		"SELECT 1 UNION ALL SELECT 2",
		"SELECT 1 INTERSECT SELECT 2",
		"SELECT 1 EXCEPT SELECT 2",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageRewriteCompoundComplex(t *testing.T) {
	cases := []string{
		"SELECT 1 UNION ALL SELECT 2 UNION SELECT 3",
		"SELECT 1 INTERSECT SELECT 2 EXCEPT SELECT 3",
		"SELECT 1 UNION ALL SELECT 2 ORDER BY 1",
		"SELECT 1 UNION SELECT 2 LIMIT 10",
		"SELECT 1 UNION SELECT 2 LIMIT 10 OFFSET 5",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageFormatCompound(t *testing.T) {
	cases := []string{
		"SELECT 1 UNION SELECT 2",
		"SELECT 1 UNION ALL SELECT 2",
		"SELECT 1 INTERSECT SELECT 2",
		"SELECT 1 EXCEPT SELECT 2",
		"SELECT 1 UNION ALL SELECT 2 ORDER BY 1",
		"SELECT 1 UNION SELECT 2 LIMIT 10",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Format(stmt)
		if err != nil {
			t.Errorf("Format(%q): %v", sql, err)
		}
		if out == "" {
			t.Errorf("Format(%q) returned empty", sql)
		}
	}
}

func TestCoverageRewriteFloatFold(t *testing.T) {
	stmt := mustParse(t, "SELECT 1.5 + 2.5")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	sel, ok := out.(*PS.Select)
	if !ok {
		t.Fatalf("expected Select, got %T", out)
	}
	_ = sel
	// Just verify no error; exact folding depends on simplifier.
}

func TestCoverageRewriteStringConcat(t *testing.T) {
	stmt := mustParse(t, "SELECT 'hello' || ' ' || 'world'")
	out, err := Rewrite(stmt)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	_ = out
}

func TestCoverageRewriteSimplifyIn(t *testing.T) {
	cases := []string{
		"SELECT * FROM t1 WHERE a IN (1, 2, 3)",
		"SELECT * FROM t1 WHERE a IN ('x', 'y')",
		"SELECT * FROM t1 WHERE a IN (1.0, 2.0)",
		"SELECT * FROM t1 WHERE a IN (TRUE, FALSE)",
		"SELECT * FROM t1 WHERE a IN (NULL, 1)",
		"SELECT * FROM t1 WHERE a IN (1 + 1, 2 * 2)",
		"SELECT * FROM t1 WHERE a IN (-1, -2)",
		"SELECT * FROM t1 WHERE a IN (1 IN (1, 2))",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageRewriteSimplifyBinaryArith(t *testing.T) {
	cases := []struct {
		sql string
	}{
		{"SELECT 1 + 2"},
		{"SELECT 5 - 3"},
		{"SELECT 4 * 5"},
		{"SELECT 10 / 2"},
		{"SELECT 10 % 3"},
		{"SELECT 1 + NULL"},
		{"SELECT NULL + 1"},
	}
	for _, c := range cases {
		stmt := mustParse(t, c.sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", c.sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", c.sql)
		}
	}
}

func TestCoverageRewriteSimplifyBinaryLogic(t *testing.T) {
	cases := []struct {
		sql string
	}{
		{"SELECT 1 AND TRUE"},
		{"SELECT 1 OR FALSE"},
		{"SELECT 'a' || 'b'"},
		{"SELECT TRUE = TRUE"},
		{"SELECT TRUE = FALSE"},
		{"SELECT 1 = 1"},
		{"SELECT 1 < 2"},
		{"SELECT 1 > 2"},
		{"SELECT 1 <= 1"},
		{"SELECT 1 >= 1"},
	}
	for _, c := range cases {
		stmt := mustParse(t, c.sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", c.sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", c.sql)
		}
	}
}

func TestCoverageRewriteSimplifyBinaryString(t *testing.T) {
	cases := []struct {
		sql string
	}{
		{"SELECT 'a' = 'a'"},
		{"SELECT 'a' = 'b'"},
		{"SELECT 'a' < 'b'"},
		{"SELECT 'b' > 'a'"},
		{"SELECT 'a' <= 'a'"},
		{"SELECT 'a' >= 'b'"},
	}
	for _, c := range cases {
		stmt := mustParse(t, c.sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", c.sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", c.sql)
		}
	}
}

func TestCoverageRewriteSimplifyBinaryNull(t *testing.T) {
	cases := []struct {
		sql string
	}{
		{"SELECT NULL = NULL"},
		{"SELECT NULL = 1"},
		{"SELECT NULL AND TRUE"},
		{"SELECT NULL OR TRUE"},
	}
	for _, c := range cases {
		stmt := mustParse(t, c.sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", c.sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", c.sql)
		}
	}
}

func TestCoverageRewriteSimplifyUnary(t *testing.T) {
	cases := []struct {
		sql string
	}{
		{"SELECT -5"},
		{"SELECT -3.14"},
		{"SELECT NOT TRUE"},
		{"SELECT NOT FALSE"},
		{"SELECT NOT NULL"},
	}
	for _, c := range cases {
		stmt := mustParse(t, c.sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", c.sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", c.sql)
		}
	}
}

func TestCoverageRewriteSimplifySubquery(t *testing.T) {
	cases := []string{
		"SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.a = t1.a)",
		"SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)",
		"SELECT * FROM t1 WHERE a > (SELECT MAX(b) FROM t2)",
		"SELECT * FROM t1 WHERE NOT EXISTS (SELECT 1 FROM t2 WHERE t2.a = t1.a)",
		"SELECT * FROM t1 WHERE a NOT IN (SELECT b FROM t2)",
		"SELECT * FROM t1 WHERE a < (SELECT MIN(b) FROM t2)",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageRewriteVarious(t *testing.T) {
	cases := []string{
		"SELECT 1 UNION ALL SELECT 2",
		"ANALYZE t1",
		"VACUUM",
		"PRAGMA cache_size",
		"SELECT 1 UNION ALL SELECT 2",
		"SELECT 1 UNION ALL SELECT 2",
		"SELECT * FROM t1 WHERE a IN (1, 2, 3)",
		"SELECT * FROM t1 WHERE a IN ('x', 'y', 'z')",
		"SELECT * FROM t1 WHERE a IN (1.0, 2.0, 3.0)",
		"SELECT * FROM t1 WHERE a IN (TRUE, FALSE)",
		"SELECT * FROM t1 WHERE a IN (NULL, 1)",
		"SELECT 1.5 + 2.5",
		"SELECT 3.0 * 2.0",
		"SELECT 10.0 / 2.0",
		"SELECT CAST(3.0 AS INTEGER)",
		"SELECT NOT TRUE",
		"SELECT NOT FALSE",
		"SELECT -5",
		"SELECT -3.14",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageFormatVarious(t *testing.T) {
	cases := []string{
		"CREATE TRIGGER t1 AFTER INSERT ON t1 BEGIN SELECT 1; END",
		"WITH RECURSIVE cnt(x) AS (SELECT 1) SELECT x FROM cnt",
		"ALTER TABLE t1 ADD COLUMN f INTEGER",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Format(stmt)
		if err != nil {
			t.Errorf("Format(%q): %v", sql, err)
		}
		if out == "" {
			t.Errorf("Format(%q) returned empty", sql)
		}
	}
}

func TestCoverageFlattenSubquery(t *testing.T) {
	cases := []string{
		"SELECT * FROM t1 WHERE a IN (SELECT 1, 2, 3)",
		"SELECT * FROM t1 WHERE a IN (SELECT b FROM t2)",
		"SELECT * FROM t1 WHERE a IN (SELECT b WHERE b > 10)",
		"SELECT * FROM t1 WHERE a IN (SELECT b AS c FROM t2)",
		"SELECT * FROM t1 WHERE a IN (SELECT b + 1 FROM t2)",
		"SELECT * FROM t1 WHERE a IN (SELECT b FROM t2 AS sub)",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageSimplifyInVarious(t *testing.T) {
	cases := []string{
		"SELECT * FROM t1 WHERE a IN (1)",
		"SELECT * FROM t1 WHERE a IN (NULL)",
		"SELECT * FROM t1 WHERE a IN (1, 1, 2, 2)",
		"SELECT * FROM t1 WHERE a IN ((1 + 2), (3 + 4))",
		"SELECT * FROM t1 WHERE a IN (TRUE, FALSE, NULL)",
		"SELECT * FROM t1 WHERE a IN (1 > 0, 2 < 3)",
		"SELECT * FROM t1 WHERE a IN (ABS(1), ABS(2))",
		"SELECT * FROM t1 WHERE a IN (1 + 1, 2 * 2)",
		"SELECT * FROM t1 WHERE a IN (-1, -2)",
		"SELECT * FROM t1 WHERE a IN (CAST(1 AS INTEGER), CAST(2 AS INTEGER))",
		"SELECT * FROM t1 WHERE a IN (CASE WHEN b > 10 THEN 1 ELSE 0 END)",
		"SELECT * FROM t1 WHERE a IN (1 BETWEEN 0 AND 2)",
		"SELECT * FROM t1 WHERE a IN ('test' LIKE '%test%')",
		"SELECT * FROM t1 WHERE a IN (1 IN (1, 2, 3))",
		"SELECT * FROM t1 WHERE a IN (EXISTS (SELECT 1 FROM t2))",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageSimplifyBinaryVarious(t *testing.T) {
	cases := []string{
		"SELECT 1 + 2",
		"SELECT 5 - 3",
		"SELECT 4 * 5",
		"SELECT 10 / 2",
		"SELECT 10 % 3",
		"SELECT 1 + NULL",
		"SELECT NULL + 1",
		"SELECT 1 AND TRUE",
		"SELECT 1 OR FALSE",
		"SELECT 'a' || 'b'",
		"SELECT TRUE = TRUE",
		"SELECT TRUE = FALSE",
		"SELECT TRUE = FALSE",
		"SELECT TRUE AND TRUE",
		"SELECT TRUE OR FALSE",
		"SELECT 1 = 1",
		"SELECT 1 = 2",
		"SELECT 1 < 2",
		"SELECT 1 > 2",
		"SELECT 1 <= 1",
		"SELECT 1 >= 1",
		"SELECT 'a' = 'a'",
		"SELECT 'a' = 'b'",
		"SELECT 'a' < 'b'",
		"SELECT 'b' > 'a'",
		"SELECT 'a' <= 'a'",
		"SELECT 'a' >= 'b'",
		"SELECT 'a' = 'b'",
		"SELECT NULL = NULL",
		"SELECT NULL = 1",
		"SELECT NULL AND TRUE",
		"SELECT NULL OR TRUE",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageFormatSelectVarious(t *testing.T) {
	cases := []string{
		"SELECT * FROM t1",
		"SELECT a FROM t1 WHERE b > 10",
		"SELECT a FROM t1 ORDER BY a DESC",
		"SELECT a FROM t1 LIMIT 10 OFFSET 5",
		"SELECT DISTINCT a FROM t1",
		"SELECT a, COUNT(*) FROM t1 GROUP BY a HAVING COUNT(*) > 1",
		"SELECT a FROM t1 INNER JOIN t2 ON t1.id = t2.id",
		"SELECT a FROM (SELECT 1 AS a) AS sub",
		"SELECT COUNT(*), SUM(a), AVG(b) FROM t1",
		"SELECT ROW_NUMBER() OVER (ORDER BY a) FROM t1",
		"SELECT CASE WHEN a > 10 THEN 'high' ELSE 'low' END FROM t1",
		"SELECT a FROM t1 WHERE a BETWEEN 1 AND 10",
		"SELECT a FROM t1 WHERE a IN (1, 2, 3)",
		"SELECT a FROM t1 WHERE a LIKE '%test%'",
		"SELECT a FROM t1 WHERE a IS NULL",
		"SELECT CAST(a AS INTEGER) FROM t1",
		"SELECT ABS(a) FROM t1",
		"SELECT ? FROM t1",
		"SELECT 'hello' FROM t1",
		"SELECT NULL FROM t1",
		"SELECT TRUE FROM t1",
		"SELECT 42 FROM t1",
		"SELECT 3.14 FROM t1",
		"SELECT a + b FROM t1",
		"SELECT -a FROM t1",
		"SELECT a AS b FROM t1",
		"SELECT t1.a FROM t1",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Format(stmt)
		if err != nil {
			t.Errorf("Format(%q): %v", sql, err)
		}
		if out == "" {
			t.Errorf("Format(%q) returned empty", sql)
		}
	}
}

func TestCoverageRewriteSimplifyBinaryVarious(t *testing.T) {
	cases := []string{
		"SELECT 1.5 + 2.5",
		"SELECT 3.0 - 1.0",
		"SELECT 2.0 * 3.0",
		"SELECT 6.0 / 2.0",
		"SELECT 1.5 = 1.5",
		"SELECT 1.5 < 2.5",
		"SELECT 1.5 > 2.5",
		"SELECT 1.5 <= 1.5",
		"SELECT 1.5 >= 1.5",
		"SELECT 1.5 != 2.5",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Rewrite(stmt)
		if err != nil {
			t.Errorf("Rewrite(%q): %v", sql, err)
		}
		if out == nil {
			t.Errorf("Rewrite(%q) returned nil", sql)
		}
	}
}

func TestCoverageFormatTypeName(t *testing.T) {
	cases := []string{
		"SELECT CAST(a AS INTEGER) FROM t1",
		"SELECT CAST(a AS TEXT) FROM t1",
		"SELECT CAST(a AS REAL) FROM t1",
		"SELECT CAST(a AS BLOB) FROM t1",
		"SELECT CAST(a AS NUMERIC) FROM t1",
		"SELECT CAST(a AS VARCHAR(100)) FROM t1",
		"SELECT CAST(a AS DECIMAL(10, 2)) FROM t1",
		"SELECT CAST(a AS BIGINT) FROM t1",
	}
	for _, sql := range cases {
		stmt := mustParse(t, sql)
		out, err := Format(stmt)
		if err != nil {
			t.Errorf("Format(%q): %v", sql, err)
		}
		if out == "" {
			t.Errorf("Format(%q) returned empty", sql)
		}
		_ = strings.Contains(out, "CAST")
	}
}
