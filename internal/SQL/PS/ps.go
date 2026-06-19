package PS

import (
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
	LX.T_UNION:        "UNION",
	LX.T_INTERSECT:    "INTERSECT",
	LX.T_EXCEPT:       "EXCEPT",
	LX.T_ALL:          "ALL",
}

// isAggregateName reports whether a bare identifier name is a
// SQL aggregate function. REQ000355: GROUP_CONCAT is included
// so SELECT GROUP_CONCAT(col) FROM t routes through the
// AggregateFunc path instead of the function-call path.
func isAggregateName(name string) bool {
	switch strings.ToUpper(name) {
	case "COUNT", "SUM", "AVG", "MIN", "MAX", "GROUP_CONCAT":
		return true
	}
	return false
}

func isMinMaxName(name string) bool {
	switch strings.ToUpper(name) {
	case "MIN", "MAX":
		return true
	}
	return false
}

func isBinaryOp(typ LX.TokenType) bool {
	switch typ {
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE,
		LX.T_AND, LX.T_OR,
		LX.T_PLUS, LX.T_MINUS, LX.T_STAR, LX.T_SLASH,
		LX.T_LIKE, LX.T_IS,
		LX.T_BITAND, LX.T_BITOR, LX.T_BITXOR,
		LX.T_MOD, LX.T_CONCAT:
		return true
	}
	return false
}

func precedence(typ LX.TokenType) int {
	switch typ {
	case LX.T_OR:
		return 1
	case LX.T_AND:
		return 2
	case LX.T_BITOR:
		return 3
	case LX.T_BITXOR:
		return 4
	case LX.T_BITAND:
		return 5
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE, LX.T_LIKE, LX.T_IS:
		return 6
	case LX.T_CONCAT:
		return 7
	case LX.T_PLUS, LX.T_MINUS:
		return 8
	case LX.T_STAR, LX.T_SLASH, LX.T_MOD:
		return 9
	}
	return 0
}

func (p *Parser) Parse() (Stmt, error) {
	p.reset()
	p.advance()

	var stmt Stmt
	var err error
	switch p.current.Type {
	case LX.T_SELECT:
		stmt, err = p.parseSelect()
	case LX.T_INSERT:
		stmt, err = p.parseInsert()
	case LX.T_UPDATE:
		stmt, err = p.parseUpdate()
	case LX.T_DELETE:
		stmt, err = p.parseDelete()
	case LX.T_CREATE:
		// CREATE TABLE vs CREATE INDEX vs CREATE VIEW vs CREATE TRIGGER vs CREATE [TEMP] VIEW
		next := p.lex.Peek().Type
		if next == LX.T_INDEX {
			stmt, err = p.parseCreateIndex()
		} else if next == LX.T_UNIQUE && p.lex.Peek2().Type == LX.T_INDEX {
			stmt, err = p.parseCreateIndex()
		} else if next == LX.T_VIEW {
			stmt, err = p.parseCreateView()
		} else if next == LX.T_TRIGGER {
			stmt, err = p.parseCreateTrigger()
		} else if next == LX.T_TEMP || next == LX.T_TEMPORARY {
			if p.lex.Peek2().Type == LX.T_VIEW {
				stmt, err = p.parseCreateView()
			} else {
				stmt, err = p.parseCreateTable()
			}
		} else {
			stmt, err = p.parseCreateTable()
		}
	case LX.T_DROP:
		// DROP TABLE vs DROP INDEX vs DROP VIEW vs DROP TRIGGER —
		// disambiguate by peeking.
		switch p.lex.Peek().Type {
		case LX.T_INDEX:
			stmt, err = p.parseDropIndex()
		case LX.T_VIEW:
			stmt, err = p.parseDropView()
		case LX.T_TRIGGER:
			stmt, err = p.parseDropTrigger()
		default:
			stmt, err = p.parseDropTable()
		}
	case LX.T_EXPLAIN:
		stmt, err = p.parseExplain()
	case LX.T_ANALYZE:
		stmt, err = p.parseAnalyze()
	case LX.T_VACUUM:
		stmt, err = p.parseVacuum()
	case LX.T_TRUNCATE:
		stmt, err = p.parseTruncate()
	case LX.T_REINDEX:
		stmt, err = p.parseReindex()
	case LX.T_VALUES:
		stmt, err = p.parseValues()
	case LX.T_PRAGMA:
		stmt, err = p.parsePragma()
	case LX.T_WITH:
		stmt, err = p.parseWith()
	case LX.T_SAVEPOINT:
		stmt, err = p.parseSavepoint()
	case LX.T_RELEASE:
		stmt, err = p.parseReleaseSavepoint()
	case LX.T_ROLLBACK:
		next := p.lex.Peek()
		if next.Type == LX.T_TO {
			stmt, err = p.parseRollbackTo()
		} else {
			// REQ000593: bare ROLLBACK without TO SAVEPOINT.
			p.advance() // consume ROLLBACK
			if p.lex.Peek().Type == LX.T_TRANSACTION {
				p.advance()
			}
			stmt = &RollbackTX{}
		}
	case LX.T_BEGIN:
		stmt, err = p.parseBegin()
	case LX.T_COMMIT:
		stmt, err = p.parseCommit()
	case LX.T_END:
		// REQ000570: bare END outside trigger/CASE context is COMMIT synonym.
		// At top-level Parse() dispatch, T_END is unambiguous.
		stmt, err = p.parseCommit()
	case LX.T_SET:
		stmt, err = p.parseSet()
	case LX.T_ALTER:
		stmt, err = p.parseAlterTable()
	case LX.T_IDENT:
		// REPLACE INTO — REPLACE is not a hard keyword, detect via lexeme.
		if strings.EqualFold(p.current.Lexeme, "REPLACE") {
			next := p.lex.Peek()
			if next.Type == LX.T_INTO {
				stmt, err = p.parseReplace()
				break
			}
		}
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	default:
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
	if err != nil {
		return nil, err
	}
	if p.current.Type != LX.T_EOF {
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
	return stmt, nil
}

// parseSelect parses a SELECT statement, possibly followed by a
// chain of compound operators (UNION, UNION ALL, INTERSECT,
// EXCEPT). REQ000383.
//
// Precedence: INTERSECT binds tighter than UNION/EXCEPT (per
// SQLite). The chain is built left-associatively.
//
//	a UNION b INTERSECT c  →  a UNION (b INTERSECT c)
//	a INTERSECT b UNION c  →  (a INTERSECT b) UNION c
//
// We implement a single precedence level for the v1 (UNION, EXCEPT)
// and a higher one for INTERSECT.
