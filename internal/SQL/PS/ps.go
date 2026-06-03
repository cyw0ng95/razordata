package PS

import (
	"errors"
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

var ErrSyntax = errors.New("ps: syntax error")

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
		return fmt.Errorf("%w: expected %v, got %v (%q) at line %d col %d",
			ErrSyntax, typ, p.current.Type, p.current.Lexeme, p.current.Line, p.current.Col)
	}
	return nil
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
	}
	return nil, fmt.Errorf("%w: unexpected token %v at line %d col %d",
		ErrSyntax, p.current.Type, p.current.Line, p.current.Col)
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

	switch p.current.Type {
	case LX.T_SELECT:
		return p.parseSelect()
	case LX.T_INSERT:
		return p.parseInsert()
	case LX.T_UPDATE:
		return p.parseUpdate()
	case LX.T_DELETE:
		return p.parseDelete()
	case LX.T_CREATE:
		return p.parseCreateTable()
	case LX.T_DROP:
		return p.parseDropTable()
	}
	return nil, fmt.Errorf("%w: unexpected token %v", ErrSyntax, p.current.Type)
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

	if err := p.expect(LX.T_FROM); err != nil {
		return nil, err
	}
	p.advance()

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	from := p.current.Lexeme
	p.advance()

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
