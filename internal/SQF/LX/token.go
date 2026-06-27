package LX

type TokenType int

const (
	T_EOF TokenType = iota

	T_IDENT
	T_STRING
	T_INT
	T_FLOAT
	T_BIND

	T_EQ
	T_NE
	T_LT
	T_LE
	T_GT
	T_GE

	T_PLUS
	T_MINUS
	T_STAR
	T_SLASH

	T_LPAREN
	T_RPAREN
	T_COMMA
	T_DOT
	T_SEMICOLON
	T_COLON

	T_CREATE
	T_DROP
	T_INSERT
	T_UPDATE
	T_DELETE
	T_SELECT
	T_FROM
	T_WHERE
	T_AND
	T_OR
	T_BEFORE
	T_AFTER
	T_NOT
	T_IN
	T_BETWEEN
	T_LIKE
	T_GLOB
	T_DIV
	T_IS
	T_NULL
	T_BEGIN
	T_COMMIT
	T_ROLLBACK
	T_AS
	T_BY
	T_ASC
	T_DESC
	T_LIMIT
	T_OFFSET
	T_TABLE
	T_INDEX
	T_PRIMARY
	T_KEY
	T_NOTNULL
	T_DEFAULT
	T_CHECK
	T_UNIQUE
	T_INT_KW
	T_BIGINT
	T_FLOAT_KW
	T_BOOL
	T_TEXT
	T_BLOB
	T_VARCHAR
	T_TIMESTAMP
	T_NUMERIC
	T_DATE
	T_TIME
	T_JSON
	T_DECIMAL
	T_VALUES
	T_SET
	T_INTO
	T_ORDER
	T_JOIN
	T_LEFT
	T_RIGHT
	T_INNER
	T_CROSS
	T_ON
	T_USING
	T_GROUP
	T_HAVING
	T_COUNT
	T_SUM
	T_AVG
	T_MIN
	T_MAX
	T_DISTINCT
	T_CASE
	T_WHEN
	T_THEN
	T_ELSE
	T_END
	T_EXISTS
	T_INSTEAD
	T_OF
	T_FOR
	T_EACH
	T_ANALYZE
	T_VACUUM
	T_TRUNCATE
	T_REINDEX
	T_EXPLAIN
	T_QUERY
	T_PLAN
	T_RETURNING
	T_CONFLICT
	T_DO
	T_NOTHING
	T_EXCLUDED
	T_WITH
	T_SAVEPOINT
	T_RELEASE
	T_TO
	T_CAST
	T_TRUE
	T_FALSE
	T_OUTER
	T_FULL
	T_PRAGMA
	T_INTERVAL
	T_OVER
	T_PARTITION
	T_ROWS
	T_RANGE
	T_PRECEDING
	T_FOLLOWING
	T_CURRENT
	T_UNBOUNDED
	T_ROW_NUMBER
	T_RANK
	T_DENSE_RANK
	T_LAG
	T_LEAD
	T_FIRST_VALUE
	T_LAST_VALUE
	T_NTH_VALUE
	T_ROW

	T_TRANSACTION
	T_ISOLATION
	T_LEVEL
	T_READ
	T_COMMITTED
	T_UNCOMMITTED
	T_REPEATABLE
	T_SERIALIZABLE
	T_VIEW
	T_TRIGGER
	T_TEMP
	T_TEMPORARY
	T_ALTER
	T_COLUMN
	T_ADD
	T_RENAME
	T_FETCH
	T_FIRST
	T_NEXT
	T_LAST
	T_ONLY
	T_REFERENCES
	T_FOREIGN
	T_CASCADE
	T_RESTRICT
	T_NO
	T_ACTION
	T_BITAND
	T_BITOR
	T_BITXOR
	T_BITNOT
	T_LSHIFT
	T_RSHIFT
	T_MOD
	T_CONCAT
	T_COALESCE
	T_NULLIF
	T_UNION
	T_INTERSECT
	T_EXCEPT
	T_ALL
	T_AUTOINCREMENT // REQ000482

	T_DEFERRED   // REQ000559
	T_IMMEDIATE  // REQ000559
	T_EXCLUSIVE  // REQ000559
	T_RAISE      // REQ000560
	T_ESCAPE     // REQ000567: LIKE ... ESCAPE
	T_INDEXED    // REQ000529/569: INDEXED BY / NOT INDEXED
	T_MATCH      // REQ000561: MATCH PARTIAL/FULL/SIMPLE
	T_DEFERRABLE // REQ000561: [NOT] DEFERRABLE
	T_INITIALLY  // REQ000561: INITIALLY DEFERRED/IMMEDIATE
	T_COLLATE    // REQ000565: COLLATE on indexed columns / ordering terms

	T_ATTACH       // REQ000557: ATTACH DATABASE
	T_DETACH       // REQ000557: DETACH DATABASE
	T_MATERIALIZED // REQ000316: MATERIALIZED VIEW
	T_REFRESH      // REQ000316: REFRESH MATERIALIZED VIEW

	// REQ000747: FILTER clause on aggregate functions
	T_FILTER
	// REQ000748: EXCLUDE clause in window frames
	T_EXCLUDE
	T_OTHERS
	T_TIES
	// REQ000746: GROUPS window frame type
	T_GROUPS

	T_ERROR TokenType = -1
)

type Token struct {
	Type    TokenType
	Lexeme  string
	Literal any
	Line    int
	Col     int
}

var tokenTypeNames = [...]string{
	T_EOF:           "EOF",
	T_IDENT:         "IDENT",
	T_STRING:        "STRING",
	T_INT:           "INT",
	T_FLOAT:         "FLOAT",
	T_BIND:          "BIND",
	T_EQ:            "EQ",
	T_NE:            "NE",
	T_LT:            "LT",
	T_LE:            "LE",
	T_GT:            "GT",
	T_GE:            "GE",
	T_PLUS:          "PLUS",
	T_MINUS:         "MINUS",
	T_STAR:          "STAR",
	T_SLASH:         "SLASH",
	T_LPAREN:        "LPAREN",
	T_RPAREN:        "RPAREN",
	T_COMMA:         "COMMA",
	T_DOT:           "DOT",
	T_SEMICOLON:     "SEMICOLON",
	T_COLON:         "COLON",
	T_CREATE:        "CREATE",
	T_DROP:          "DROP",
	T_INSERT:        "INSERT",
	T_UPDATE:        "UPDATE",
	T_DELETE:        "DELETE",
	T_SELECT:        "SELECT",
	T_FROM:          "FROM",
	T_WHERE:         "WHERE",
	T_AND:           "AND",
	T_OR:            "OR",
	T_BEFORE:        "BEFORE",
	T_AFTER:         "AFTER",
	T_NOT:           "NOT",
	T_IN:            "IN",
	T_BETWEEN:       "BETWEEN",
	T_LIKE:          "LIKE",
	T_GLOB:          "GLOB",
	T_DIV:           "DIV",
	T_IS:            "IS",
	T_NULL:          "NULL",
	T_BEGIN:         "BEGIN",
	T_COMMIT:        "COMMIT",
	T_ROLLBACK:      "ROLLBACK",
	T_AS:            "AS",
	T_BY:            "BY",
	T_ASC:           "ASC",
	T_DESC:          "DESC",
	T_LIMIT:         "LIMIT",
	T_OFFSET:        "OFFSET",
	T_TABLE:         "TABLE",
	T_INDEX:         "INDEX",
	T_PRIMARY:       "PRIMARY",
	T_KEY:           "KEY",
	T_NOTNULL:       "NOTNULL",
	T_DEFAULT:       "DEFAULT",
	T_CHECK:         "CHECK",
	T_UNIQUE:        "UNIQUE",
	T_INT_KW:        "INT_KW",
	T_BIGINT:        "BIGINT",
	T_FLOAT_KW:      "FLOAT_KW",
	T_BOOL:          "BOOL",
	T_TEXT:          "TEXT",
	T_BLOB:          "BLOB",
	T_VARCHAR:       "VARCHAR",
	T_TIMESTAMP:     "TIMESTAMP",
	T_NUMERIC:       "NUMERIC",
	T_DATE:          "DATE",
	T_TIME:          "TIME",
	T_JSON:          "JSON",
	T_DECIMAL:       "DECIMAL",
	T_VALUES:        "VALUES",
	T_SET:           "SET",
	T_INTO:          "INTO",
	T_ORDER:         "ORDER",
	T_JOIN:          "JOIN",
	T_LEFT:          "LEFT",
	T_RIGHT:         "RIGHT",
	T_INNER:         "INNER",
	T_CROSS:         "CROSS",
	T_ON:            "ON",
	T_USING:         "USING",
	T_GROUP:         "GROUP",
	T_HAVING:        "HAVING",
	T_COUNT:         "COUNT",
	T_SUM:           "SUM",
	T_AVG:           "AVG",
	T_MIN:           "MIN",
	T_MAX:           "MAX",
	T_DISTINCT:      "DISTINCT",
	T_CASE:          "CASE",
	T_WHEN:          "WHEN",
	T_THEN:          "THEN",
	T_ELSE:          "ELSE",
	T_END:           "END",
	T_CAST:          "CAST",
	T_EXISTS:        "EXISTS",
	T_ANALYZE:       "ANALYZE",
	T_VACUUM:        "VACUUM",
	T_TRUNCATE:      "TRUNCATE",
	T_REINDEX:       "REINDEX",
	T_TRUE:          "TRUE",
	T_FALSE:         "FALSE",
	T_OUTER:         "OUTER",
	T_FULL:          "FULL",
	T_PRAGMA:        "PRAGMA",
	T_INTERVAL:      "INTERVAL",
	T_OVER:          "OVER",
	T_PARTITION:     "PARTITION",
	T_ROWS:          "ROWS",
	T_RANGE:         "RANGE",
	T_PRECEDING:     "PRECEDING",
	T_FOLLOWING:     "FOLLOWING",
	T_CURRENT:       "CURRENT",
	T_UNBOUNDED:     "UNBOUNDED",
	T_ROW_NUMBER:    "ROW_NUMBER",
	T_RANK:          "RANK",
	T_DENSE_RANK:    "DENSE_RANK",
	T_LAG:           "LAG",
	T_LEAD:          "LEAD",
	T_FIRST_VALUE:   "FIRST_VALUE",
	T_LAST_VALUE:    "LAST_VALUE",
	T_NTH_VALUE:     "NTH_VALUE",
	T_ROW:           "ROW",
	T_TRANSACTION:   "TRANSACTION",
	T_ISOLATION:     "ISOLATION",
	T_LEVEL:         "LEVEL",
	T_READ:          "READ",
	T_COMMITTED:     "COMMITTED",
	T_UNCOMMITTED:   "UNCOMMITTED",
	T_REPEATABLE:    "REPEATABLE",
	T_SERIALIZABLE:  "SERIALIZABLE",
	T_VIEW:          "VIEW",
	T_TRIGGER:       "TRIGGER",
	T_TEMP:          "TEMP",
	T_TEMPORARY:     "TEMPORARY",
	T_ALTER:         "ALTER",
	T_COLUMN:        "COLUMN",
	T_ADD:           "ADD",
	T_RENAME:        "RENAME",
	T_FETCH:         "FETCH",
	T_FIRST:         "FIRST",
	T_NEXT:          "NEXT",
	T_LAST:          "LAST",
	T_ONLY:          "ONLY",
	T_EXPLAIN:       "EXPLAIN",
	T_QUERY:         "QUERY",
	T_PLAN:          "PLAN",
	T_RETURNING:     "RETURNING",
	T_CONFLICT:      "CONFLICT",
	T_DO:            "DO",
	T_NOTHING:       "NOTHING",
	T_EXCLUDED:      "EXCLUDED",
	T_WITH:          "WITH",
	T_SAVEPOINT:     "SAVEPOINT",
	T_RELEASE:       "RELEASE",
	T_TO:            "TO",
	T_REFERENCES:    "REFERENCES",
	T_FOREIGN:       "FOREIGN",
	T_CASCADE:       "CASCADE",
	T_RESTRICT:      "RESTRICT",
	T_NO:            "NO",
	T_ACTION:        "ACTION",
	T_BITAND:        "BITAND",
	T_BITOR:         "BITOR",
	T_BITXOR:        "BITXOR",
	T_BITNOT:        "BITNOT",
	T_LSHIFT:        "LSHIFT",
	T_RSHIFT:        "RSHIFT",
	T_MOD:           "MOD",
	T_CONCAT:        "CONCAT",
	T_COALESCE:      "COALESCE",
	T_NULLIF:        "NULLIF",
	T_UNION:         "UNION",
	T_INTERSECT:     "INTERSECT",
	T_EXCEPT:        "EXCEPT",
	T_ALL:           "ALL",
	T_AUTOINCREMENT: "AUTOINCREMENT",
	T_DEFERRED:      "DEFERRED",
	T_IMMEDIATE:     "IMMEDIATE",
	T_EXCLUSIVE:     "EXCLUSIVE",
	T_RAISE:         "RAISE",
	T_ESCAPE:        "ESCAPE",
	T_INDEXED:       "INDEXED",
	T_MATCH:         "MATCH",
	T_DEFERRABLE:    "DEFERRABLE",
	T_INITIALLY:     "INITIALLY",
	T_COLLATE:       "COLLATE",
	T_ATTACH:        "ATTACH",
	T_DETACH:        "DETACH",
	T_MATERIALIZED:  "MATERIALIZED",
	T_REFRESH:       "REFRESH",
	T_FILTER:        "FILTER",
	T_EXCLUDE:       "EXCLUDE",
	T_OTHERS:        "OTHERS",
	T_TIES:          "TIES",
	T_GROUPS:        "GROUPS",
}

func (t Token) String() string {
	if int(t.Type) < len(tokenTypeNames) && tokenTypeNames[t.Type] != "" {
		return tokenTypeNames[t.Type] + ":" + t.Lexeme
	}
	return "UNKNOWN:" + t.Lexeme
}

func (t Token) IsEOF() bool {
	return t.Type == T_EOF
}

func (t Token) IsError() bool {
	return t.Type == T_ERROR
}
