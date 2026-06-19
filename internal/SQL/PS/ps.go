package PS

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

type Parser struct {
	lex                  *LX.Lexer
	current              LX.Token
	paramIndex           int
	pendingJoins         []string // REQ000368: comma-separated tables awaiting CROSS-join synthesis
	pendingSubquery      Stmt     // REQ000436: subquery from FROM clause
	pendingSubqueryAlias string
}

func NewParser(input string) *Parser {
package PS

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

type Parser struct {
	lex                  *LX.Lexer
	current              LX.Token
	paramIndex           int
	pendingJoins         []string // REQ000368: comma-separated tables awaiting CROSS-join synthesis
	pendingSubquery      Stmt     // REQ000436: subquery from FROM clause
	pendingSubqueryAlias string
}

func NewParser(input string) *Parser {
	return &Parser{
		lex:     LX.NewLexer(input),
		current: LX.Token{Type: LX.T_EOF, Lexeme: "", Line: 0, Col: 0},
	}
}

func (p *Parser) reset() {
	p.paramIndex = 0
	p.pendingJoins = nil
	p.pendingSubquery = nil
}

func (p *Parser) advance() {
	p.current = p.lex.Next()
}

func (p *Parser) expect(typ LX.TokenType) error {
	if p.current.Type != typ {
		return &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: tokenName(typ),
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	return nil
}

func tokenName(t LX.TokenType) string {
	if int(t) < 0 {
		return "<error>"
	}
	if int(t) >= len(tokenNames) {
		return "<unknown>"
	}
	return tokenNames[t]
}

var tokenNames = [...]string{
	LX.T_EOF:          "EOF",
	LX.T_IDENT:        "identifier",
	LX.T_STRING:       "string literal",
	LX.T_INT:          "integer",
	LX.T_FLOAT:        "float",
	LX.T_BIND:         "?",
	LX.T_EQ:           "=",
	LX.T_NE:           "!=",
	LX.T_LT:           "<",
	LX.T_LE:           "<=",
	LX.T_GT:           ">",
	LX.T_GE:           ">=",
	LX.T_PLUS:         "+",
	LX.T_MINUS:        "-",
	LX.T_STAR:         "*",
	LX.T_SLASH:        "/",
	LX.T_LPAREN:       "(",
	LX.T_RPAREN:       ")",
	LX.T_COMMA:        ",",
	LX.T_DOT:          ".",
	LX.T_SEMICOLON:    ";",
	LX.T_COLON:        ":",
	LX.T_CREATE:       "CREATE",
	LX.T_DROP:         "DROP",
	LX.T_INSERT:       "INSERT",
	LX.T_UPDATE:       "UPDATE",
	LX.T_DELETE:       "DELETE",
	LX.T_SELECT:       "SELECT",
	LX.T_FROM:         "FROM",
	LX.T_WHERE:        "WHERE",
	LX.T_AND:          "AND",
	LX.T_OR:           "OR",
	LX.T_NOT:          "NOT",
	LX.T_IN:           "IN",
	LX.T_BETWEEN:      "BETWEEN",
	LX.T_LIKE:         "LIKE",
	LX.T_IS:           "IS",
	LX.T_NULL:         "NULL",
	LX.T_BEGIN:        "BEGIN",
	LX.T_COMMIT:       "COMMIT",
	LX.T_ROLLBACK:     "ROLLBACK",
	LX.T_AS:           "AS",
	LX.T_BY:           "BY",
	LX.T_ASC:          "ASC",
	LX.T_DESC:         "DESC",
	LX.T_LIMIT:        "LIMIT",
	LX.T_OFFSET:       "OFFSET",
	LX.T_TABLE:        "TABLE",
	LX.T_INDEX:        "INDEX",
	LX.T_PRIMARY:      "PRIMARY",
	LX.T_KEY:          "KEY",
	LX.T_NOTNULL:      "NOTNULL",
	LX.T_DEFAULT:      "DEFAULT",
	LX.T_UNIQUE:       "UNIQUE",
	LX.T_INT_KW:       "INTEGER",
	LX.T_BIGINT:       "BIGINT",
	LX.T_FLOAT_KW:     "FLOAT",
	LX.T_BOOL:         "BOOLEAN",
	LX.T_TEXT:         "TEXT",
	LX.T_BLOB:         "BLOB",
	LX.T_VARCHAR:      "VARCHAR",
	LX.T_TIMESTAMP:    "TIMESTAMP",
	LX.T_VALUES:       "VALUES",
	LX.T_SET:          "SET",
	LX.T_INTO:         "INTO",
	LX.T_ORDER:        "ORDER",
	LX.T_JOIN:         "JOIN",
	LX.T_LEFT:         "LEFT",
	LX.T_RIGHT:        "RIGHT",
	LX.T_INNER:        "INNER",
	LX.T_CROSS:        "CROSS",
	LX.T_ON:           "ON",
	LX.T_USING:        "USING",
	LX.T_GROUP:        "GROUP",
	LX.T_HAVING:       "HAVING",
	LX.T_COUNT:        "COUNT",
	LX.T_SUM:          "SUM",
	LX.T_AVG:          "AVG",
	LX.T_MIN:          "MIN",
	LX.T_MAX:          "MAX",
	LX.T_DISTINCT:     "DISTINCT",
	LX.T_CASE:         "CASE",
	LX.T_WHEN:         "WHEN",
	LX.T_THEN:         "THEN",
	LX.T_ELSE:         "ELSE",
	LX.T_END:          "END",
	LX.T_CAST:         "CAST",
	LX.T_EXISTS:       "EXISTS",
	LX.T_TRUE:         "TRUE",
	LX.T_FALSE:        "FALSE",
	LX.T_EXPLAIN:      "EXPLAIN",
	LX.T_QUERY:        "QUERY",
	LX.T_PLAN:         "PLAN",
	LX.T_RETURNING:    "RETURNING",
	LX.T_CONFLICT:     "CONFLICT",
	LX.T_DO:           "DO",
	LX.T_NOTHING:      "NOTHING",
	LX.T_EXCLUDED:     "EXCLUDED",
	LX.T_WITH:         "WITH",
	LX.T_SAVEPOINT:    "SAVEPOINT",
	LX.T_RELEASE:      "RELEASE",
	LX.T_TO:           "TO",
	LX.T_INTERVAL:     "INTERVAL",
	LX.T_OVER:         "OVER",
	LX.T_PARTITION:    "PARTITION",
	LX.T_ROWS:         "ROWS",
	LX.T_RANGE:        "RANGE",
	LX.T_PRECEDING:    "PRECEDING",
	LX.T_FOLLOWING:    "FOLLOWING",
	LX.T_CURRENT:      "CURRENT",
	LX.T_UNBOUNDED:    "UNBOUNDED",
	LX.T_ROW_NUMBER:   "ROW_NUMBER",
	LX.T_RANK:         "RANK",
	LX.T_DENSE_RANK:   "DENSE_RANK",
	LX.T_LAG:          "LAG",
	LX.T_LEAD:         "LEAD",
	LX.T_FIRST_VALUE:  "FIRST_VALUE",
	LX.T_LAST_VALUE:   "LAST_VALUE",
	LX.T_NTH_VALUE:    "NTH_VALUE",
	LX.T_ROW:          "ROW",
	LX.T_TRANSACTION:  "TRANSACTION",
	LX.T_ISOLATION:    "ISOLATION",
	LX.T_LEVEL:        "LEVEL",
	LX.T_READ:         "READ",
	LX.T_COMMITTED:    "COMMITTED",
	LX.T_UNCOMMITTED:  "UNCOMMITTED",
	LX.T_REPEATABLE:   "REPEATABLE",
	LX.T_SERIALIZABLE: "SERIALIZABLE",
	LX.T_VIEW:         "VIEW",
	LX.T_ALTER:        "ALTER",
	LX.T_COLUMN:       "COLUMN",
	LX.T_ADD:          "ADD",
	LX.T_RENAME:       "RENAME",
	LX.T_FETCH:        "FETCH",
	LX.T_FIRST:        "FIRST",
	LX.T_ONLY:         "ONLY",
	LX.T_REFERENCES:   "REFERENCES",
	LX.T_FOREIGN:      "FOREIGN",
	LX.T_CASCADE:      "CASCADE",
	LX.T_RESTRICT:     "RESTRICT",
	LX.T_NO:           "NO",
	LX.T_ACTION:       "ACTION",
	LX.T_BITAND:       "&",
	LX.T_BITOR:        "|",
	LX.T_BITXOR:       "^",
	LX.T_BITNOT:       "~",
	LX.T_MOD:          "%",
	LX.T_CONCAT:       "||",
