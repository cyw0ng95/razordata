package PS

import (
	"encoding/hex"
	"fmt"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"strings"
)

func (p *Parser) parsePrimary() (Expr, error) {
	switch p.current.Type {
	case LX.T_INT:
		loc := p.loc()
		val := p.current.LitInt
		p.advance()
		return &NumberLiteral{Loc: loc, Val: val}, nil
	case LX.T_FLOAT:
		loc := p.loc()
		val := p.current.LitFloat
		p.advance()
		return &FloatLiteral{Loc: loc, Val: val}, nil
	case LX.T_STRING:
		loc := p.loc()
		val := p.current.LitStr
		p.advance()
		return &StringLiteral{Loc: loc, Val: val}, nil
	case LX.T_NULL:
		loc := p.loc()
		p.advance()
		return &NullLiteral{Loc: loc}, nil
	case LX.T_TRUE:
		loc := p.loc()
		p.advance()
		return &BoolLiteral{Loc: loc, Val: true}, nil
	case LX.T_FALSE:
		loc := p.loc()
		p.advance()
		return &BoolLiteral{Loc: loc, Val: false}, nil
	case LX.T_BIND:
		loc := p.loc()
		idx := p.paramIndex
		p.paramIndex++
		p.advance()
		return &Param{Loc: loc, Index: idx}, nil
	case LX.T_STAR:
		loc := p.loc()
		p.advance()
		return &StarExpr{Loc: loc}, nil
case LX.T_IDENT, LX.T_EXCLUDED:
		loc := p.loc()
		// REQ002108: lexer already lowercases T_IDENT Lexeme.
		name := p.current.Lexeme
		// T_EXCLUDED is a keyword token, not T_IDENT — the lexer
		// preserves its original case, so fold it via the shared
		// identifier normalizer here. REQ002306.
		if p.current.Type == LX.T_EXCLUDED {
			name = normalizeIdent(name)
		}
		p.advance()
		// REQ000808: hex string literal X'...'
		if strings.EqualFold(name, "X") && p.current.Type == LX.T_STRING {
			hexStr := p.current.Lexeme
			if len(hexStr)%2 != 0 {
				return nil, &SyntaxError{
					Input:  p.lex.Input(),
					Line:   p.current.Line,
					Col:    p.current.Col,
					Got:    "hex string must have even number of hex digits",
					Lexeme: hexStr,
				}
			}
			decoded, err := hex.DecodeString(hexStr)
			if err != nil {
				return nil, &SyntaxError{
					Input:  p.lex.Input(),
					Line:   p.current.Line,
					Col:    p.current.Col,
					Got:    "invalid hex string: " + err.Error(),
					Lexeme: hexStr,
				}
			}
			p.advance()
			return &StringLiteral{Loc: loc, Val: string(decoded)}, nil
		}
		if p.current.Type == LX.T_DOT {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			mid := p.current.Lexeme
			p.advance()
			// REQ000750: check for three-part name (db.table.col)
			if p.current.Type == LX.T_DOT {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				col := p.current.Lexeme
				p.advance()
				return &QualifiedName{Loc: loc, Database: name, Table: mid, Name: col, SlotIdx: -1}, nil
			}
			return &QualifiedName{Loc: loc, Table: name, Name: mid, SlotIdx: -1}, nil
		}
		if p.current.Type == LX.T_LPAREN {
			return p.parseFunctionCall(name)
		}
		return &Ident{Loc: loc, Name: name, SlotIdx: -1}, nil
	case LX.T_GLOB:
		// REQ000729: GLOB can be used as a function call GLOB(pattern, string)
		// or as a binary operator expr GLOB pattern.
		loc := p.loc()
		name := p.current.Lexeme
		p.advance()
		if p.current.Type == LX.T_LPAREN {
			return p.parseFunctionCall(name)
		}
		// Not followed by '(' — treat as identifier for binary operator
		// parsing in parsePostfix.
		return &Ident{Loc: loc, Name: name, SlotIdx: -1}, nil
	case LX.T_RAISE:
		// RAISE(ABORT, 'message') or RAISE(IGNORE) (REQ000560)
		loc := p.loc()
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
		rf := &RaiseFunc{Loc: loc, Action: action}
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
		// REQ002108: lexer provides canonical uppercase Lexeme from tokenTypeNames.
		name := p.current.Lexeme
		p.advance()
		agg, args, err := p.parseAggregateFunc(name, false)
		if err != nil {
			return nil, err
		}
		if p.current.Type == LX.T_OVER {
			return p.parseWindowFunc(name, args)
		}
		return agg, nil
	case LX.T_MIN, LX.T_MAX:
		// REQ002108: lexer provides canonical uppercase Lexeme from tokenTypeNames.
		name := p.current.Lexeme
		p.advance()
		agg, args, err := p.parseAggregateFunc(name, true)
		if err != nil {
			return nil, err
		}
		if p.current.Type == LX.T_OVER {
			return p.parseWindowFunc(name, args)
		}
		return agg, nil
	case LX.T_ROW_NUMBER, LX.T_RANK, LX.T_DENSE_RANK, LX.T_LAG, LX.T_LEAD, LX.T_FIRST_VALUE, LX.T_LAST_VALUE, LX.T_NTH_VALUE:
		return p.parseWindowBuiltin()
	case LX.T_LPAREN:
		loc := p.loc()
		p.advance()
		if p.current.Type == LX.T_SELECT {
			// REQ000962: save and restore pendingSubquery around
			// nested subquery parsing. The inner subquery may set
			// pendingSubquery for its own FROM (SELECT ...) clause,
			// which would leak into the outer SELECT's SubqueryFrom
			// field and cause wrong results (e.g. 2 rows instead of 1).
			savedPending := p.pendingSubquery
			p.pendingSubquery = nil
			sel, err := p.parseSelect()
			p.pendingSubquery = savedPending
			if err != nil {
				return nil, err
			}
			if err := p.expect(LX.T_RPAREN); err != nil {
				return nil, err
			}
			p.advance()
			return &SubqueryExpr{Loc: loc, Subquery: sel}, nil
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

// parseAggregateFunc parses an aggregate function call: name([DISTINCT|ALL] arg, ...).
// The '(' has already been consumed.
// Returns (expr, args, err) where expr is *AggregateFunc or *FunctionCall.
// args is the full argument list for use by parseWindowFunc.
func (p *Parser) parseAggregateFunc(name string, allowMultiArgs bool) (Expr, []Expr, error) {
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, nil, err
	}
	loc := p.loc()
	p.advance()

	distinct := false
	if p.current.Type == LX.T_DISTINCT {
		distinct = true
		p.advance()
	} else if p.current.Type == LX.T_ALL {
		p.advance() // ALL is a no-op (default behavior)
	}

	var args []Expr
	if p.current.Type == LX.T_STAR {
		args = append(args, &StarExpr{Loc: loc})
		p.advance()
	} else if p.current.Type != LX.T_RPAREN {
		a, err := p.parseExpr()
		if err != nil {
			return nil, nil, err
		}
		args = append(args, a)
		if allowMultiArgs {
			for p.current.Type == LX.T_COMMA {
				p.advance()
				a, err := p.parseExpr()
				if err != nil {
					return nil, nil, err
				}
				args = append(args, a)
			}
		}
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, nil, err
	}
	p.advance()

	// Multi-arg MIN/MAX → scalar function call (e.g. max(a, b, c))
	if len(args) > 1 {
		return &FunctionCall{Loc: loc, Name: name, Args: args}, args, nil
	}

	agg := &AggregateFunc{Loc: loc, Name: name, Arg: args[0], Distinct: distinct}
	if p.current.Type == LX.T_FILTER {
		filter, err := p.parseFilterClause()
		if err != nil {
			return nil, nil, err
		}
		agg.Filter = filter
	}
	return agg, args, nil
}

// parseFunctionCall parses a function call: name(arg1, arg2, ...).
// The '(' has already been consumed.
func (p *Parser) parseFunctionCall(name string) (Expr, error) {
	loc := p.loc()
	p.advance() // consume '('
	// REQ000761: normalize function name to uppercase once,
	// eliminating strings.ToUpper in evalFunction.
	name = strings.ToUpper(name)
	// REQ000437: aggregate names like GROUP_CONCAT accept
	// the DISTINCT keyword before their argument.
	// REQ000805: ALL is also accepted as a no-op.
	var distinct bool
	if isAggregateName(name) && p.current.Type == LX.T_DISTINCT {
		distinct = true
		p.advance()
	} else if isAggregateName(name) && p.current.Type == LX.T_ALL {
		p.advance() // ALL is a no-op (default behavior)
	}
	var args []Expr
	if p.current.Type != LX.T_RPAREN {
		if distinct && p.current.Type == LX.T_STAR {
			return nil, &SyntaxError{
				Input:    p.lex.Input(),
				Line:     p.current.Line,
				Col:      p.current.Col,
				Expected: "expression after DISTINCT",
				Got:      "*",
				Lexeme:   p.current.Lexeme,
			}
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
			return &FunctionCall{Loc: loc, Name: name, Args: args}, nil
		}
		var arg Expr
		if len(args) > 0 {
			arg = args[0]
		}
		// REQ000523: GROUP_CONCAT with optional separator
		var sep Expr
		if name == "GROUP_CONCAT" && len(args) > 1 {
			sep = args[1]
		}
		agg := &AggregateFunc{Loc: loc, Name: name, Arg: arg, Distinct: distinct, Separator: sep}
		if p.current.Type == LX.T_FILTER {
			filter, err := p.parseFilterClause()
			if err != nil {
				return nil, err
			}
			agg.Filter = filter
		}
		return agg, nil
	}
	return &FunctionCall{Loc: loc, Name: name, Args: args}, nil
}

// parseFilterClause parses FILTER (WHERE expr). REQ000747.
// The caller has already consumed the closing paren of the function call,
// and p.current is the FILTER token.
func (p *Parser) parseFilterClause() (Expr, error) {
	p.advance() // consume FILTER
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance() // consume '('
	if p.current.Type != LX.T_WHERE {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "WHERE",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	p.advance() // consume WHERE
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance() // consume ')'
	return expr, nil
}

func (p *Parser) parseUnary() (Expr, error) {
	if p.current.Type == LX.T_NOT {
		loc := p.loc()
		p.advance()
		// REQ000806: NOT binds at precedence 3 (one above AND at 2,
		// below comparison ops at 6). This ensures NOT parses its
		// operand through BETWEEN (cmpPrec=6) but stops before
		// AND/OR, so `NOT x BETWEEN a AND b AND c` parses as
		// `(NOT (x BETWEEN a AND b)) AND c`, not `NOT ((x BETWEEN a AND b) AND c)`.
		// REQ001711: previously used minPrec=1 which allowed AND to
		// be consumed as part of the NOT operand, causing wrong
		// CASE WHEN evaluation in `NOT (-36) BETWEEN 21 AND 96 AND ...`.
		operand, err := p.parseBinary(3)
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Loc: loc, Op: LX.T_NOT, Operand: operand}, nil
	}
	if p.current.Type == LX.T_MINUS || p.current.Type == LX.T_PLUS || p.current.Type == LX.T_BITNOT {
		loc := p.loc()
		op := p.current.Type
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Loc: loc, Op: op, Operand: operand}, nil
	}
	return p.parsePostfix()
}

// parsePostfix is currently a pass-through to parsePrimary.
//
// REQ001692: IN / NOT IN / GLOB / NOT GLOB / NOT LIKE used to be
// dispatched here, which bound them at unary precedence — tighter
// than unary minus. That made `-a NOT IN (b)` parse as
// `-(NOT(a IN (b)))` instead of `NOT((-a) IN (b))`, and the
// `-(BoolValue)` evaluated to NULL, filtering out every row.
// They now dispatch from parseBinary at comparison precedence (6),
// alongside BETWEEN/NOT BETWEEN. Plain GLOB and plain LIKE flow
// through the normal binary-operator path (isBinaryOp/precedence).
func (p *Parser) parsePostfix() (Expr, error) {
	return p.parsePrimary()
}

// parseNotGlob parses `expr NOT GLOB pattern` as NOT(expr GLOB pattern).
func (p *Parser) parseNotGlob(expr Expr) (Expr, error) {
	loc := p.loc()
	p.advance() // consume GLOB
	right, err := p.parseBinary(7)
	if err != nil {
		return nil, err
	}
	glob := &BinaryExpr{Loc: loc, Op: LX.T_GLOB, Left: expr, Right: right}
	return &UnaryExpr{Loc: loc, Op: LX.T_NOT, Operand: glob}, nil
}

// REQ000380: `NOT LIKE` — parse x NOT LIKE y as NOT(x LIKE y).
func (p *Parser) parseNotLike(expr Expr) (Expr, error) {
	loc := p.loc()
	p.advance()
	right, err := p.parseBinary(7)
	if err != nil {
		return nil, err
	}
	like := &BinaryExpr{Loc: loc, Op: LX.T_LIKE, Left: expr, Right: right}
	// REQ000567: LIKE ... ESCAPE expr
	if p.current.Type == LX.T_ESCAPE {
		p.advance()
		escape, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		like.Escape = escape
	}
	return &UnaryExpr{Loc: loc, Op: LX.T_NOT, Operand: like}, nil
}

// REQ000381: `NOT IN` — parse x NOT IN (...) as NOT(x IN (...)).
// REQ001692: now dispatched from parseBinary at precedence 6; the
// caller has already consumed T_NOT, so we only consume T_IN and the
// ( ... ) here.
func (p *Parser) parseNotIn(expr Expr) (Expr, error) {
	loc := p.loc()
	p.advance()
	in, err := p.parseInBody(expr)
	if err != nil {
		return nil, err
	}
	return &UnaryExpr{Loc: loc, Op: LX.T_NOT, Operand: in}, nil
}

// REQ000434: `NOT BETWEEN` — parse x NOT BETWEEN low AND high as NOT(x BETWEEN low AND high).
func (p *Parser) parseNotBetween(expr Expr) (Expr, error) {
	loc := p.loc()
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
	between := &BetweenExpr{Loc: loc, Expr: expr, Low: low, High: high}
	return &UnaryExpr{Loc: loc, Op: LX.T_NOT, Operand: between}, nil
}

func (p *Parser) parseBetween(expr Expr) (Expr, error) {
	loc := p.loc()
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
	return &BetweenExpr{Loc: loc, Expr: expr, Low: low, High: high}, nil
}

func (p *Parser) parseIn(expr Expr) (Expr, error) {
	p.advance()
	return p.parseInBody(expr)
}

// parseInBody parses the (...) part of an IN expression. Caller
// must have already advanced past the T_IN token. Used by
// parseIn and parseNotIn (REQ000381).
func (p *Parser) parseInBody(expr Expr) (Expr, error) {
	// REQ000718: SQLite allows `expr IN tableName` as shorthand for
	// `expr IN (SELECT * FROM tableName)`. If the next token is an
	// identifier (not `(`), synthesize the subquery form. Use StarExpr
	// (not Ident{"*"}) so the planner expands it against the table
	// schema just like a hand-written subquery.
	if p.current.Type == LX.T_IDENT {
		loc := p.loc()
		name := p.current.Lexeme
		p.advance()
		sel := &Select{
			Loc:  loc,
			Cols: []Expr{&StarExpr{Loc: loc}},
			From: name,
		}
		return &InExpr{Loc: loc, Expr: expr, Subquery: sel}, nil
	}
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	loc := p.loc()
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
		return &InExpr{Loc: loc, Expr: expr, Subquery: sel}, nil
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
	return &InExpr{Loc: loc, Expr: expr, List: items}, nil
}

func (p *Parser) parseBinary(minPrec int) (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for {
		// Comparison-level operators with multi-token or NOT-split
		// syntax (BETWEEN, IN, NOT BETWEEN/IN/GLOB/LIKE) bind at
		// precedence 6 — the same as =, <, LIKE, GLOB, IS.
		//
		// REQ001692: dispatching these here (instead of from
		// parsePostfix at the unary level) ensures the operator
		// attaches to the full arithmetic LHS. `-a NOT IN (b)`
		// parses as `NOT((-a) IN (b))`, not `-(NOT(a IN (b)))`.
		// Plain GLOB and plain LIKE are plain binary ops and flow
		// through isBinaryOp/precedence below.
		const cmpPrec = 6
		if cmpPrec >= minPrec {
			switch p.current.Type {
			case LX.T_BETWEEN:
				left, err = p.parseBetween(left)
				if err != nil {
					return nil, err
				}
				continue
			case LX.T_IN:
				left, err = p.parseIn(left)
				if err != nil {
					return nil, err
				}
				continue
			case LX.T_NOT:
				switch p.lex.Peek().Type {
				case LX.T_BETWEEN:
					p.advance()
					left, err = p.parseNotBetween(left)
					if err != nil {
						return nil, err
					}
					continue
				case LX.T_IN:
					p.advance()
					left, err = p.parseNotIn(left)
					if err != nil {
						return nil, err
					}
					continue
				case LX.T_GLOB:
					p.advance()
					left, err = p.parseNotGlob(left)
					if err != nil {
						return nil, err
					}
					continue
				case LX.T_LIKE:
					p.advance()
					left, err = p.parseNotLike(left)
					if err != nil {
						return nil, err
					}
					continue
				}
			}
		}

		if !isBinaryOp(p.current.Type) || precedence(p.current.Type) < minPrec {
			break
		}
		op := p.current.Type
		loc := p.loc()
		p.advance()
		// REQ000833: `IS NOT NULL` — when IS is followed by NOT, handle
		// it specially so the NOT doesn't consume subsequent operators
		// (e.g., AND a > b) at NOT's low precedence (1).
		if op == LX.T_IS && p.current.Type == LX.T_NOT && p.lex.Peek().Type == LX.T_NULL {
			p.advance() // consume NOT
			p.advance() // consume NULL
			notNull := &UnaryExpr{Loc: loc, Op: LX.T_NOT, Operand: &NullLiteral{Loc: loc}}
			left = &BinaryExpr{Loc: loc, Op: op, Left: left, Right: notNull}
			continue
		}
		nextMinPrec := precedence(op) + 1
		right, err := p.parseBinary(nextMinPrec)
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Loc: loc, Op: op, Left: left, Right: right}
		// REQ000567: LIKE ... ESCAPE expr
		if op == LX.T_LIKE && p.current.Type == LX.T_ESCAPE {
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
	loc := p.loc()
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
	return &CastExpr{Loc: loc, Expr: expr, Type: typ}, nil
}

func (p *Parser) parseExists() (Expr, error) {
	loc := p.loc()
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
	return &ExistsExpr{Loc: loc, Subquery: sel}, nil
}

// parseInterval parses INTERVAL 'value' UNIT. REQ000288: validates unit.
func (p *Parser) parseInterval() (Expr, error) {
	loc := p.loc()
	p.advance() // consume INTERVAL
	if err := p.expect(LX.T_STRING); err != nil {
		return nil, err
	}
	// REQ001143: use typed LitStr accessor.
	val := p.current.LitStr
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
		return &IntervalLiteral{Loc: loc, Value: val, Unit: unit}, nil
	}
	return &IntervalLiteral{Loc: loc, Value: val, Unit: "DAY"}, nil
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
		info.Type = p.current.Type
		p.advance()
	case LX.T_FLOAT_KW:
		info.Type = LX.T_FLOAT_KW
		p.advance()
	case LX.T_BOOL:
		info.Type = LX.T_BOOL
		p.advance()
	case LX.T_TEXT:
		info.Type = LX.T_TEXT
		p.advance()
	case LX.T_BLOB:
		info.Type = LX.T_BLOB
		p.advance()
	case LX.T_TIMESTAMP:
		info.Type = LX.T_TIMESTAMP
		p.advance()
	case LX.T_VARCHAR:
		info.Type = LX.T_VARCHAR
		p.advance()
		if err := p.parseTypeSize(&info.Size); err != nil {
			return nil, err
		}
	case LX.T_NUMERIC:
		info.Type = LX.T_NUMERIC
		p.advance()
		if err := p.parseTypePrecision(&info.Precision, &info.Scale); err != nil {
			return nil, err
		}
		info.Size = info.Precision // use Size for ColDef compatibility
	case LX.T_DECIMAL:
		info.Type = LX.T_DECIMAL
		p.advance()
		if err := p.parseTypePrecision(&info.Precision, &info.Scale); err != nil {
			return nil, err
		}
		info.Size = info.Precision // use Size for ColDef compatibility
	case LX.T_DATE:
		info.Type = LX.T_DATE
		p.advance()
	case LX.T_TIME:
		info.Type = LX.T_TIME
		p.advance()
	case LX.T_JSON:
		info.Type = LX.T_JSON
		p.advance()
	case LX.T_IDENT:
		switch strings.ToUpper(p.current.Lexeme) {
		case "SIGNED", "UNSIGNED":
			info.Type = LX.T_INT_KW
			p.advance()
		default:
			return nil, &SyntaxError{
				Input:  p.lex.Input(),
				Line:   p.current.Line,
				Col:    p.current.Col,
				Got:    "type " + p.current.Lexeme,
				Lexeme: p.current.Lexeme,
			}
		}
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
	if _, err := fmt.Sscanf(p.current.Lexeme, "%d", &n); err != nil {
		return &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    "integer",
			Lexeme: p.current.Lexeme,
		}
	}
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
	if _, err := fmt.Sscanf(p.current.Lexeme, "%d", &n); err != nil {
		return &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    "integer",
			Lexeme: p.current.Lexeme,
		}
	}
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
		if _, err := fmt.Sscanf(p.current.Lexeme, "%d", &m); err != nil {
			return &SyntaxError{
				Input:  p.lex.Input(),
				Line:   p.current.Line,
				Col:    p.current.Col,
				Got:    "integer",
				Lexeme: p.current.Lexeme,
			}
		}
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
	loc := p.loc()
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
		wLoc := p.loc()
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
		whenList = append(whenList, WhenClause{Loc: wLoc, Cond: cond, Then: then})
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

	return &CaseExpr{Loc: loc, Expr: expr, WhenList: whenList, Else: elseExpr}, nil
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
	loc := p.loc()
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

	return &FunctionCall{Loc: loc, Name: "COALESCE", Args: args}, nil
}

// parseNullif parses: NULLIF(expr1, expr2)
// Returns NULL if expr1 = expr2, otherwise expr1.
func (p *Parser) parseNullif() (Expr, error) {
	loc := p.loc()
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

	return &FunctionCall{Loc: loc, Name: "NULLIF", Args: []Expr{arg1, arg2}}, nil
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
