package PS

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

type Parser struct {
	lex          *LX.Lexer
	current      LX.Token
	paramIndex   int
	pendingJoins []string // REQ000368: comma-separated tables awaiting CROSS-join synthesis
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

func (p *Parser) parsePrimary() (Expr, error) {
	switch p.current.Type {
	case LX.T_INT:
		val := p.current.Literal.(int64)
		p.advance()
		return &NumberLiteral{Val: val}, nil
	case LX.T_FLOAT:
		val := p.current.Lexeme
		p.advance()
		return &FloatLiteral{Val: parseFloat(val)}, nil
	case LX.T_STRING:
		val := p.current.Literal.(string)
		p.advance()
		return &StringLiteral{Val: val}, nil
	case LX.T_NULL:
		p.advance()
		return &NullLiteral{}, nil
	case LX.T_TRUE:
		p.advance()
		return &BoolLiteral{Val: true}, nil
	case LX.T_FALSE:
		p.advance()
		return &BoolLiteral{Val: false}, nil
	case LX.T_BIND:
		idx := p.paramIndex
		p.paramIndex++
		p.advance()
		return &Param{Index: idx}, nil
	case LX.T_STAR:
		p.advance()
		return &StarExpr{}, nil
	case LX.T_IDENT, LX.T_EXCLUDED:
		name := p.current.Lexeme
		p.advance()
		if p.current.Type == LX.T_DOT {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			col := p.current.Lexeme
			p.advance()
			return &QualifiedName{Table: name, Name: col}, nil
		}
		if p.current.Type == LX.T_LPAREN {
			p.advance()
			var args []Expr
			if p.current.Type != LX.T_RPAREN {
				a, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				for p.current.Type == LX.T_COMMA {
					p.advance()
					a, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
				}
			}
			if err := p.expect(LX.T_RPAREN); err != nil {
				return nil, err
			}
			p.advance()
			// REQ000355: aggregate functions like GROUP_CONCAT are
			// spelled as plain identifiers in SQL. Route them
			// through AggregateFunc so the executor's aggregate
			// path handles them.
			if isAggregateName(name) {
				var arg Expr
				if len(args) > 0 {
					arg = args[0]
				}
				return &AggregateFunc{Name: name, Arg: arg}, nil
			}
			return &FunctionCall{Name: name, Args: args}, nil
		}
		return &Ident{Name: name}, nil
	case LX.T_COUNT, LX.T_SUM, LX.T_AVG, LX.T_MIN, LX.T_MAX:
		name := strings.ToUpper(p.current.Lexeme)
		p.advance()
		if err := p.expect(LX.T_LPAREN); err != nil {
			return nil, err
		}
		p.advance()
		// Handle DISTINCT keyword in aggregate functions
		distinct := false
		if p.current.Type == LX.T_DISTINCT {
			distinct = true
			p.advance()
		}
		var arg Expr
		if p.current.Type == LX.T_STAR {
			arg = &StarExpr{}
			p.advance()
		} else {
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			arg = a
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
		agg := &AggregateFunc{Name: name, Arg: arg, Distinct: distinct}
		if p.current.Type == LX.T_OVER {
			return p.parseWindowFunc(name, []Expr{arg})
		}
		return agg, nil
	case LX.T_ROW_NUMBER, LX.T_RANK, LX.T_DENSE_RANK, LX.T_LAG, LX.T_LEAD, LX.T_FIRST_VALUE, LX.T_LAST_VALUE, LX.T_NTH_VALUE:
		return p.parseWindowBuiltin()
	case LX.T_LPAREN:
		p.advance()
		if p.current.Type == LX.T_SELECT {
			sel, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if err := p.expect(LX.T_RPAREN); err != nil {
				return nil, err
			}
			p.advance()
			return &SubqueryExpr{Subquery: sel}, nil
		}
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
		return expr, nil
	case LX.T_CASE:
		return p.parseCaseExpr()
	case LX.T_CAST:
		return p.parseCast()
	case LX.T_COALESCE:
		return p.parseCoalesce()
	case LX.T_NULLIF:
		return p.parseNullif()
	case LX.T_EXISTS:
		return p.parseExists()
	case LX.T_INTERVAL:
		return p.parseInterval()
	}
	return nil, &SyntaxError{
		Input:  p.lex.Input(),
		Line:   p.current.Line,
		Col:    p.current.Col,
		Got:    tokenName(p.current.Type),
		Lexeme: p.current.Lexeme,
	}
}

func (p *Parser) parseUnary() (Expr, error) {
	if p.current.Type == LX.T_NOT || p.current.Type == LX.T_MINUS || p.current.Type == LX.T_PLUS || p.current.Type == LX.T_BITNOT {
		op := int(p.current.Type)
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: op, Operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	switch p.current.Type {
	case LX.T_BETWEEN:
		return p.parseBetween(expr)
	case LX.T_IN:
		return p.parseIn(expr)
	}
	if p.current.Type == LX.T_NOT {
		next := p.lex.Peek().Type
		switch next {
		case LX.T_LIKE:
			p.advance()
			return p.parseNotLike(expr)
		case LX.T_IN:
			p.advance()
			return p.parseNotIn(expr)
		case LX.T_BETWEEN:
			p.advance()
			return p.parseNotBetween(expr)
		}
	}
	return expr, nil
}

// REQ000380: `NOT LIKE` — parse x NOT LIKE y as NOT(x LIKE y).
func (p *Parser) parseNotLike(expr Expr) (Expr, error) {
	p.advance()
	right, err := p.parseBinary(7)
	if err != nil {
		return nil, err
	}
	like := &BinaryExpr{Op: int(LX.T_LIKE), Left: expr, Right: right}
	return &UnaryExpr{Op: int(LX.T_NOT), Operand: like}, nil
}

// REQ000381: `NOT IN` — parse x NOT IN (...) as NOT(x IN (...)).
func (p *Parser) parseNotIn(expr Expr) (Expr, error) {
	// parsePostfix already consumed T_NOT; we still need to
	// consume T_IN and the ( ... ).
	p.advance()
	in, err := p.parseInBody(expr)
	if err != nil {
		return nil, err
	}
	return &UnaryExpr{Op: int(LX.T_NOT), Operand: in}, nil
}

// REQ000434: `NOT BETWEEN` — parse x NOT BETWEEN low AND high as NOT(x BETWEEN low AND high).
func (p *Parser) parseNotBetween(expr Expr) (Expr, error) {
	p.advance() // consume BETWEEN
	low, err := p.parseBinary(3)
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_AND); err != nil {
		return nil, err
	}
	p.advance() // consume AND
	high, err := p.parseBinary(3)
	if err != nil {
		return nil, err
	}
	between := &BetweenExpr{Expr: expr, Low: low, High: high}
	return &UnaryExpr{Op: int(LX.T_NOT), Operand: between}, nil
}

func (p *Parser) parseBetween(expr Expr) (Expr, error) {
	p.advance()
	low, err := p.parseBinary(3)
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_AND); err != nil {
		return nil, err
	}
	p.advance()
	high, err := p.parseBinary(3)
	if err != nil {
		return nil, err
	}
	return &BetweenExpr{Expr: expr, Low: low, High: high}, nil
}

func (p *Parser) parseIn(expr Expr) (Expr, error) {
	p.advance()
	return p.parseInBody(expr)
}

// parseInBody parses the (...) part of an IN expression. Caller
// must have already advanced past the T_IN token. Used by
// parseIn and parseNotIn (REQ000381).
func (p *Parser) parseInBody(expr Expr) (Expr, error) {
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()
	if p.current.Type == LX.T_SELECT {
		sel, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
		return &InExpr{Expr: expr, Subquery: sel}, nil
	}
	var items []Expr
	if p.current.Type != LX.T_RPAREN {
		for {
			it, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			items = append(items, it)
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()
	return &InExpr{Expr: expr, List: items}, nil
}

func (p *Parser) parseBinary(minPrec int) (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for isBinaryOp(p.current.Type) && precedence(p.current.Type) >= minPrec {
		op := int(p.current.Type)
		p.advance()
		nextMinPrec := precedence(LX.TokenType(op)) + 1
		right, err := p.parseBinary(nextMinPrec)
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}

	return left, nil
}

func (p *Parser) parseExpr() (Expr, error) {
	return p.parseBinary(1)
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
		// CREATE TABLE vs CREATE INDEX vs CREATE VIEW — disambiguate by peeking.
		next := p.lex.Peek().Type
		if next == LX.T_INDEX {
			stmt, err = p.parseCreateIndex()
		} else if next == LX.T_UNIQUE && p.lex.Peek2().Type == LX.T_INDEX {
			stmt, err = p.parseCreateIndex()
		} else if next == LX.T_VIEW {
			stmt, err = p.parseCreateView()
		} else {
			stmt, err = p.parseCreateTable()
		}
	case LX.T_DROP:
		// DROP TABLE vs DROP INDEX — disambiguate by peeking.
		if p.lex.Peek().Type == LX.T_INDEX {
			stmt, err = p.parseDropIndex()
		} else {
			stmt, err = p.parseDropTable()
		}
	case LX.T_EXPLAIN:
		stmt, err = p.parseExplain()
	case LX.T_ANALYZE:
		stmt, err = p.parseAnalyze()
	case LX.T_VACUUM:
		stmt, err = p.parseVacuum()
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
		}
	case LX.T_SET:
		stmt, err = p.parseSet()
	case LX.T_ALTER:
		stmt, err = p.parseAlterTable()
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
func (p *Parser) parseSelect() (Stmt, error) {
	left, err := p.parseIntersectChain()
	if err != nil {
		return nil, err
	}
	for p.current.Type == LX.T_UNION || p.current.Type == LX.T_EXCEPT {
		op := CompoundUnion
		if p.current.Type == LX.T_EXCEPT {
			op = CompoundExcept
		}
		p.advance()
		// EXCEPT ALL is not in SQLite; only UNION supports
		// the ALL suffix. Reject EXCEPT ALL with a syntax
		// error.
		if p.current.Type == LX.T_ALL {
			if op == CompoundExcept {
				return nil, &SyntaxError{
					Input:    p.lex.Input(),
					Line:     p.current.Line,
					Col:      p.current.Col,
					Expected: "EXCEPT (no ALL suffix)",
					Got:      tokenName(p.current.Type),
					Lexeme:   p.current.Lexeme,
				}
			}
			op = CompoundUnionAll
			p.advance()
		}
		right, err := p.parseIntersectChain()
		if err != nil {
			return nil, err
		}
		left = &CompoundStmt{Left: left, Op: op, Right: right}
	}
	// REQ000383: ORDER BY / LIMIT / OFFSET at the end of a
	// compound chain (or standalone SELECT) apply to the entire
	// result, not individual leaf SELECTs.
	ob, lim, off, err := p.parseTrailingClauses()
	if err != nil {
		return nil, err
	}
	switch s := left.(type) {
	case *CompoundStmt:
		s.OrderBy = ob
		s.Limit = lim
		s.Offset = off
	case *Select:
		s.OrderBy = ob
		s.Limit = lim
		s.Offset = off
	}
	return left, nil
}

// parseTrailingClauses parses optional ORDER BY / LIMIT / OFFSET.
// REQ000383.
func (p *Parser) parseTrailingClauses() ([]OrderItem, Expr, Expr, error) {
	var orderBy []OrderItem
	if p.current.Type == LX.T_ORDER {
		p.advance()
		if err := p.expect(LX.T_BY); err != nil {
			return nil, nil, nil, err
		}
		p.advance()
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, nil, nil, err
			}
			desc := false
			if p.current.Type == LX.T_ASC {
				p.advance()
			} else if p.current.Type == LX.T_DESC {
				desc = true
				p.advance()
			}
			orderBy = append(orderBy, OrderItem{Expr: expr, Desc: desc})
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}
	var limit Expr
	if p.current.Type == LX.T_LIMIT {
		p.advance()
		l, err := p.parseExpr()
		if err != nil {
			return nil, nil, nil, err
		}
		limit = l
	}
	var offset Expr
	if p.current.Type == LX.T_OFFSET {
		p.advance()
		o, err := p.parseExpr()
		if err != nil {
			return nil, nil, nil, err
		}
		offset = o
	}
	return orderBy, limit, offset, nil
}

// parseIntersectChain parses `INTERSECT` (or chain thereof) and
// returns either a plain *Select or a *CompoundStmt. REQ000383.
//
// Trailing ORDER BY/LIMIT/OFFSET are always parsed by the outer
// caller (parseSelect), so that they apply to the entire compound
// chain rather than individual leaf SELECTs.
func (p *Parser) parseIntersectChain() (Stmt, error) {
	first, err := p.parseOneSelect()
	if err != nil {
		return nil, err
	}
	var left Stmt = first
	for p.current.Type == LX.T_INTERSECT {
		p.advance()
		right, err := p.parseOneSelect()
		if err != nil {
			return nil, err
		}
		left = &CompoundStmt{Left: left, Op: CompoundIntersect, Right: right}
	}
	// Never parse trailing clauses here — let parseSelect handle
	// them so they apply to the entire chain.
	return left, nil
}

// parseOneSelect parses a single SELECT statement (no compound
// chain). REQ000383: split out from parseSelect.
func (p *Parser) parseOneSelect() (*Select, error) {
	p.advance()

	var distinct bool
	if p.current.Type == LX.T_DISTINCT {
		distinct = true
		p.advance()
	}

	var cols []Expr
	if p.current.Type == LX.T_STAR {
		cols = append(cols, &StarExpr{})
		p.advance()
	} else {
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if p.current.Type == LX.T_AS {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				expr = &AliasedExpr{Expr: expr, Alias: p.current.Lexeme}
				p.advance()
			}
			cols = append(cols, expr)
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}

	var from string
	if p.current.Type == LX.T_FROM {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		from = p.current.Lexeme
		p.advance()
	}

	// REQ000368: implicit comma-join. `FROM a, b, c` is parsed
	// as `FROM a CROSS JOIN b CROSS JOIN c`. The first table
	// stays as `from`; each subsequent comma-separated identifier
	// becomes a CROSS join entry.
	for p.current.Type == LX.T_COMMA {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		// Defer the join: we need to finish parsing the alias
		// for the first table before collecting joins. Stash
		// the right-table name in a local and append after
		// alias parsing.
		p.pendingJoins = append(p.pendingJoins, p.current.Lexeme)
		p.advance()
	}

	var fromAlias string
	if p.current.Type == LX.T_AS {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		fromAlias = p.current.Lexeme
		p.advance()
	}

	// REQ000368: promote any pending comma-separated tables
	// (collected above) into CROSS joins.
	var joins []JoinClause
	for _, right := range p.pendingJoins {
		joins = append(joins, JoinClause{Kind: "CROSS", Right: right})
	}
	p.pendingJoins = nil

	for p.current.Type == LX.T_JOIN || p.current.Type == LX.T_LEFT ||
		p.current.Type == LX.T_RIGHT || p.current.Type == LX.T_INNER ||
		p.current.Type == LX.T_CROSS {
		kind := "INNER"
		switch p.current.Type {
		case LX.T_LEFT:
			kind = "LEFT"
		case LX.T_RIGHT:
			kind = "RIGHT"
		case LX.T_CROSS:
			kind = "CROSS"
		}
		p.advance()
		if p.current.Type == LX.T_OUTER {
			p.advance()
		}
		if p.current.Type == LX.T_JOIN {
			p.advance()
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		right := p.current.Lexeme
		p.advance()
		var on Expr
		if p.current.Type == LX.T_ON {
			p.advance()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			on = e
		}
		joins = append(joins, JoinClause{Kind: kind, Right: right, On: on})
	}

	var where Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		where = w
	}

	var groupBy []Expr
	if p.current.Type == LX.T_GROUP {
		p.advance()
		if err := p.expect(LX.T_BY); err != nil {
			return nil, err
		}
		p.advance()
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			groupBy = append(groupBy, expr)
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}

	var having Expr
	if p.current.Type == LX.T_HAVING {
		p.advance()
		h, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		having = h
	}

	// REQ000383: trailing ORDER BY / LIMIT / OFFSET / FETCH are
	// parsed by the caller (parseIntersectChain or parseSelect),
	// not here, so that compound chains can attach them to the
	// whole result rather than the leaf Select.

	return &Select{
		Cols:      cols,
		From:      from,
		FromAlias: fromAlias,
		Joins:     joins,
		Where:     where,
		GroupBy:   groupBy,
		Having:    having,
		Distinct:  distinct,
	}, nil
}

func (p *Parser) parseInsert() (*Insert, error) {
	p.advance()

	if err := p.expect(LX.T_INTO); err != nil {
		return nil, err
	}
	p.advance()

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()

	var cols []string
	if p.current.Type == LX.T_LPAREN {
		p.advance()
		for {
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			cols = append(cols, p.current.Lexeme)
			p.advance()
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
	}

	if err := p.expect(LX.T_VALUES); err != nil {
		return nil, err
	}
	p.advance()

	var values [][]Expr
	for {
		if err := p.expect(LX.T_LPAREN); err != nil {
			return nil, err
		}
		p.advance()

		var row []Expr
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			row = append(row, expr)
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}

		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()

		values = append(values, row)

		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}

	returning, err := p.parseReturning()
	if err != nil {
		return nil, err
	}

	// Parse ON CONFLICT clause if present
	var onConflict *OnConflict
	if p.current.Type == LX.T_ON {
		// Peek to check if this is ON CONFLICT (not ON for JOIN)
		next := p.lex.Peek()
		if next.Type == LX.T_CONFLICT {
			onConflict, err = p.parseOnConflict()
			if err != nil {
				return nil, err
			}
		}
	}

	return &Insert{Table: table, Cols: cols, Values: values, Returning: returning, OnConflict: onConflict}, nil
}

func (p *Parser) parseOnConflict() (*OnConflict, error) {
	// ON CONFLICT
	if err := p.expect(LX.T_ON); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.expect(LX.T_CONFLICT); err != nil {
		return nil, err
	}
	p.advance()

	// Optional (target columns)
	var columns []string
	if p.current.Type == LX.T_LPAREN {
		p.advance()
		for {
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			columns = append(columns, p.current.Lexeme)
			p.advance()
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
	}

	// DO NOTHING or DO UPDATE SET
	if err := p.expect(LX.T_DO); err != nil {
		return nil, err
	}
	p.advance()

	if p.current.Type == LX.T_NOTHING {
		p.advance()
		return &OnConflict{Columns: columns, DoNothing: true}, nil
	}

	if err := p.expect(LX.T_UPDATE); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.expect(LX.T_SET); err != nil {
		return nil, err
	}
	p.advance()

	var setClauses []Pair
	for {
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		col := p.current.Lexeme
		p.advance()

		if err := p.expect(LX.T_EQ); err != nil {
			return nil, err
		}
		p.advance()

		// REQ000291: parseExpr handles EXCLUDED.col as QualifiedName
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		setClauses = append(setClauses, Pair{Col: col, Val: val})

		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}

	return &OnConflict{Columns: columns, SetClauses: setClauses}, nil
}

func (p *Parser) parseUpdate() (*Update, error) {
	p.advance()

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()

	if err := p.expect(LX.T_SET); err != nil {
		return nil, err
	}
	p.advance()

	var set []Pair
	for {
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		col := p.current.Lexeme
		p.advance()

		if err := p.expect(LX.T_EQ); err != nil {
			return nil, err
		}
		p.advance()

		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		set = append(set, Pair{Col: col, Val: val})

		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}

	var where Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		where = w
	}

	returning, err := p.parseReturning()
	if err != nil {
		return nil, err
	}

	return &Update{Table: table, Set: set, Where: where, Returning: returning}, nil
}

func (p *Parser) parseDelete() (*Delete, error) {
	p.advance()

	if err := p.expect(LX.T_FROM); err != nil {
		return nil, err
	}
	p.advance()

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()

	var where Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		where = w
	}

	returning, err := p.parseReturning()
	if err != nil {
		return nil, err
	}

	return &Delete{Table: table, Where: where, Returning: returning}, nil
}

func (p *Parser) parseCreateTable() (*CreateTable, error) {
	p.advance()

	if err := p.expect(LX.T_TABLE); err != nil {
		return nil, err
	}
	p.advance()

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()

	var cols []ColDef
	var pk *string
	for {
		if p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE || p.current.Type == LX.T_FOREIGN {
			break
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		colName := p.current.Lexeme
		p.advance()

		// Parse type with optional size/precision (REQ000207)
		typeInfo, err := p.parseCastType()
		if err != nil {
			return nil, err
		}

		col := NewColDef(colName, typeInfo.Type)
		col.Size = typeInfo.Size

		for p.current.Type == LX.T_NOTNULL || p.current.Type == LX.T_PRIMARY ||
			p.current.Type == LX.T_DEFAULT || p.current.Type == LX.T_UNIQUE ||
			p.current.Type == LX.T_NOT || p.current.Type == LX.T_CHECK {
			if p.current.Type == LX.T_PRIMARY {
				peek := p.lex.Peek()
				if peek.Type == LX.T_KEY {
					p.advance()
					p.advance()
					if p.current.Type == LX.T_LPAREN {
						break
					}
					col.PK = true
					continue
				}
			}
			switch p.current.Type {
			case LX.T_NOTNULL:
				col.Nullable = false
				p.advance()
			case LX.T_NOT:
				p.advance()
				if p.current.Type == LX.T_NULL {
					col.Nullable = false
					p.advance()
				}
			case LX.T_PRIMARY:
				col.PK = true
				col.Nullable = false
				p.advance()
				if p.current.Type == LX.T_KEY {
					p.advance()
				}
			case LX.T_DEFAULT:
				p.advance()
				d, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				col.Default = d
			case LX.T_CHECK:
				p.advance()
				if err := p.expect(LX.T_LPAREN); err != nil {
					return nil, err
				}
				p.advance()
				checkExpr, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				if err := p.expect(LX.T_RPAREN); err != nil {
					return nil, err
				}
				p.advance()
				col.Check = checkExpr
			case LX.T_UNIQUE:
				col.Unique = true
				p.advance()
				if p.current.Type == LX.T_KEY {
					p.advance()
				}
			}
		}

		// REQ000126: column-level REFERENCES clause (after other constraints)
		if p.current.Type == LX.T_REFERENCES {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			col.ReferencesTable = p.current.Lexeme
			p.advance()
			if p.current.Type == LX.T_LPAREN {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				col.ReferencesColumn = p.current.Lexeme
				p.advance()
				if err := p.expect(LX.T_RPAREN); err != nil {
					return nil, err
				}
				p.advance()
			}
			for p.current.Type == LX.T_ON {
				p.advance()
				if p.current.Type == LX.T_DELETE {
					p.advance()
					col.OnDelete = p.parseFKAction()
				} else if p.current.Type == LX.T_UPDATE {
					p.advance()
					col.OnUpdate = p.parseFKAction()
				}
			}
		}

		cols = append(cols, col)

		if p.current.Type == LX.T_COMMA {
			p.advance()
			continue
		}
		if p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE || p.current.Type == LX.T_FOREIGN {
			break
		}
		if p.current.Type == LX.T_RPAREN {
			break
		}
		break
	}

	var uniqueConstraints []UniqueKey
	var foreignKeys []ForeignKeyConstraint
	for p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE || p.current.Type == LX.T_FOREIGN {
		isPK := p.current.Type == LX.T_PRIMARY
		isFK := p.current.Type == LX.T_FOREIGN
		p.advance()
		if isPK {
			if err := p.expect(LX.T_KEY); err != nil {
				return nil, err
			}
			p.advance()
		} else if isFK {
			if err := p.expect(LX.T_KEY); err != nil {
				return nil, err
			}
			p.advance()
		} else if p.current.Type == LX.T_KEY {
			p.advance()
		}
		if err := p.expect(LX.T_LPAREN); err != nil {
			return nil, err
		}
		p.advance()
		// Parse one or more identifiers inside the parens.
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		firstName := p.current.Lexeme
		p.advance()
		names := []string{firstName}
		for p.current.Type == LX.T_COMMA {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			names = append(names, p.current.Lexeme)
			p.advance()
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
		if isFK {
			// FOREIGN KEY (cols) REFERENCES table(refCols) [ON DELETE/UPDATE action]
			if err := p.expect(LX.T_REFERENCES); err != nil {
				return nil, err
			}
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			refTable := p.current.Lexeme
			p.advance()
			var refCols []string
			if p.current.Type == LX.T_LPAREN {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				refCols = append(refCols, p.current.Lexeme)
				p.advance()
				for p.current.Type == LX.T_COMMA {
					p.advance()
					if err := p.expect(LX.T_IDENT); err != nil {
						return nil, err
					}
					refCols = append(refCols, p.current.Lexeme)
					p.advance()
				}
				if err := p.expect(LX.T_RPAREN); err != nil {
					return nil, err
				}
				p.advance()
			}
			fk := ForeignKeyConstraint{Columns: names, RefTable: refTable, RefColumns: refCols}
			for p.current.Type == LX.T_ON {
				p.advance()
				if p.current.Type == LX.T_DELETE {
					p.advance()
					fk.OnDelete = p.parseFKAction()
				} else if p.current.Type == LX.T_UPDATE {
					p.advance()
					fk.OnUpdate = p.parseFKAction()
				}
			}
			foreignKeys = append(foreignKeys, fk)
		} else if isPK {
			if len(names) != 1 {
				return nil, fmt.Errorf("ps: composite PRIMARY KEY (a, b) not supported, got %d columns", len(names))
			}
			pkName := names[0]
			pk = &pkName
		} else {
			uniqueConstraints = append(uniqueConstraints, UniqueKey{Cols: names})
		}
		// Allow comma between multiple UNIQUE / PRIMARY clauses.
		if p.current.Type == LX.T_COMMA {
			p.advance()
		}
	}

	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()

	for _, col := range cols {
		if col.PK {
			pk = &col.Name
			break
		}
	}

	return &CreateTable{Name: name, Cols: cols, PK: pk, UniqueConstraints: uniqueConstraints, ForeignKeys: foreignKeys}, nil
}

// parseFKAction parses CASCADE / RESTRICT / SET NULL / SET DEFAULT / NO ACTION.
func (p *Parser) parseFKAction() string {
	switch p.current.Type {
	case LX.T_CASCADE:
		p.advance()
		return "CASCADE"
	case LX.T_RESTRICT:
		p.advance()
		return "RESTRICT"
	case LX.T_SET:
		p.advance()
		if p.current.Type == LX.T_NULL {
			p.advance()
			return "SET NULL"
		}
		if p.current.Type == LX.T_DEFAULT {
			p.advance()
			return "SET DEFAULT"
		}
		return "SET"
	case LX.T_NO:
		p.advance()
		if p.current.Type == LX.T_ACTION {
			p.advance()
			return "NO ACTION"
		}
		return "NO"
	default:
		return "NO ACTION"
	}
}

func (p *Parser) parseDropTable() (*DropTable, error) {
	p.advance()

	if err := p.expect(LX.T_TABLE); err != nil {
		return nil, err
	}
	p.advance()

	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
	}
	if p.current.Type == LX.T_EXISTS {
		p.advance()
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &DropTable{Name: name}, nil
}

func (p *Parser) parseExplain() (*ExplainStmt, error) {
	p.advance() // consume EXPLAIN

	mode := ExplainNormal
	if p.current.Type == LX.T_QUERY {
		// Use lexer Peek to check if QUERY is followed by PLAN
		next := p.lex.Peek()
		if next.Type == LX.T_PLAN {
			mode = ExplainQueryPlan
			p.advance() // consume QUERY
			p.advance() // consume PLAN
		} else {
			// EXPLAIN QUERY without PLAN is invalid SQL
			return nil, &SyntaxError{
				Input:    p.lex.Input(),
				Line:     p.current.Line,
				Col:      p.current.Col,
				Expected: "PLAN",
				Got:      tokenName(p.current.Type),
				Lexeme:   p.current.Lexeme,
			}
		}
	}

	// Parse the inner statement without resetting the parser state.
	// We use the same dispatch logic as Parse() but without reset().
	var inner Stmt
	var innerErr error
	switch p.current.Type {
	case LX.T_SELECT:
		inner, innerErr = p.parseSelect()
	case LX.T_INSERT:
		inner, innerErr = p.parseInsert()
	case LX.T_UPDATE:
		inner, innerErr = p.parseUpdate()
	case LX.T_DELETE:
		inner, innerErr = p.parseDelete()
	case LX.T_CREATE:
		next := p.lex.Peek().Type
		if next == LX.T_INDEX {
			inner, innerErr = p.parseCreateIndex()
		} else if next == LX.T_UNIQUE && p.lex.Peek2().Type == LX.T_INDEX {
			inner, innerErr = p.parseCreateIndex()
		} else if next == LX.T_VIEW {
			inner, innerErr = p.parseCreateView()
		} else {
			inner, innerErr = p.parseCreateTable()
		}
	case LX.T_DROP:
		if p.lex.Peek().Type == LX.T_INDEX {
			inner, innerErr = p.parseDropIndex()
		} else {
			inner, innerErr = p.parseDropTable()
		}
	case LX.T_EXPLAIN:
		inner, innerErr = p.parseExplain()
	case LX.T_ANALYZE:
		inner, innerErr = p.parseAnalyze()
	case LX.T_VACUUM:
		inner, innerErr = p.parseVacuum()
	default:
		innerErr = &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "statement",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	if innerErr != nil {
		return nil, innerErr
	}

	return &ExplainStmt{Mode: mode, Inner: inner}, nil
}

func (p *Parser) parseAnalyze() (*AnalyzeStmt, error) {
	p.advance() // consume ANALYZE

	stmt := &AnalyzeStmt{}
	if p.current.Type == LX.T_IDENT {
		stmt.Table = p.current.Lexeme
		p.advance()
	}

	return stmt, nil
}

func (p *Parser) parseVacuum() (*VacuumStmt, error) {
	p.advance() // consume VACUUM

	stmt := &VacuumStmt{}
	if p.current.Type == LX.T_IDENT {
		stmt.Table = p.current.Lexeme
		p.advance()
	}

	return stmt, nil
}

func (p *Parser) parsePragma() (*PragmaStmt, error) {
	p.advance() // consume PRAGMA

	if p.current.Type != LX.T_IDENT {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "identifier after PRAGMA",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}

	stmt := &PragmaStmt{Name: p.current.Lexeme}
	p.advance()

	// Optional: PRAGMA name = value
	if p.current.Type == LX.T_EQ {
		p.advance()
		if p.current.Type == LX.T_IDENT || p.current.Type == LX.T_STRING {
			stmt.Value = p.current.Lexeme
			p.advance()
		} else if p.current.Type == LX.T_INT {
			stmt.Value = p.current.Lexeme
			p.advance()
		}
	}

	return stmt, nil
}

func (p *Parser) parseReturning() ([]Expr, error) {
	if p.current.Type != LX.T_RETURNING {
		return nil, nil
	}
	p.advance() // consume RETURNING

	var cols []Expr
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		cols = append(cols, expr)
		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}
	return cols, nil
}

func (p *Parser) parseCast() (Expr, error) {
	p.advance()
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_AS); err != nil {
		return nil, err
	}
	p.advance()
	typ, err := p.parseCastType()
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()
	return &CastExpr{Expr: expr, Type: typ}, nil
}

func (p *Parser) parseExists() (Expr, error) {
	p.advance()
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()
	sel, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()
	return &ExistsExpr{Subquery: sel}, nil
}

// parseInterval parses INTERVAL 'value' UNIT. REQ000288: validates unit.
func (p *Parser) parseInterval() (Expr, error) {
	p.advance() // consume INTERVAL
	if err := p.expect(LX.T_STRING); err != nil {
		return nil, err
	}
	val := p.current.Literal.(string)
	p.advance()
	if p.current.Type == LX.T_IDENT {
		unit := strings.ToUpper(p.current.Lexeme)
		validUnits := map[string]bool{
			"YEAR": true, "YEARS": true,
			"MONTH": true, "MONTHS": true,
			"DAY": true, "DAYS": true,
			"HOUR": true, "HOURS": true,
			"MINUTE": true, "MINUTES": true,
			"SECOND": true, "SECONDS": true,
		}
		if !validUnits[unit] {
			return nil, &SyntaxError{
				Input:  p.lex.Input(),
				Line:   p.current.Line,
				Col:    p.current.Col,
				Got:    unit,
				Lexeme: p.current.Lexeme,
			}
		}
		p.advance()
		return &IntervalLiteral{Value: val, Unit: unit}, nil
	}
	return &IntervalLiteral{Value: val, Unit: "DAY"}, nil
}

// parseWindowBuiltin parses window built-in functions (ROW_NUMBER, RANK, etc.).
func (p *Parser) parseWindowBuiltin() (Expr, error) {
	name := strings.ToUpper(p.current.Lexeme)
	p.advance()
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()
	var args []Expr
	if p.current.Type != LX.T_RPAREN {
		for {
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()
	return p.parseWindowFunc(name, args)
}

// parseWindowFunc parses the OVER clause after a function name.
func (p *Parser) parseWindowFunc(name string, args []Expr) (Expr, error) {
	if err := p.expect(LX.T_OVER); err != nil {
		return nil, err
	}
	p.advance()
	spec, err := p.parseWindowSpec()
	if err != nil {
		return nil, err
	}
	return &WindowFunc{Name: name, Args: args, Over: spec}, nil
}

// parseWindowSpec parses (PARTITION BY ... ORDER BY ... frame).
func (p *Parser) parseWindowSpec() (*WindowSpec, error) {
	spec := &WindowSpec{}
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()

	// PARTITION BY
	if p.current.Type == LX.T_PARTITION {
		p.advance()
		if err := p.expect(LX.T_BY); err != nil {
			return nil, err
		}
		p.advance()
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			spec.PartitionBy = append(spec.PartitionBy, e)
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}

	// ORDER BY
	if p.current.Type == LX.T_ORDER {
		p.advance()
		if err := p.expect(LX.T_BY); err != nil {
			return nil, err
		}
		p.advance()
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			desc := false
			if p.current.Type == LX.T_ASC {
				p.advance()
			} else if p.current.Type == LX.T_DESC {
				desc = true
				p.advance()
			}
			spec.OrderBy = append(spec.OrderBy, OrderItem{Expr: e, Desc: desc})
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}

	// Frame clause (ROWS or RANGE)
	if p.current.Type == LX.T_ROWS || p.current.Type == LX.T_RANGE {
		frameType := p.current.Lexeme
		p.advance()
		frame, err := p.parseWindowFrame(frameType)
		if err != nil {
			return nil, err
		}
		spec.Frame = frame
	}

	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()
	return spec, nil
}

// parseWindowFrame parses ROWS/RANGE BETWEEN start AND end.
func (p *Parser) parseWindowFrame(frameType string) (*WindowFrame, error) {
	frame := &WindowFrame{Type: strings.ToUpper(frameType)}
	if err := p.expect(LX.T_BETWEEN); err != nil {
		return nil, err
	}
	p.advance()
	start, err := p.parseFrameBound()
	if err != nil {
		return nil, err
	}
	frame.Start = start
	if err := p.expect(LX.T_AND); err != nil {
		return nil, err
	}
	p.advance()
	end, err := p.parseFrameBound()
	if err != nil {
		return nil, err
	}
	frame.End = end
	return frame, nil
}

// parseFrameBound parses a single frame boundary.
func (p *Parser) parseFrameBound() (FrameBound, error) {
	if p.current.Type == LX.T_UNBOUNDED {
		p.advance()
		if p.current.Type == LX.T_PRECEDING {
			p.advance()
			return FrameBound{Type: "UNBOUNDED_PRECEDING"}, nil
		}
		if p.current.Type == LX.T_FOLLOWING {
			p.advance()
			return FrameBound{Type: "UNBOUNDED_FOLLOWING"}, nil
		}
	}
	if p.current.Type == LX.T_CURRENT {
		p.advance()
		if err := p.expect(LX.T_ROW); err != nil {
			return FrameBound{}, err
		}
		p.advance()
		return FrameBound{Type: "CURRENT_ROW"}, nil
	}
	// n PRECEDING or n FOLLOWING
	expr, err := p.parseExpr()
	if err != nil {
		return FrameBound{}, err
	}
	if p.current.Type == LX.T_PRECEDING {
		p.advance()
		return FrameBound{Type: "PRECEDING", Offset: expr}, nil
	}
	if p.current.Type == LX.T_FOLLOWING {
		p.advance()
		return FrameBound{Type: "FOLLOWING", Offset: expr}, nil
	}
	return FrameBound{}, &SyntaxError{
		Input:  p.lex.Input(),
		Line:   p.current.Line,
		Col:    p.current.Col,
		Got:    tokenName(p.current.Type),
		Lexeme: p.current.Lexeme,
	}
}

// parseCastType parses a type reference optionally followed by
// a size or precision/scale specifier (REQ000207).
// Supports: VARCHAR(N), CHAR(N), DECIMAL(P,S), NUMERIC(P,S).
func (p *Parser) parseCastType() (*TypeInfo, error) {
	info := &TypeInfo{}

	switch p.current.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		info.Type = int(p.current.Type)
		p.advance()
	case LX.T_FLOAT_KW:
		info.Type = int(LX.T_FLOAT_KW)
		p.advance()
	case LX.T_BOOL:
		info.Type = int(LX.T_BOOL)
		p.advance()
	case LX.T_TEXT:
		info.Type = int(LX.T_TEXT)
		p.advance()
	case LX.T_BLOB:
		info.Type = int(LX.T_BLOB)
		p.advance()
	case LX.T_TIMESTAMP:
		info.Type = int(LX.T_TIMESTAMP)
		p.advance()
	case LX.T_VARCHAR:
		info.Type = int(LX.T_VARCHAR)
		p.advance()
		if err := p.parseTypeSize(&info.Size); err != nil {
			return nil, err
		}
	case LX.T_NUMERIC:
		info.Type = int(LX.T_NUMERIC)
		p.advance()
		if err := p.parseTypePrecision(&info.Precision, &info.Scale); err != nil {
			return nil, err
		}
		info.Size = info.Precision // use Size for ColDef compatibility
	case LX.T_DECIMAL:
		info.Type = int(LX.T_DECIMAL)
		p.advance()
		if err := p.parseTypePrecision(&info.Precision, &info.Scale); err != nil {
			return nil, err
		}
		info.Size = info.Precision // use Size for ColDef compatibility
	case LX.T_DATE:
		info.Type = int(LX.T_DATE)
		p.advance()
	case LX.T_TIME:
		info.Type = int(LX.T_TIME)
		p.advance()
	case LX.T_JSON:
		info.Type = int(LX.T_JSON)
		p.advance()
	default:
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    "type " + tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
	return info, nil
}

// parseTypeSize parses (N) for VARCHAR(N), CHAR(N).
func (p *Parser) parseTypeSize(size *int) error {
	if p.current.Type != LX.T_LPAREN {
		return nil // optional
	}
	p.advance()
	if p.current.Type != LX.T_INT {
		return &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    "integer",
			Lexeme: p.current.Lexeme,
		}
	}
	n := 0
	fmt.Sscanf(p.current.Lexeme, "%d", &n)
	*size = n
	p.advance()
	if err := p.expect(LX.T_RPAREN); err != nil {
		return err
	}
	p.advance() // consume the ')'
	return nil
}

// parseTypePrecision parses (P,S) for DECIMAL(P,S), NUMERIC(P,S).
func (p *Parser) parseTypePrecision(precision, scale *int) error {
	if p.current.Type != LX.T_LPAREN {
		return nil // optional
	}
	p.advance()
	if p.current.Type != LX.T_INT {
		return &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    "integer",
			Lexeme: p.current.Lexeme,
		}
	}
	n := 0
	fmt.Sscanf(p.current.Lexeme, "%d", &n)
	*precision = n
	p.advance()

	if p.current.Type == LX.T_COMMA {
		p.advance()
		if p.current.Type != LX.T_INT {
			return &SyntaxError{
				Input:  p.lex.Input(),
				Line:   p.current.Line,
				Col:    p.current.Col,
				Got:    "integer",
				Lexeme: p.current.Lexeme,
			}
		}
		m := 0
		fmt.Sscanf(p.current.Lexeme, "%d", &m)
		*scale = m
		p.advance()
	}

	if err := p.expect(LX.T_RPAREN); err != nil {
		return err
	}
	p.advance() // consume the ')'
	return nil
}

func (p *Parser) parseCaseExpr() (Expr, error) {
	p.advance()

	var expr Expr
	var whenList []WhenClause
	var elseExpr Expr

	if p.current.Type != LX.T_WHEN {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		expr = e
	}

	for p.current.Type == LX.T_WHEN {
		p.advance()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(LX.T_THEN); err != nil {
			return nil, err
		}
		p.advance()
		then, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		whenList = append(whenList, WhenClause{Cond: cond, Then: then})
	}

	if p.current.Type == LX.T_ELSE {
		p.advance()
		var err error
		elseExpr, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}

	if err := p.expect(LX.T_END); err != nil {
		return nil, err
	}
	p.advance()

	return &CaseExpr{Expr: expr, WhenList: whenList, Else: elseExpr}, nil
}

func parseFloat(s string) float64 {
	var val float64
	var dot bool
	var div float64 = 1
	for _, c := range s {
		if c == '.' {
			dot = true
			continue
		}
		if !dot {
			val = val*10 + float64(c-'0')
		} else {
			div *= 10
			val += float64(c-'0') / div
		}
	}
	return val
}

// parseCoalesce parses: COALESCE(expr[,expr]...)
// Returns the first non-NULL expression (left-to-right).
// parseCoalesce parses: COALESCE(expr1, expr2, ...)
// Returns the first non-NULL argument, or NULL if all are NULL.
func (p *Parser) parseCoalesce() (Expr, error) {
	p.advance() // consume COALESCE

	var args []Expr

	// Optional parens
	if p.current.Type == LX.T_LPAREN {
		p.advance()
		for {
			arg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)

			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
	} else {
		// Special form: COALESCE expr1, expr2, ... (no parens)
		for {
			arg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)

			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}

	return &FunctionCall{Name: "COALESCE", Args: args}, nil
}

// parseNullif parses: NULLIF(expr1, expr2)
// Returns NULL if expr1 = expr2, otherwise expr1.
func (p *Parser) parseNullif() (Expr, error) {
	p.advance() // consume NULLIF

	var arg1, arg2 Expr
	var err error

	if p.current.Type == LX.T_LPAREN {
		p.advance()
		arg1, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(LX.T_COMMA); err != nil {
			return nil, err
		}
		p.advance()
		arg2, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
	} else {
		// Special form: NULLIF expr1, expr2 (no parens)
		arg1, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(LX.T_COMMA); err != nil {
			return nil, err
		}
		p.advance()
		arg2, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}

	return &FunctionCall{Name: "NULLIF", Args: []Expr{arg1, arg2}}, nil
}

func (p *Parser) parseWith() (*WithStmt, error) {
	p.advance() // consume WITH

	var ctes []*CommonTableExpr
	for {
		// Parse CTE name
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		name := p.current.Lexeme
		p.advance()

		// Optional column aliases
		var cols []string
		if p.current.Type == LX.T_LPAREN {
			p.advance()
			for {
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				cols = append(cols, p.current.Lexeme)
				p.advance()
				if p.current.Type != LX.T_COMMA {
					break
				}
				p.advance()
			}
			if err := p.expect(LX.T_RPAREN); err != nil {
				return nil, err
			}
			p.advance()
		}

		// AS (query)
		if err := p.expect(LX.T_AS); err != nil {
			return nil, err
		}
		p.advance()
		if err := p.expect(LX.T_LPAREN); err != nil {
			return nil, err
		}
		p.advance()

		query, err := p.Parse()
		if err != nil {
			return nil, err
		}

		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()

		ctes = append(ctes, &CommonTableExpr{
			Name:  name,
			Cols:  cols,
			Query: query,
		})

		// Check for more CTEs (comma-separated)
		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}

	// Parse the main query
	inner, err := p.Parse()
	if err != nil {
		return nil, err
	}

	return &WithStmt{CTEs: ctes, Inner: inner}, nil
}

func (p *Parser) parseSavepoint() (*SavepointStmt, error) {
	p.advance() // consume SAVEPOINT

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &SavepointStmt{Name: name}, nil
}

func (p *Parser) parseReleaseSavepoint() (*ReleaseSavepointStmt, error) {
	p.advance() // consume RELEASE

	// Optional SAVEPOINT keyword
	if p.current.Type == LX.T_SAVEPOINT {
		p.advance()
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &ReleaseSavepointStmt{Name: name}, nil
}

func (p *Parser) parseRollbackTo() (*RollbackToStmt, error) {
	p.advance() // consume ROLLBACK
	p.advance() // consume TO

	// Optional SAVEPOINT keyword
	if p.current.Type == LX.T_SAVEPOINT {
		p.advance()
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &RollbackToStmt{Name: name}, nil
}

// parseCreateIndex parses `CREATE [UNIQUE] INDEX <name> ON <table> (<cols>)`.
// REQ000251 — secondary indexes MVP.
// On entry, current token is CREATE.
func (p *Parser) parseCreateIndex() (*CreateIndexStmt, error) {
	unique := false
	p.advance() // consume CREATE
	// current is now INDEX, or UNIQUE if CREATE UNIQUE INDEX
	if p.current.Type == LX.T_UNIQUE {
		unique = true
		p.advance() // consume UNIQUE
	}
	if err := p.expect(LX.T_INDEX); err != nil {
		return nil, err
	}
	p.advance() // consume INDEX
	// Read index name
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()
	if err := p.expect(LX.T_ON); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()
	cols, err := p.parseIdentList()
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()
	return &CreateIndexStmt{
		Name:    name,
		Table:   table,
		Columns: cols,
		Unique:  unique,
	}, nil
}

// parseIdentList parses a comma-separated list of identifiers
// until the closing paren or end of statement.
func (p *Parser) parseIdentList() ([]string, error) {
	cols := []string{}
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	cols = append(cols, p.current.Lexeme)
	p.advance()
	for p.current.Type == LX.T_COMMA {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		cols = append(cols, p.current.Lexeme)
		p.advance()
	}
	return cols, nil
}

// parseDropIndex parses `DROP INDEX <name>`. REQ000251.
func (p *Parser) parseDropIndex() (*DropIndexStmt, error) {
	p.advance() // consume DROP
	if err := p.expect(LX.T_INDEX); err != nil {
		return nil, err
	}
	p.advance() // consume INDEX
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()
	return &DropIndexStmt{Name: name}, nil
}

// parseSet parses SET TRANSACTION ISOLATION LEVEL ... (REQ000123).
func (p *Parser) parseSet() (*SetTransactionStmt, error) {
	p.advance() // consume SET
	if err := p.expect(LX.T_TRANSACTION); err != nil {
		return nil, err
	}
	p.advance() // consume TRANSACTION
	if err := p.expect(LX.T_ISOLATION); err != nil {
		return nil, err
	}
	p.advance() // consume ISOLATION
	if err := p.expect(LX.T_LEVEL); err != nil {
		return nil, err
	}
	p.advance() // consume LEVEL

	// Parse isolation level
	level := ""
	switch p.current.Type {
	case LX.T_READ:
		p.advance()
		switch p.current.Type {
		case LX.T_UNCOMMITTED:
			level = "READ UNCOMMITTED"
			p.advance()
		case LX.T_COMMITTED:
			level = "READ COMMITTED"
			p.advance()
		default:
			return nil, &SyntaxError{
				Input:  p.lex.Input(),
				Line:   p.current.Line,
				Col:    p.current.Col,
				Got:    tokenName(p.current.Type),
				Lexeme: p.current.Lexeme,
			}
		}
	case LX.T_REPEATABLE:
		p.advance()
		if err := p.expect(LX.T_READ); err != nil {
			return nil, err
		}
		level = "REPEATABLE READ"
		p.advance()
	case LX.T_SERIALIZABLE:
		level = "SERIALIZABLE"
		p.advance()
	default:
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
	return &SetTransactionStmt{Level: level}, nil
}

// parseCreateView parses CREATE VIEW name AS SELECT ... (REQ000240).
func (p *Parser) parseCreateView() (*CreateViewStmt, error) {
	p.advance() // consume CREATE
	if err := p.expect(LX.T_VIEW); err != nil {
		return nil, err
	}
	p.advance() // consume VIEW
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()
	if err := p.expect(LX.T_AS); err != nil {
		return nil, err
	}
	p.advance() // consume AS
	sel, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	return &CreateViewStmt{Name: name, As: sel}, nil
}

// parseAlterTable parses ALTER TABLE name ADD/DROP COLUMN col (REQ000243).
func (p *Parser) parseAlterTable() (*AlterTableStmt, error) {
	p.advance() // consume ALTER
	if err := p.expect(LX.T_TABLE); err != nil {
		return nil, err
	}
	p.advance() // consume TABLE
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()

	switch p.current.Type {
	case LX.T_ADD:
		p.advance() // consume ADD
		if p.current.Type == LX.T_COLUMN {
			p.advance() // consume COLUMN (optional)
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		col := p.current.Lexeme
		p.advance()
		// Skip type and DEFAULT clause (schema migration deferred)
		for p.current.Type != LX.T_SEMICOLON && p.current.Type != LX.T_EOF &&
			p.current.Type != LX.T_COMMA {
			p.advance()
		}
		return &AlterTableStmt{Table: table, Action: "ADD COLUMN", Column: col}, nil

	case LX.T_DROP:
		p.advance() // consume DROP
		if p.current.Type == LX.T_COLUMN {
			p.advance() // consume COLUMN (optional)
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		col := p.current.Lexeme
		p.advance()
		return &AlterTableStmt{Table: table, Action: "DROP COLUMN", Column: col}, nil

	case LX.T_RENAME:
		p.advance() // consume RENAME
		if p.current.Type == LX.T_TO {
			p.advance() // consume TO (optional)
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		newName := p.current.Lexeme
		p.advance()
		return &AlterTableStmt{Table: table, Action: "RENAME", Column: newName}, nil

	default:
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
}
