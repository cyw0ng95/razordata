package PS

import (
	"testing"
)

// REQ000742: EXPLAIN covers all statement types.
func TestREQ000742_ExplainCoversAllTypes(t *testing.T) {
	tests := []struct {
		sql string
	}{
		// All statement types that EXPLAIN should handle
		{"EXPLAIN SELECT * FROM t"},
		{"EXPLAIN INSERT INTO t VALUES (1)"},
		{"EXPLAIN UPDATE t SET v = 1 WHERE id = 1"},
		{"EXPLAIN DELETE FROM t WHERE id = 1"},
		{"EXPLAIN CREATE TABLE t (x INTEGER)"},
		{"EXPLAIN CREATE INDEX i ON t(x)"},
		{"EXPLAIN CREATE UNIQUE INDEX i ON t(x)"},
		{"EXPLAIN CREATE VIEW v AS SELECT * FROM t"},
		{"EXPLAIN CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1; END"},
		{"EXPLAIN DROP TABLE t"},
		{"EXPLAIN DROP INDEX i"},
		{"EXPLAIN DROP VIEW v"},
		{"EXPLAIN DROP TRIGGER tr"},
		{"EXPLAIN TRUNCATE TABLE t"},
		{"EXPLAIN ANALYZE"},
		{"EXPLAIN VACUUM"},
		{"EXPLAIN REINDEX"},
		//
		// Some tests might fail if the inner parse fails for
		// non-standard statements. We only check that the
		// EXPLAIN parse itself doesn't produce a syntax error.
	}
	for _, tc := range tests {
		p := NewParser(tc.sql)
		_, err := p.Parse()
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.sql, err)
		}
	}
}

func TestREQ000751_ExplainTruncate(t *testing.T) {
	p := NewParser("EXPLAIN TRUNCATE TABLE t")
	stmt, err := p.Parse()
	if err != nil {
		t.Fatalf("EXPLAIN TRUNCATE: %v", err)
	}
	e, ok := stmt.(*ExplainStmt)
	if !ok {
		t.Fatalf("expected *ExplainStmt, got %T", stmt)
	}
	if _, ok := e.Inner.(*TruncateStmt); !ok {
		t.Fatalf("inner stmt expected *TruncateStmt, got %T", e.Inner)
	}
}
