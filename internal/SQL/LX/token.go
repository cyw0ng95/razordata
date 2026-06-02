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
	T_NOT
	T_IN
	T_BETWEEN
	T_LIKE
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
	T_UNIQUE
	T_INT_KW
	T_BIGINT
	T_FLOAT_KW
	T_BOOL
	T_TEXT
	T_BLOB
	T_VARCHAR
	T_TIMESTAMP
	T_VALUES
	T_SET
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
	T_CAST
	T_EXISTS
)

type Token struct {
	Type    TokenType
	Lexeme  string
	Literal any
	Line    int
	Col     int
}

func (t Token) String() string {
	return string(rune(t.Type)) + ":" + t.Lexeme
}

func (t Token) IsEOF() bool {
	return t.Type == T_EOF
}

func (t Token) IsError() bool {
	return t.Lexeme == "ERROR"
}
