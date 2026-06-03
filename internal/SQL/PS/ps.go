package PS

import (
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
	LX.T_EOF:       "EOF",
	LX.T_IDENT:     "identifier",
	LX.T_STRING:    "string literal",
	LX.T_INT:       "integer",
	LX.T_FLOAT:     "float",
	LX.T_BIND:      "?",
	LX.T_EQ:        "=",
	LX.T_NE:        "!=",
	LX.T_LT:        "<",
	LX.T_LE:        "<=",
	LX.T_GT:        ">",
	LX.T_GE:        ">=",
	LX.T_PLUS:      "+",
	LX.T_MINUS:     "-",
	LX.T_STAR:      "*",
	LX.T_SLASH:     "/",
	LX.T_LPAREN:    "(",
	LX.T_RPAREN:    ")",
	LX.T_COMMA:     ",",
	LX.T_DOT:       ".",
	LX.T_SEMICOLON: ";",
	LX.T_COLON:     ":",
	LX.T_CREATE:    "CREATE",
	LX.T_DROP:      "DROP",
	LX.T_INSERT:    "INSERT",
	LX.T_UPDATE:    "UPDATE",
	LX.T_DELETE:    "DELETE",
	LX.T_SELECT:    "SELECT",
	LX.T_FROM:      "FROM",
	LX.T_WHERE:     "WHERE",
	LX.T_AND:       "AND",
	LX.T_OR:        "OR",
	LX.T_NOT:       "NOT",
	LX.T_IN:        "IN",
	LX.T_BETWEEN:   "BETWEEN",
	LX.T_LIKE:      "LIKE",
	LX.T_IS:        "IS",
	LX.T_NULL:      "NULL",
	LX.T_BEGIN:     "BEGIN",
	LX.T_COMMIT:    "COMMIT",
	LX.T_ROLLBACK:  "ROLLBACK",
	LX.T_AS:        "AS",
	LX.T_BY:        "BY",
	LX.T_ASC:       "ASC",
	LX.T_DESC:      "DESC",
	LX.T_LIMIT:     "LIMIT",
	LX.T_OFFSET:    "OFFSET",
	LX.T_TABLE:     "TABLE",
	LX.T_INDEX:     "INDEX",
	LX.T_PRIMARY:   "PRIMARY",
	LX.T_KEY:       "KEY",
	LX.T_NOTNULL:   "NOTNULL",
	LX.T_DEFAULT:   "DEFAULT",
	LX.T_UNIQUE:    "UNIQUE",
	LX.T_INT_KW:    "INTEGER",
	LX.T_BIGINT:    "BIGINT",
	LX.T_FLOAT_KW:  "FLOAT",
	LX.T_BOOL:      "BOOLEAN",
	LX.T_TEXT:      "TEXT",
	LX.T_BLOB:      "BLOB",
	LX.T_VARCHAR:   "VARCHAR",
	LX.T_TIMESTAMP: "TIMESTAMP",
	LX.T_VALUES:    "VALUES",
	LX.T_SET:       "SET",
	LX.T_INTO:      "INTO",
	LX.T_ORDER:     "ORDER",
	LX.T_JOIN:      "JOIN",
	LX.T_LEFT:      "LEFT",
	LX.T_RIGHT:     "RIGHT",
	LX.T_INNER:     "INNER",
	LX.T_CROSS:     "CROSS",
	LX.T_ON:        "ON",
	LX.T_USING:     "USING",
	LX.T_GROUP:     "GROUP",
	LX.T_HAVING:    "HAVING",
	LX.T_COUNT:     "COUNT",
	LX.T_SUM:       "SUM",
	LX.T_AVG:       "AVG",
	LX.T_MIN:       "MIN",
	LX.T_MAX:       "MAX",
	LX.T_DISTINCT:  "DISTINCT",
	LX.T_CASE:      "CASE",
	LX.T_WHEN:      "WHEN",
	LX.T_THEN:      "THEN",
	LX.T_ELSE:      "ELSE",
	LX.T_END:       "END",
	LX.T_CAST:      "CAST",
	LX.T_EXISTS:    "EXISTS",
	LX.T_TRUE:      "TRUE",
	LX.T_FALSE:     "FALSE",
}

func (p *Parser) peek() LX.Token {
	return p.lex.Peek()
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
		return &AggregateFunc{Name: name, Arg: arg}, nil
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
		stmt, err = p.parseCreateTable()
	case LX.T_DROP:
		stmt, err = p.parseDropTable()
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

	var where Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		where = w
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
		Where:     where,
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

	return &Insert{Table: table, Cols: cols, Values: values}, nil
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

	return &Update{Table: table, Set: set, Where: where}, nil
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

	return &Delete{Table: table, Where: where}, nil
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

		colType := p.current.Type
		p.advance()

		col := NewColDef(colName, int(colType))

		for p.current.Type == LX.T_NOTNULL || p.current.Type == LX.T_PRIMARY ||
			p.current.Type == LX.T_DEFAULT || p.current.Type == LX.T_UNIQUE ||
			p.current.Type == LX.T_NOT {
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
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		pkName := p.current.Lexeme
		pk = &pkName
		p.advance()
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
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

	return &CreateTable{Name: name, Cols: cols, PK: pk}, nil
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

func parseInt(s string) int64 {
	val, _ := LX.ParseIntLiteral(s)
	return val
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

func (p *Parser) parseCastType() (int, error) {
	var typ int
	switch p.current.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		typ = int(LX.T_INT_KW)
	case LX.T_FLOAT_KW:
		typ = int(LX.T_FLOAT_KW)
	case LX.T_BOOL:
		typ = int(LX.T_BOOL)
	case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
		typ = int(LX.T_TEXT)
	case LX.T_TIMESTAMP:
		typ = int(LX.T_TIMESTAMP)
	default:
		return 0, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    "type " + tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
	p.advance()
	return typ, nil
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
