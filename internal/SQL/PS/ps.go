package PS

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

type Parser struct {
	lex        *LX.Lexer
	current    LX.Token
	paramIndex int
}

func NewParser(input string) *Parser {
	return &Parser{
		lex:     LX.NewLexer(input),
		current: LX.Token{Type: LX.T_EOF, Lexeme: "", Line: 0, Col: 0},
	}
}

func (p *Parser) reset() {
	p.paramIndex = 0
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
	LX.T_EOF:         "EOF",
	LX.T_IDENT:       "identifier",
	LX.T_STRING:      "string literal",
	LX.T_INT:         "integer",
	LX.T_FLOAT:       "float",
	LX.T_BIND:        "?",
	LX.T_EQ:          "=",
	LX.T_NE:          "!=",
	LX.T_LT:          "<",
	LX.T_LE:          "<=",
	LX.T_GT:          ">",
	LX.T_GE:          ">=",
	LX.T_PLUS:        "+",
	LX.T_MINUS:       "-",
	LX.T_STAR:        "*",
	LX.T_SLASH:       "/",
	LX.T_LPAREN:      "(",
	LX.T_RPAREN:      ")",
	LX.T_COMMA:       ",",
	LX.T_DOT:         ".",
	LX.T_SEMICOLON:   ";",
	LX.T_COLON:       ":",
	LX.T_CREATE:      "CREATE",
	LX.T_DROP:        "DROP",
	LX.T_INSERT:      "INSERT",
	LX.T_UPDATE:      "UPDATE",
	LX.T_DELETE:      "DELETE",
	LX.T_SELECT:      "SELECT",
	LX.T_FROM:        "FROM",
	LX.T_WHERE:       "WHERE",
	LX.T_AND:         "AND",
	LX.T_OR:          "OR",
	LX.T_NOT:         "NOT",
	LX.T_IN:          "IN",
	LX.T_BETWEEN:     "BETWEEN",
	LX.T_LIKE:        "LIKE",
	LX.T_IS:          "IS",
	LX.T_NULL:        "NULL",
	LX.T_BEGIN:       "BEGIN",
	LX.T_COMMIT:      "COMMIT",
	LX.T_ROLLBACK:    "ROLLBACK",
	LX.T_AS:          "AS",
	LX.T_BY:          "BY",
	LX.T_ASC:         "ASC",
	LX.T_DESC:        "DESC",
	LX.T_LIMIT:       "LIMIT",
	LX.T_OFFSET:      "OFFSET",
	LX.T_TABLE:       "TABLE",
	LX.T_INDEX:       "INDEX",
	LX.T_PRIMARY:     "PRIMARY",
	LX.T_KEY:         "KEY",
	LX.T_NOTNULL:     "NOTNULL",
	LX.T_DEFAULT:     "DEFAULT",
	LX.T_UNIQUE:      "UNIQUE",
	LX.T_INT_KW:      "INTEGER",
	LX.T_BIGINT:      "BIGINT",
	LX.T_FLOAT_KW:    "FLOAT",
	LX.T_BOOL:        "BOOLEAN",
	LX.T_TEXT:        "TEXT",
	LX.T_BLOB:        "BLOB",
	LX.T_VARCHAR:     "VARCHAR",
	LX.T_TIMESTAMP:   "TIMESTAMP",
	LX.T_VALUES:      "VALUES",
	LX.T_SET:         "SET",
	LX.T_INTO:        "INTO",
	LX.T_ORDER:       "ORDER",
	LX.T_JOIN:        "JOIN",
	LX.T_LEFT:        "LEFT",
	LX.T_RIGHT:       "RIGHT",
	LX.T_INNER:       "INNER",
	LX.T_CROSS:       "CROSS",
	LX.T_ON:          "ON",
	LX.T_USING:       "USING",
	LX.T_GROUP:       "GROUP",
	LX.T_HAVING:      "HAVING",
	LX.T_COUNT:       "COUNT",
	LX.T_SUM:         "SUM",
	LX.T_AVG:         "AVG",
	LX.T_MIN:         "MIN",
	LX.T_MAX:         "MAX",
	LX.T_DISTINCT:    "DISTINCT",
	LX.T_CASE:        "CASE",
	LX.T_WHEN:        "WHEN",
	LX.T_THEN:        "THEN",
	LX.T_ELSE:        "ELSE",
	LX.T_END:         "END",
	LX.T_CAST:        "CAST",
	LX.T_EXISTS:      "EXISTS",
	LX.T_TRUE:        "TRUE",
	LX.T_FALSE:       "FALSE",
	LX.T_EXPLAIN:     "EXPLAIN",
	LX.T_QUERY:       "QUERY",
	LX.T_PLAN:        "PLAN",
	LX.T_RETURNING:   "RETURNING",
	LX.T_CONFLICT:    "CONFLICT",
	LX.T_DO:          "DO",
	LX.T_NOTHING:     "NOTHING",
	LX.T_EXCLUDED:    "EXCLUDED",
	LX.T_WITH:        "WITH",
	LX.T_SAVEPOINT:   "SAVEPOINT",
	LX.T_RELEASE:     "RELEASE",
	LX.T_TO:          "TO",
	LX.T_INTERVAL:    "INTERVAL",
	LX.T_OVER:        "OVER",
	LX.T_PARTITION:   "PARTITION",
	LX.T_ROWS:        "ROWS",
	LX.T_RANGE:       "RANGE",
	LX.T_PRECEDING:   "PRECEDING",
	LX.T_FOLLOWING:   "FOLLOWING",
	LX.T_CURRENT:     "CURRENT",
	LX.T_UNBOUNDED:   "UNBOUNDED",
	LX.T_ROW_NUMBER:  "ROW_NUMBER",
	LX.T_RANK:        "RANK",
	LX.T_DENSE_RANK:  "DENSE_RANK",
	LX.T_LAG:         "LAG",
	LX.T_LEAD:        "LEAD",
	LX.T_FIRST_VALUE: "FIRST_VALUE",
	LX.T_LAST_VALUE:  "LAST_VALUE",
	LX.T_NTH_VALUE: "NTH_VALUE",
	LX.T_ROW:       "ROW",
	LX.T_TRANSACTION: "TRANSACTION",
	LX.T_ISOLATION:  "ISOLATION",
	LX.T_LEVEL:      "LEVEL",
	LX.T_READ:       "READ",
	LX.T_COMMITTED:  "COMMITTED",
	LX.T_UNCOMMITTED: "UNCOMMITTED",
	LX.T_REPEATABLE: "REPEATABLE",
	LX.T_SERIALIZABLE: "SERIALIZABLE",
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
	case LX.T_IDENT:
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
		return &Ident{Name: name}, nil
	case LX.T_COUNT, LX.T_SUM, LX.T_AVG, LX.T_MIN, LX.T_MAX:
		name := p.current.Lexeme
		p.advance()
		if err := p.expect(LX.T_LPAREN); err != nil {
			return nil, err
		}
		p.advance()
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
		agg := &AggregateFunc{Name: name, Arg: arg}
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
	if p.current.Type == LX.T_NOT || p.current.Type == LX.T_MINUS || p.current.Type == LX.T_PLUS {
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
	return expr, nil
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
		LX.T_LIKE, LX.T_IS:
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
	case LX.T_EQ, LX.T_NE, LX.T_LT, LX.T_LE, LX.T_GT, LX.T_GE, LX.T_LIKE, LX.T_IS:
		return 3
	case LX.T_PLUS, LX.T_MINUS:
		return 4
	case LX.T_STAR, LX.T_SLASH:
		return 5
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
		// CREATE TABLE vs CREATE INDEX — disambiguate by looking
		// ahead 1-2 tokens (CREATE [UNIQUE] INDEX vs CREATE TABLE).
		next := p.lex.Peek().Type
		if next == LX.T_INDEX {
			stmt, err = p.parseCreateIndex()
		} else if next == LX.T_UNIQUE && p.lex.Peek2().Type == LX.T_INDEX {
			stmt, err = p.parseCreateIndex()
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

func (p *Parser) parseSelect() (*Select, error) {
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

	var fromAlias string
	if p.current.Type == LX.T_AS {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		fromAlias = p.current.Lexeme
		p.advance()
	}

	var joins []JoinClause
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

	var orderBy []OrderItem
	if p.current.Type == LX.T_ORDER {
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
			return nil, err
		}
		limit = l
	}

	var offset Expr
	if p.current.Type == LX.T_OFFSET {
		p.advance()
		o, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		offset = o
	}

	return &Select{
		Cols:      cols,
		From:      from,
		FromAlias: fromAlias,
		Joins:     joins,
		Where:     where,
		GroupBy:   groupBy,
		Having:    having,
		OrderBy:   orderBy,
		Limit:     limit,
		Offset:    offset,
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

		// Handle EXCLUDED.col
		if p.current.Type == LX.T_DOT {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			// For now, treat EXCLUDED.col as a reference
			p.advance()
		} else {
			if err := p.expect(LX.T_EQ); err != nil {
				return nil, err
			}
			p.advance()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			setClauses = append(setClauses, Pair{Col: col, Val: val})
		}

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
		if p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE {
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

		cols = append(cols, col)

		if p.current.Type == LX.T_COMMA {
			p.advance()
			continue
		}
		if p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE {
			break
		}
		if p.current.Type == LX.T_RPAREN {
			break
		}
		break
	}

	var uniqueConstraints []UniqueKey
	for p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE {
		isPK := p.current.Type == LX.T_PRIMARY
		p.advance()
		if isPK {
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
		if isPK {
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

	return &CreateTable{Name: name, Cols: cols, PK: pk, UniqueConstraints: uniqueConstraints}, nil
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

	inner, err := p.Parse()
	if err != nil {
		return nil, err
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

// parseInterval parses INTERVAL 'value' UNIT.
func (p *Parser) parseInterval() (Expr, error) {
	p.advance() // consume INTERVAL
	if err := p.expect(LX.T_STRING); err != nil {
		return nil, err
	}
	val := p.current.Literal.(string)
	p.advance()
	if p.current.Type == LX.T_IDENT {
		unit := strings.ToUpper(p.current.Lexeme)
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
