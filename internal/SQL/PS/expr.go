package PS

import (
	"fmt"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"strings"
)

func (p *Parser) parsePrimary() (Expr, error) {
	switch p.current.Type {
	case LX.T_INT:
		val, ok := p.current.Literal.(int64)
		if !ok {
			return nil, &SyntaxError{Msg: fmt.Sprintf("expected int64 literal, got %T", p.current.Literal)}
		}
		p.advance()
		return &NumberLiteral{Val: val}, nil
	case LX.T_FLOAT:
		val := p.current.Lexeme
		p.advance()
		return &FloatLiteral{Val: parseFloat(val)}, nil
	case LX.T_STRING:
		val, ok := p.current.Literal.(string)
		if !ok {
			return nil, &SyntaxError{Msg: fmt.Sprintf("expected string literal, got %T", p.current.Literal)}
		}
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
			// REQ000437: aggregate names like GROUP_CONCAT accept
			// the DISTINCT keyword before their argument.
			// Handle it here (before the arg loop) so the loop
			// does not try to parse DISTINCT as an expression.
			var distinct bool
			if isAggregateName(name) && p.current.Type == LX.T_DISTINCT {
				distinct = true
				p.advance()
			}
			var args []Expr
			if p.current.Type != LX.T_RPAREN {
				if distinct && p.current.Type == LX.T_STAR {
					return nil, fmt.Errorf("ps: syntax error: DISTINCT not allowed with COUNT(*)")
				}
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
				// MIN/MAX with multiple args → scalar function
				if len(args) > 1 && isMinMaxName(name) {
					return &FunctionCall{Name: name, Args: args}, nil
				}
				var arg Expr
				if len(args) > 0 {
					arg = args[0]
				}
				// REQ000523: GROUP_CONCAT with optional separator
				// GROUP_CONCAT(col, sep) stores the second arg as
				// the separator expression.
				var sep Expr
				if name == "GROUP_CONCAT" && len(args) > 1 {
					sep = args[1]
				}
				return &AggregateFunc{Name: name, Arg: arg, Distinct: distinct, Separator: sep}, nil
			}
			return &FunctionCall{Name: name, Args: args}, nil
		}
		return &Ident{Name: name}, nil
	case LX.T_RAISE:
		// RAISE(ABORT, 'message') or RAISE(IGNORE) (REQ000560)
		p.advance()
		if err := p.expect(LX.T_LPAREN); err != nil {
			return nil, err
		}
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		action := p.current.Lexeme
		p.advance()
		rf := &RaiseFunc{Action: action}
		if p.current.Type == LX.T_COMMA {
			p.advance()
			msg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			rf.Message = msg
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
		return rf, nil
	case LX.T_COUNT, LX.T_SUM, LX.T_AVG:
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
	case LX.T_MIN, LX.T_MAX:
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
		var args []Expr
		if p.current.Type == LX.T_STAR {
			args = append(args, &StarExpr{})
			p.advance()
		} else if p.current.Type != LX.T_RPAREN {
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
		// MIN/MAX with multiple args → scalar function (e.g. max(a, b, c))
		if len(args) > 1 {
			return &FunctionCall{Name: name, Args: args}, nil
		}
		// Single arg → aggregate function
		agg := &AggregateFunc{Name: name, Arg: args[0], Distinct: distinct}
		if p.current.Type == LX.T_OVER {
			return p.parseWindowFunc(name, args)
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
	// REQ000567: LIKE ... ESCAPE expr
	if p.current.Type == LX.T_ESCAPE {
		p.advance()
		escape, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		like.Escape = escape
	}
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
		// REQ000567: LIKE ... ESCAPE expr
		if op == int(LX.T_LIKE) && p.current.Type == LX.T_ESCAPE {
			p.advance()
			escape, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			left.(*BinaryExpr).Escape = escape
		}
	}

	return left, nil
}

func (p *Parser) parseExpr() (Expr, error) {
	return p.parseBinary(1)
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
