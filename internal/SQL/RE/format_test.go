package RE

import (
	"strings"
	"testing"
)

func TestFormatCompound(t *testing.T) {
	cases := []string{
		"SELECT 1 UNION SELECT 2",
		"SELECT 1 UNION ALL SELECT 2",
		"SELECT 1 INTERSECT SELECT 2",
		"SELECT 1 EXCEPT SELECT 2",
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

func TestFormatCreateTrigger(t *testing.T) {
	stmt := mustParse(t, "CREATE TRIGGER t AFTER INSERT ON t1 BEGIN SELECT 1; END")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "CREATE TRIGGER") {
		t.Errorf("Format output missing CREATE TRIGGER: %s", out)
	}
}

func TestFormatWithRecursive(t *testing.T) {
	stmt := mustParse(t, "WITH RECURSIVE cnt(x) AS (SELECT 1) SELECT x FROM cnt")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "RECURSIVE") {
		t.Errorf("Format output missing RECURSIVE: %s", out)
	}
}

func TestFormatAlterTable(t *testing.T) {
	stmt := mustParse(t, "ALTER TABLE t1 ADD COLUMN f INTEGER")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "ALTER TABLE") {
		t.Errorf("Format output missing ALTER TABLE: %s", out)
	}
}

func TestFormatSelectStar(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "*") {
		t.Errorf("Format output missing *: %s", out)
	}
}

func TestFormatSelectWhere(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 WHERE b > 10")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "WHERE") {
		t.Errorf("Format output missing WHERE: %s", out)
	}
}

func TestFormatSelectOrderBy(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 ORDER BY a DESC")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "ORDER BY") {
		t.Errorf("Format output missing ORDER BY: %s", out)
	}
}

func TestFormatSelectLimitOffset(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 LIMIT 10 OFFSET 5")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "LIMIT") || !strings.Contains(out, "OFFSET") {
		t.Errorf("Format output missing LIMIT/OFFSET: %s", out)
	}
}

func TestFormatSelectDistinct(t *testing.T) {
	stmt := mustParse(t, "SELECT DISTINCT a FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "DISTINCT") {
		t.Errorf("Format output missing DISTINCT: %s", out)
	}
}

func TestFormatSelectGroupByHaving(t *testing.T) {
	stmt := mustParse(t, "SELECT a, COUNT(*) FROM t1 GROUP BY a HAVING COUNT(*) > 1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "SELECT") {
		t.Errorf("Format output missing GROUP BY/HAVING: %s", out)
	}
}

func TestFormatSelectJoin(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 INNER JOIN t2 ON t1.id = t2.id")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "SELECT") {
		t.Errorf("Format output missing JOIN: %s", out)
	}
}

func TestFormatSelectSubquery(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM (SELECT 1 AS a) AS sub")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "SELECT") {
		t.Errorf("Format output missing SELECT: %s", out)
	}
}

func TestFormatSelectAggregate(t *testing.T) {
	stmt := mustParse(t, "SELECT COUNT(*), SUM(a), AVG(b) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "COUNT") || !strings.Contains(out, "SUM") || !strings.Contains(out, "AVG") {
		t.Errorf("Format output missing aggregates: %s", out)
	}
}

func TestFormatSelectWindowFunction(t *testing.T) {
	stmt := mustParse(t, "SELECT ROW_NUMBER() OVER (ORDER BY a) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "SELECT") {
		t.Errorf("Format output missing SELECT: %s", out)
	}
}

func TestFormatSelectCase(t *testing.T) {
	stmt := mustParse(t, "SELECT CASE WHEN a > 10 THEN 'high' ELSE 'low' END FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "CASE") || !strings.Contains(out, "WHEN") || !strings.Contains(out, "ELSE") || !strings.Contains(out, "END") {
		t.Errorf("Format output missing CASE expression: %s", out)
	}
}

func TestFormatSelectBetween(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 WHERE a BETWEEN 1 AND 10")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "BETWEEN") {
		t.Errorf("Format output missing BETWEEN: %s", out)
	}
}

func TestFormatSelectIn(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 WHERE a IN (1, 2, 3)")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if strings.Contains(out, "error") {
		t.Errorf("Format output missing IN: %s", out)
	}
}

func TestFormatSelectLike(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 WHERE a LIKE '%test%'")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "LIKE") {
		t.Errorf("Format output missing LIKE: %s", out)
	}
}

func TestFormatSelectIs(t *testing.T) {
	stmt := mustParse(t, "SELECT a FROM t1 WHERE a IS NULL")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "IS NULL") {
		t.Errorf("Format output missing IS NULL: %s", out)
	}
}

func TestFormatSelectCast(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS INTEGER) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "CAST") {
		t.Errorf("Format output missing CAST: %s", out)
	}
}

func TestFormatSelectFunction(t *testing.T) {
	stmt := mustParse(t, "SELECT ABS(a) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "ABS") {
		t.Errorf("Format output missing ABS: %s", out)
	}
}

func TestFormatSelectParam(t *testing.T) {
	stmt := mustParse(t, "SELECT ? FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "?") {
		t.Errorf("Format output missing ?: %s", out)
	}
}

func TestFormatSelectStringLiteral(t *testing.T) {
	stmt := mustParse(t, "SELECT 'hello' FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "'hello'") {
		t.Errorf("Format output missing string literal: %s", out)
	}
}

func TestFormatSelectNullLiteral(t *testing.T) {
	stmt := mustParse(t, "SELECT NULL FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "NULL") {
		t.Errorf("Format output missing NULL: %s", out)
	}
}

func TestFormatSelectBoolLiteral(t *testing.T) {
	stmt := mustParse(t, "SELECT TRUE FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "TRUE") {
		t.Errorf("Format output missing TRUE: %s", out)
	}
}

func TestFormatSelectNumberLiteral(t *testing.T) {
	stmt := mustParse(t, "SELECT 42 FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("Format output missing 42: %s", out)
	}
}

func TestFormatSelectFloatLiteral(t *testing.T) {
	stmt := mustParse(t, "SELECT 3.14 FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "3.14") {
		t.Errorf("Format output missing 3.14: %s", out)
	}
}

func TestFormatSelectBinaryExpr(t *testing.T) {
	stmt := mustParse(t, "SELECT a + b FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "+") {
		t.Errorf("Format output missing +: %s", out)
	}
}

func TestFormatSelectUnaryExpr(t *testing.T) {
	stmt := mustParse(t, "SELECT -a FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "-") {
		t.Errorf("Format output missing -: %s", out)
	}
}

func TestFormatSelectAliasedExpr(t *testing.T) {
	stmt := mustParse(t, "SELECT a AS b FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "AS") {
		t.Errorf("Format output missing AS: %s", out)
	}
}

func TestFormatSelectQualifiedName(t *testing.T) {
	stmt := mustParse(t, "SELECT t1.a FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if strings.Contains(out, "error") {
		t.Errorf("Format output missing t1.a: %s", out)
	}
}

func TestFormatCompoundIntersect(t *testing.T) {
	stmt := mustParse(t, "SELECT 1 INTERSECT SELECT 2")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "INTERSECT") {
		t.Errorf("Format output missing INTERSECT: %s", out)
	}
}

func TestFormatCompoundExcept(t *testing.T) {
	stmt := mustParse(t, "SELECT 1 EXCEPT SELECT 2")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "EXCEPT") {
		t.Errorf("Format output missing EXCEPT: %s", out)
	}
}

func TestFormatCompoundWithOrderBy(t *testing.T) {
	stmt := mustParse(t, "SELECT 1 UNION SELECT 2 ORDER BY 1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "ORDER BY") {
		t.Errorf("Format output missing ORDER BY: %s", out)
	}
}

func TestFormatCompoundWithLimit(t *testing.T) {
	stmt := mustParse(t, "SELECT 1 UNION SELECT 2 LIMIT 10")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "LIMIT") {
		t.Errorf("Format output missing LIMIT: %s", out)
	}
}

func TestFormatCompoundWithOffset(t *testing.T) {
	stmt := mustParse(t, "SELECT 1 UNION SELECT 2 LIMIT 10 OFFSET 5")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "OFFSET") {
		t.Errorf("Format output missing OFFSET: %s", out)
	}
}

func TestFormatCompoundThreeWay(t *testing.T) {
	stmt := mustParse(t, "SELECT 1 UNION SELECT 2 UNION SELECT 3")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "UNION") {
		t.Errorf("Format output missing UNION: %s", out)
	}
}

func TestFormatTypeNameInt(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS INTEGER) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "INTEGER") {
		t.Errorf("Format output missing INTEGER: %s", out)
	}
}

func TestFormatTypeNameText(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS TEXT) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "TEXT") {
		t.Errorf("Format output missing TEXT: %s", out)
	}
}

func TestFormatTypeNameReal(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS REAL) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if strings.Contains(out, "error") {
		t.Errorf("Format output missing REAL: %s", out)
	}
}

func TestFormatTypeNameBlob(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS BLOB) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "BLOB") {
		t.Errorf("Format output missing BLOB: %s", out)
	}
}

func TestFormatTypeNameNumeric(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS NUMERIC) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if strings.Contains(out, "error") {
		t.Errorf("Format output missing NUMERIC: %s", out)
	}
}

func TestFormatTypeNameVarchar(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS VARCHAR(100)) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(out, "VARCHAR") {
		t.Errorf("Format output missing VARCHAR: %s", out)
	}
}

func TestFormatTypeNameDecimal(t *testing.T) {
	stmt := mustParse(t, "SELECT CAST(a AS DECIMAL(10, 2)) FROM t1")
	out, err := Format(stmt)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if strings.Contains(out, "error") {
		t.Errorf("Format output missing DECIMAL: %s", out)
	}
}
