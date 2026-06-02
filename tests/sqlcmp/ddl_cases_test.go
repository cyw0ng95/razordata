package sqlcmp

import "testing"

var ddlCases = []testCase{
	{"create_table_simple", "CREATE TABLE t (a INTEGER)", false},
	{"create_table_text", "CREATE TABLE t (a TEXT)", false},
	{"create_table_real", "CREATE TABLE t (a REAL)", false},
	{"create_table_blob", "CREATE TABLE t (a BLOB)", false},
	{"create_table_bigint", "CREATE TABLE t (a BIGINT)", false},
	{"create_table_varchar", "CREATE TABLE t (a VARCHAR)", false},
	{"create_table_timestamp", "CREATE TABLE t (a TIMESTAMP)", false},
	{"create_table_pk", "CREATE TABLE t (a INTEGER PRIMARY KEY)", false},
	{"create_table_pk_col", "CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT)", false},
	{"create_table_notnull", "CREATE TABLE t (a TEXT NOT NULL)", false},
	{"create_table_pk_notnull", "CREATE TABLE t (a INTEGER PRIMARY KEY NOT NULL)", false},
	{"create_table_pk_constraint", "CREATE TABLE t (a INTEGER, b TEXT, PRIMARY KEY (a))", false},
	{"create_table_multi_col", "CREATE TABLE t (a INTEGER, b TEXT, c REAL)", false},
	{"create_table_all_types", "CREATE TABLE t (a INTEGER, b TEXT, c REAL, d BLOB)", false},
	{"create_table_default", "CREATE TABLE t (a INTEGER DEFAULT 0)", false},
	{"create_table_unique", "CREATE TABLE t (a TEXT UNIQUE)", false},
	{"drop_table", "DROP TABLE t", false},
	{"drop_table_if_exists", "DROP TABLE IF EXISTS t", false},
	{"create_table_inline_notnull", "CREATE TABLE t (a INTEGER NOT NULL)", false},
	{"create_table_inline_unique", "CREATE TABLE t (a TEXT UNIQUE)", false},
	{"create_table_inline_default", "CREATE TABLE t (a INTEGER DEFAULT 0)", false},
}

func TestDDLRewrite(t *testing.T) {
	for _, tc := range ddlCases {
		t.Run(tc.name, tc.runRewrite)
	}
}

func TestDDLParse(t *testing.T) {
	for _, tc := range ddlCases {
		t.Run(tc.name, tc.runParse)
	}
}
