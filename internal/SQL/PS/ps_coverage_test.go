package PS

import (
	"testing"
)

func parse(sql string) (Stmt, error) {
	return NewParser(sql).Parse()
}

func mustParse(t *testing.T, sql string) Stmt {
	t.Helper()
	stmt, err := parse(sql)
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	return stmt
}

// === Window functions ===

func TestParse_WindowFunc_ROW_NUMBER(t *testing.T) {
	mustParse(t, "SELECT ROW_NUMBER() OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_RANK(t *testing.T) {
	mustParse(t, "SELECT RANK() OVER (PARTITION BY grp ORDER BY val) FROM t")
}

func TestParse_WindowFunc_DENSE_RANK(t *testing.T) {
	mustParse(t, "SELECT DENSE_RANK() OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_LAG(t *testing.T) {
	mustParse(t, "SELECT LAG(val, 1) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_LAG_with_default(t *testing.T) {
	mustParse(t, "SELECT LAG(val, 2, 0) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_LEAD(t *testing.T) {
	mustParse(t, "SELECT LEAD(val, 1) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_LEAD_with_default(t *testing.T) {
	mustParse(t, "SELECT LEAD(val, 3, -1) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_FIRST_VALUE(t *testing.T) {
	mustParse(t, "SELECT FIRST_VALUE(val) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_LAST_VALUE(t *testing.T) {
	mustParse(t, "SELECT LAST_VALUE(val) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_NTH_VALUE(t *testing.T) {
	mustParse(t, "SELECT NTH_VALUE(val, 3) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFrame_ROWS(t *testing.T) {
	mustParse(t, "SELECT SUM(v) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t")
}

func TestParse_WindowFrame_ROWS_range(t *testing.T) {
	mustParse(t, "SELECT SUM(v) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t")
}

func TestParse_WindowFrame_ROWS_current(t *testing.T) {
	mustParse(t, "SELECT SUM(v) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t")
}

func TestParse_WindowFrame_ROWS_empty(t *testing.T) {
	mustParse(t, "SELECT COUNT(*) OVER (ORDER BY id) FROM t")
}

func TestParse_WindowFunc_no_partition(t *testing.T) {
	mustParse(t, "SELECT ROW_NUMBER() OVER (ORDER BY a, b DESC) FROM t")
}

func TestParse_WindowFunc_partition_only(t *testing.T) {
	mustParse(t, "SELECT COUNT(*) OVER (PARTITION BY x) FROM t")
}

// === SAVEPOINT / RELEASE / ROLLBACK TO ===

func TestParse_Savepoint(t *testing.T) {
	stmt := mustParse(t, "SAVEPOINT sp1")
	if _, ok := stmt.(*SavepointStmt); !ok {
		t.Fatalf("expected *SavepointStmt, got %T", stmt)
	}
}

func TestParse_ReleaseSavepoint(t *testing.T) {
	stmt := mustParse(t, "RELEASE sp1")
	if _, ok := stmt.(*ReleaseSavepointStmt); !ok {
		t.Fatalf("expected *ReleaseSavepointStmt, got %T", stmt)
	}
}

func TestParse_RollbackTo(t *testing.T) {
	stmt := mustParse(t, "ROLLBACK TO sp1")
	if _, ok := stmt.(*RollbackToStmt); !ok {
		t.Fatalf("expected *RollbackToStmt, got %T", stmt)
	}
}

func TestParse_RollbackTo_savepoint(t *testing.T) {
	stmt := mustParse(t, "ROLLBACK TO SAVEPOINT sp1")
	if _, ok := stmt.(*RollbackToStmt); !ok {
		t.Fatalf("expected *RollbackToStmt, got %T", stmt)
	}
}

// === EXPLAIN ===

func TestParse_Explain(t *testing.T) {
	stmt := mustParse(t, "EXPLAIN SELECT 1")
	if _, ok := stmt.(*ExplainStmt); !ok {
		t.Fatalf("expected *ExplainStmt, got %T", stmt)
	}
}

func TestParse_ExplainQueryPlan(t *testing.T) {
	stmt := mustParse(t, "EXPLAIN QUERY PLAN SELECT 1")
	if _, ok := stmt.(*ExplainStmt); !ok {
		t.Fatalf("expected *ExplainStmt, got %T", stmt)
	}
}

// === TRUNCATE ===

func TestParse_Truncate(t *testing.T) {
	stmt := mustParse(t, "TRUNCATE TABLE t")
	if _, ok := stmt.(*TruncateStmt); !ok {
		t.Fatalf("expected *TruncateStmt, got %T", stmt)
	}
}

func TestParse_Truncate_no_table(t *testing.T) {
	stmt := mustParse(t, "TRUNCATE t")
	if _, ok := stmt.(*TruncateStmt); !ok {
		t.Fatalf("expected *TruncateStmt, got %T", stmt)
	}
}

// === REINDEX ===

func TestParse_Reindex(t *testing.T) {
	stmt := mustParse(t, "REINDEX")
	if _, ok := stmt.(*ReindexStmt); !ok {
		t.Fatalf("expected *ReindexStmt, got %T", stmt)
	}
}

func TestParse_Reindex_table(t *testing.T) {
	stmt := mustParse(t, "REINDEX t")
	if _, ok := stmt.(*ReindexStmt); !ok {
		t.Fatalf("expected *ReindexStmt, got %T", stmt)
	}
}

// === NOT LIKE / NOT IN / NOT BETWEEN ===

func TestParse_NotLike(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t WHERE a NOT LIKE '%x%'")
	sel := stmt.(*Select)
	if sel.Where == nil {
		t.Fatal("expected WHERE clause")
	}
}

func TestParse_NotIn(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t WHERE a NOT IN (1, 2, 3)")
	sel := stmt.(*Select)
	if sel.Where == nil {
		t.Fatal("expected WHERE clause")
	}
}

func TestParse_NotBetween(t *testing.T) {
	stmt := mustParse(t, "SELECT * FROM t WHERE a NOT BETWEEN 1 AND 10")
	sel := stmt.(*Select)
	if sel.Where == nil {
		t.Fatal("expected WHERE clause")
	}
}

// === COALESCE / NULLIF ===

func TestParse_Coalesce(t *testing.T) {
	mustParse(t, "SELECT COALESCE(a, b, c, 0) FROM t")
}

func TestParse_Nullif(t *testing.T) {
	mustParse(t, "SELECT NULLIF(a, b) FROM t")
}

// === DROP TABLE IF EXISTS ===

func TestParse_DropTable_IfExists(t *testing.T) {
	stmt := mustParse(t, "DROP TABLE IF EXISTS t")
	dt := stmt.(*DropTable)
	if !dt.IfExists {
		t.Error("expected IfExists=true")
	}
}

func TestParse_DropTable(t *testing.T) {
	stmt := mustParse(t, "DROP TABLE t")
	dt := stmt.(*DropTable)
	if dt.IfExists {
		t.Error("expected IfExists=false")
	}
}

// === CREATE INDEX IF NOT EXISTS ===

func TestParse_CreateIndex_IfNotExists(t *testing.T) {
	stmt := mustParse(t, "CREATE INDEX IF NOT EXISTS idx ON t (a)")
	ci := stmt.(*CreateIndexStmt)
	if !ci.IfExists {
		t.Error("expected IfExists=true")
	}
}

func TestParse_CreateIndex(t *testing.T) {
	stmt := mustParse(t, "CREATE INDEX idx ON t (a, b)")
	ci := stmt.(*CreateIndexStmt)
	if ci.IfExists {
		t.Error("expected IfExists=false")
	}
}

func TestParse_CreateUniqueIndex(t *testing.T) {
	stmt := mustParse(t, "CREATE UNIQUE INDEX idx ON t (a)")
	ci := stmt.(*CreateIndexStmt)
	if !ci.Unique {
		t.Error("expected Unique=true")
	}
}

// === DROP INDEX IF EXISTS ===

func TestParse_DropIndex_IfExists(t *testing.T) {
	stmt := mustParse(t, "DROP INDEX IF EXISTS idx")
	di := stmt.(*DropIndexStmt)
	if !di.IfExists {
		t.Error("expected IfExists=true")
	}
}

func TestParse_DropIndex(t *testing.T) {
	stmt := mustParse(t, "DROP INDEX idx")
	di := stmt.(*DropIndexStmt)
	if di.IfExists {
		t.Error("expected IfExists=false")
	}
}

// === ALTER TABLE RENAME COLUMN ===

func TestParse_AlterTable_RenameColumn(t *testing.T) {
	stmt := mustParse(t, "ALTER TABLE t RENAME COLUMN a TO b")
	at := stmt.(*AlterTableStmt)
	if at.Action != "RENAME COLUMN" {
		t.Errorf("action=%q, want RENAME COLUMN", at.Action)
	}
	if at.Column != "a" {
		t.Errorf("column=%q, want a", at.Column)
	}
	if at.NewName != "b" {
		t.Errorf("newName=%q, want b", at.NewName)
	}
}

func TestParse_AlterTable_RenameTable(t *testing.T) {
	stmt := mustParse(t, "ALTER TABLE t RENAME TO new_t")
	at := stmt.(*AlterTableStmt)
	if at.Action != "RENAME" {
		t.Errorf("action=%q, want RENAME", at.Action)
	}
}

// === AUTOINCREMENT ===

func TestParse_CreateTable_Autoincrement(t *testing.T) {
	stmt := mustParse(t, "CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT)")
	ct := stmt.(*CreateTable)
	if len(ct.Cols) != 1 {
		t.Fatalf("expected 1 col, got %d", len(ct.Cols))
	}
	if !ct.Cols[0].Autoincrement {
		t.Error("expected Autoincrement=true")
	}
	if !ct.Cols[0].PK {
		t.Error("expected PK=true")
	}
}

func TestParse_CreateTable_CompositePK(t *testing.T) {
	stmt := mustParse(t, "CREATE TABLE t (a INT, b INT, PRIMARY KEY (a, b))")
	ct := stmt.(*CreateTable)
	if ct.PK == nil || *ct.PK != "a" {
		t.Errorf("PK=%v, want a", ct.PK)
	}
}

// === UNION / INTERSECT / EXCEPT ===

func TestParse_Union(t *testing.T) {
	mustParse(t, "SELECT 1 UNION SELECT 2")
}

func TestParse_UnionAll(t *testing.T) {
	mustParse(t, "SELECT 1 UNION ALL SELECT 2")
}

func TestParse_Intersect(t *testing.T) {
	mustParse(t, "SELECT 1 INTERSECT SELECT 2")
}

func TestParse_Except(t *testing.T) {
	mustParse(t, "SELECT 1 EXCEPT SELECT 2")
}

// === Subquery in FROM ===

func TestParse_FromSubquery(t *testing.T) {
	mustParse(t, "SELECT a FROM (SELECT 1 AS a) sub")
}

func TestParse_FromSubqueryAliased(t *testing.T) {
	mustParse(t, "SELECT sub.a FROM (SELECT 1 AS a) AS sub")
}

// === CASE expression ===

func TestParse_CaseSimple(t *testing.T) {
	mustParse(t, "SELECT CASE a WHEN 1 THEN 'x' WHEN 2 THEN 'y' ELSE 'z' END FROM t")
}

func TestParse_CaseSearched(t *testing.T) {
	mustParse(t, "SELECT CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END FROM t")
}

func TestParse_CaseNoElse(t *testing.T) {
	mustParse(t, "SELECT CASE a WHEN 1 THEN 'x' END FROM t")
}

// === CAST ===

func TestParse_Cast(t *testing.T) {
	mustParse(t, "SELECT CAST(a AS INTEGER) FROM t")
}

func TestParse_CastText(t *testing.T) {
	mustParse(t, "SELECT CAST(a AS TEXT) FROM t")
}

func TestParse_CastBlob(t *testing.T) {
	mustParse(t, "SELECT CAST(a AS BLOB) FROM t")
}

func TestParse_CastReal(t *testing.T) {
	mustParse(t, "SELECT CAST(a AS REAL) FROM t")
}

// === LIMIT / OFFSET ===

func TestParse_LimitOffset(t *testing.T) {
	mustParse(t, "SELECT * FROM t LIMIT 10 OFFSET 5")
}

func TestParse_LimitOnly(t *testing.T) {
	mustParse(t, "SELECT * FROM t LIMIT 10")
}

func TestParse_OffsetOnly(t *testing.T) {
	mustParse(t, "SELECT * FROM t OFFSET 5")
}

// === IS NULL / IS NOT NULL ===

func TestParse_IsNull(t *testing.T) {
	mustParse(t, "SELECT * FROM t WHERE a IS NULL")
}

func TestParse_IsNotNull(t *testing.T) {
	mustParse(t, "SELECT * FROM t WHERE a IS NOT NULL")
}

// === BETWEEN ===

func TestParse_Between(t *testing.T) {
	mustParse(t, "SELECT * FROM t WHERE a BETWEEN 1 AND 10")
}

// === IN with subquery ===

func TestParse_InSubquery(t *testing.T) {
	mustParse(t, "SELECT * FROM t WHERE a IN (SELECT b FROM s)")
}

// === EXISTS subquery ===

func TestParse_ExistsSubquery(t *testing.T) {
	mustParse(t, "SELECT * FROM t WHERE EXISTS (SELECT 1 FROM s)")
}

// === CTE (WITH) ===

func TestParse_WithCTE(t *testing.T) {
	mustParse(t, "WITH cte AS (SELECT 1) SELECT * FROM cte")
}

func TestParse_WithCTEColumns(t *testing.T) {
	mustParse(t, "WITH cte(x, y) AS (SELECT 1, 2) SELECT * FROM cte")
}

func TestParse_MultipleCTE(t *testing.T) {
	mustParse(t, "WITH a AS (SELECT 1), b AS (SELECT 2) SELECT * FROM a, b")
}

// === VALUES ===

// === ORDER BY expression ===

func TestParse_OrderByExpression(t *testing.T) {
	mustParse(t, "SELECT * FROM t ORDER BY a + b")
}

func TestParse_OrderByDesc(t *testing.T) {
	mustParse(t, "SELECT * FROM t ORDER BY a DESC")
}

func TestParse_OrderByAsc(t *testing.T) {
	mustParse(t, "SELECT * FROM t ORDER BY a ASC")
}

func TestParse_OrderByMultiple(t *testing.T) {
	mustParse(t, "SELECT * FROM t ORDER BY a, b DESC, c ASC")
}

// === HAVING ===

func TestParse_Having(t *testing.T) {
	mustParse(t, "SELECT a, COUNT(*) FROM t GROUP BY a HAVING COUNT(*) > 1")
}

// === DISTINCT ===

func TestParse_Distinct(t *testing.T) {
	mustParse(t, "SELECT DISTINCT a FROM t")
}

// === Aggregate functions ===

func TestParse_CountStar(t *testing.T) {
	mustParse(t, "SELECT COUNT(*) FROM t")
}

func TestParse_Sum(t *testing.T) {
	mustParse(t, "SELECT SUM(a) FROM t")
}

func TestParse_Avg(t *testing.T) {
	mustParse(t, "SELECT AVG(a) FROM t")
}

func TestParse_Min(t *testing.T) {
	mustParse(t, "SELECT MIN(a) FROM t")
}

func TestParse_Max(t *testing.T) {
	mustParse(t, "SELECT MAX(a) FROM t")
}

func TestParse_GroupConcat(t *testing.T) {
	mustParse(t, "SELECT GROUP_CONCAT(a) FROM t")
}

func TestParse_GroupConcatSep(t *testing.T) {
	mustParse(t, "SELECT GROUP_CONCAT(a, '-') FROM t")
}

func TestParse_GroupConcatDistinct(t *testing.T) {
	mustParse(t, "SELECT GROUP_CONCAT(DISTINCT a) FROM t")
}

// === INSERT variations ===

func TestParse_InsertOrIgnore(t *testing.T) {
	stmt := mustParse(t, "INSERT OR IGNORE INTO t (a) VALUES (1)")
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionIgnore {
		t.Errorf("conflict=%v, want Ignore", ins.ConflictAction)
	}
}

func TestParse_InsertOrReplace(t *testing.T) {
	stmt := mustParse(t, "INSERT OR REPLACE INTO t (a) VALUES (1)")
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionReplace {
		t.Errorf("conflict=%v, want Replace", ins.ConflictAction)
	}
}

func TestParse_InsertOrAbort(t *testing.T) {
	stmt := mustParse(t, "INSERT OR ABORT INTO t (a) VALUES (1)")
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionAbort {
		t.Errorf("conflict=%v, want Abort", ins.ConflictAction)
	}
}

func TestParse_InsertOrRollback(t *testing.T) {
	stmt := mustParse(t, "INSERT OR ROLLBACK INTO t (a) VALUES (1)")
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionRollback {
		t.Errorf("conflict=%v, want Rollback", ins.ConflictAction)
	}
}

func TestParse_InsertOrFail(t *testing.T) {
	stmt := mustParse(t, "INSERT OR FAIL INTO t (a) VALUES (1)")
	ins := stmt.(*Insert)
	if ins.ConflictAction != ConflictActionFail {
		t.Errorf("conflict=%v, want Fail", ins.ConflictAction)
	}
}

func TestParse_InsertOnConflictDoNothing(t *testing.T) {
	stmt := mustParse(t, "INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO NOTHING")
	ins := stmt.(*Insert)
	if ins.OnConflict == nil {
		t.Fatal("expected OnConflict")
	}
	if !ins.OnConflict.DoNothing {
		t.Error("expected DoNothing=true")
	}
}

func TestParse_InsertOnConflictDoUpdate(t *testing.T) {
	stmt := mustParse(t, "INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET a = EXCLUDED.a")
	ins := stmt.(*Insert)
	if ins.OnConflict == nil {
		t.Fatal("expected OnConflict")
	}
	if len(ins.OnConflict.SetClauses) == 0 {
		t.Error("expected SET clauses")
	}
}

func TestParse_InsertReturning(t *testing.T) {
	stmt := mustParse(t, "INSERT INTO t (a) VALUES (1) RETURNING *")
	ins := stmt.(*Insert)
	if len(ins.Returning) != 1 {
		t.Errorf("returning=%d, want 1", len(ins.Returning))
	}
}

func TestParse_InsertReturningCols(t *testing.T) {
	stmt := mustParse(t, "INSERT INTO t (a, b) VALUES (1, 2) RETURNING a, b")
	ins := stmt.(*Insert)
	if len(ins.Returning) != 2 {
		t.Errorf("returning=%d, want 2", len(ins.Returning))
	}
}

// === UPDATE variations ===

func TestParse_UpdateReturning(t *testing.T) {
	stmt := mustParse(t, "UPDATE t SET a = 1 RETURNING *")
	_ = stmt
}

// === DELETE variations ===

func TestParse_DeleteReturning(t *testing.T) {
	mustParse(t, "DELETE FROM t WHERE a = 1 RETURNING *")
}

func TestParse_DeleteReturningCols(t *testing.T) {
	mustParse(t, "DELETE FROM t WHERE a = 1 RETURNING a")
}

// === CREATE VIEW ===

func TestParse_CreateViewFull(t *testing.T) {
	mustParse(t, "CREATE VIEW v AS SELECT 1")
}

func TestParse_DropView(t *testing.T) {
	mustParse(t, "DROP VIEW v")
}

func TestParse_DropViewIfExists(t *testing.T) {
	stmt := mustParse(t, "DROP VIEW IF EXISTS v")
	dv := stmt.(*DropViewStmt)
	if !dv.IfExists {
		t.Error("expected IfExists=true")
	}
}

// === CREATE TRIGGER ===

func TestParse_CreateTrigger(t *testing.T) {
	mustParse(t, "CREATE TRIGGER trg AFTER INSERT ON t BEGIN SELECT 1; END")
}

func TestParse_CreateTriggerBefore(t *testing.T) {
	mustParse(t, "CREATE TRIGGER trg BEFORE DELETE ON t BEGIN SELECT 1; END")
}

func TestParse_CreateTriggerInsteadOf(t *testing.T) {
	mustParse(t, "CREATE TRIGGER trg INSTEAD OF UPDATE ON t BEGIN SELECT 1; END")
}

func TestParse_DropTrigger(t *testing.T) {
	mustParse(t, "DROP TRIGGER trg")
}

func TestParse_DropTriggerIfExists(t *testing.T) {
	stmt := mustParse(t, "DROP TRIGGER IF EXISTS trg")
	dt := stmt.(*DropTriggerStmt)
	if !dt.IfExists {
		t.Error("expected IfExists=true")
	}
}

// === CREATE TABLE AS SELECT ===

func TestParse_CreateTableAsSelect(t *testing.T) {
	mustParse(t, "CREATE TABLE t AS SELECT 1 AS a, 2 AS b")
}

func TestParse_CreateTableAsSelectWhere(t *testing.T) {
	mustParse(t, "CREATE TABLE t AS SELECT * FROM s WHERE a > 1")
}

// === Nested subqueries ===

func TestParse_ScalarSubquery(t *testing.T) {
	mustParse(t, "SELECT (SELECT COUNT(*) FROM s) FROM t")
}

func TestParse_ExistsInWhere(t *testing.T) {
	mustParse(t, "SELECT * FROM t WHERE EXISTS (SELECT 1 FROM s WHERE s.id = t.id)")
}

// === Error cases ===

func TestParse_ErrorOnEmpty(t *testing.T) {
	_, err := parse("")
	if err == nil {
		t.Error("expected error on empty input")
	}
}

func TestParse_ErrorOnInvalid(t *testing.T) {
	_, err := parse("NOT VALID SQL")
	if err == nil {
		t.Error("expected error on invalid SQL")
	}
}

func TestParse_ErrorOnSyntax(t *testing.T) {
	_, err := parse("SELECT FROM WHERE")
	if err == nil {
		t.Error("expected syntax error")
	}
}

func TestParse_ErrorOnUnclosedParen(t *testing.T) {
	_, err := parse("SELECT (1")
	if err == nil {
		t.Error("expected error on unclosed paren")
	}
}

// === Multiple statements ===

// === Pragma ===

func TestParse_Pragma(t *testing.T) {
	stmt := mustParse(t, "PRAGMA cache_size")
	if _, ok := stmt.(*PragmaStmt); !ok {
		t.Fatalf("expected *PragmaStmt, got %T", stmt)
	}
}

func TestParse_PragmaWithValue(t *testing.T) {
	stmt := mustParse(t, "PRAGMA journal_mode = WAL")
	ps := stmt.(*PragmaStmt)
	if ps.Value != "WAL" {
		t.Errorf("value=%q, want WAL", ps.Value)
	}
}

// === EXPLAIN ===

func TestParse_ExplainDetailed(t *testing.T) {
	stmt := mustParse(t, "EXPLAIN SELECT * FROM t WHERE a > 1")
	if _, ok := stmt.(*ExplainStmt); !ok {
		t.Fatalf("expected *ExplainStmt, got %T", stmt)
	}
}
