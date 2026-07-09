package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"strings"
)

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
	ob, lim, off, offFirst, fetchFirst, err := p.parseTrailingClauses()
	if err != nil {
		return nil, err
	}
	switch s := left.(type) {
	case *CompoundStmt:
		s.OrderBy = ob
		s.Limit = lim
		s.Offset = off
		s.OffsetFirst = offFirst
		s.FetchFirst = fetchFirst
	case *Select:
		s.OrderBy = ob
		s.Limit = lim
		s.Offset = off
		s.OffsetFirst = offFirst
		s.FetchFirst = fetchFirst
	}
	return left, nil
}

// parseTrailingClauses parses optional ORDER BY / LIMIT / OFFSET / FETCH FIRST.
// REQ000907: FETCH FIRST/NEXT n ROWS ONLY is parsed here and mapped to LIMIT.
func (p *Parser) parseTrailingClauses() ([]OrderItem, Expr, Expr, bool, *FetchFirst, error) {
	var orderBy []OrderItem
	if p.current.Type == LX.T_ORDER {
		p.advance()
		if err := p.expect(LX.T_BY); err != nil {
			return nil, nil, nil, false, nil, err
		}
		p.advance()
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, nil, nil, false, nil, err
			}
			desc := false
			if p.current.Type == LX.T_ASC {
				p.advance()
			} else if p.current.Type == LX.T_DESC {
				desc = true
				p.advance()
			}
			var collation string
			if p.current.Type == LX.T_COLLATE {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, nil, nil, false, nil, err
				}
				collation = p.current.Lexeme
				p.advance()
			}
			nullsOrder := int8(0)
			// Accept both T_IDENT (legacy) and T_FIRST/T_LAST keywords (REQ000907).
			if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "NULLS") {
				p.advance()
				if p.current.Type == LX.T_FIRST ||
					(p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "FIRST")) {
					nullsOrder = 1
					p.advance()
				} else if p.current.Type == LX.T_LAST ||
					(p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "LAST")) {
					nullsOrder = -1
					p.advance()
				} else {
					return nil, nil, nil, false, nil, &SyntaxError{
						Input:    p.lex.Input(),
						Line:     p.current.Line,
						Col:      p.current.Col,
						Expected: "FIRST or LAST",
						Got:      tokenName(p.current.Type),
						Lexeme:   p.current.Lexeme,
					}
				}
			}
			orderBy = append(orderBy, OrderItem{Expr: expr, Desc: desc, Collation: collation, NullsOrder: nullsOrder})
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}
	offsetFirst := false
	var limit Expr
	var offset Expr
	// Handle two forms:
	//   (1) LIMIT n OFFSET m  (standard)
	//   (2) OFFSET m LIMIT n  (reversed)
	if p.current.Type == LX.T_OFFSET {
		offsetFirst = true
		p.advance()
		o, err := p.parseExpr()
		if err != nil {
			return nil, nil, nil, false, nil, err
		}
		offset = o
	}
	if p.current.Type == LX.T_LIMIT {
		p.advance()
		l, err := p.parseExpr()
		if err != nil {
			return nil, nil, nil, false, nil, err
		}
		limit = l
	}
	// In form (1), OFFSET may follow LIMIT
	if !offsetFirst && p.current.Type == LX.T_OFFSET {
		p.advance()
		o, err := p.parseExpr()
		if err != nil {
			return nil, nil, nil, false, nil, err
		}
		offset = o
	}
	// REQ000907: FETCH FIRST/NEXT n ROWS ONLY.
	// SQL:2008 standard alternative to LIMIT.
	// Form: FETCH {FIRST|NEXT} [count] {ROW|ROWS} ONLY
	var fetchFirst *FetchFirst
	if p.current.Type == LX.T_FETCH {
		p.advance()
		// FIRST or NEXT (both equivalent semantically).
		if p.current.Type != LX.T_FIRST && p.current.Type != LX.T_NEXT {
			return nil, nil, nil, false, nil, &SyntaxError{
				Input:    p.lex.Input(),
				Line:     p.current.Line,
				Col:      p.current.Col,
				Expected: "FIRST or NEXT",
				Got:      tokenName(p.current.Type),
				Lexeme:   p.current.Lexeme,
			}
		}
		p.advance()
		// Optional count expression. If ROW/ROWS follows without
		// a count, treat it as count=1.
		var count Expr
		if p.current.Type != LX.T_ROW && p.current.Type != LX.T_ROWS {
			c, err := p.parseExpr()
			if err != nil {
				return nil, nil, nil, false, nil, err
			}
			count = c
		}
		// ROW or ROWS (both equivalent).
		if p.current.Type != LX.T_ROW && p.current.Type != LX.T_ROWS {
			return nil, nil, nil, false, nil, &SyntaxError{
				Input:    p.lex.Input(),
				Line:     p.current.Line,
				Col:      p.current.Col,
				Expected: "ROW or ROWS",
				Got:      tokenName(p.current.Type),
				Lexeme:   p.current.Lexeme,
			}
		}
		p.advance()
		// ONLY (or WITH TIES — not implemented in v1).
		if p.current.Type != LX.T_ONLY {
			return nil, nil, nil, false, nil, &SyntaxError{
				Input:    p.lex.Input(),
				Line:     p.current.Line,
				Col:      p.current.Col,
				Expected: "ONLY",
				Got:      tokenName(p.current.Type),
				Lexeme:   p.current.Lexeme,
			}
		}
		p.advance()
		fetchFirst = &FetchFirst{Count: count}
		// REQ000907: SQLite rejects mixing OFFSET with FETCH.
		// We allow it but map FETCH to LIMIT.
	}
	return orderBy, limit, offset, offsetFirst, fetchFirst, nil
}

// parseIntersectChain parses `INTERSECT` (or chain thereof) and
// returns either a plain *Select or a *CompoundStmt. REQ000383.
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

// parseSelectList parses a SELECT column list: one or more expressions,
// optionally with AS aliases. Handles * and comma-separated columns.
// REQ001002: extracted from parseOneSelect.
func (p *Parser) parseSelectList() ([]Expr, bool, error) {
	var distinct bool
	if p.current.Type == LX.T_DISTINCT {
		distinct = true
		p.advance()
	} else if p.current.Type == LX.T_ALL {
		p.advance()
	}

	var cols []Expr
	if p.current.Type == LX.T_STAR {
		cols = append(cols, &StarExpr{})
		p.advance()
		return cols, distinct, nil
	}
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, false, err
		}
		if p.current.Type == LX.T_AS {
			p.advance()
			if err := p.expectIdentOrErr(); err != nil {
				return nil, false, err
			}
			expr = &AliasedExpr{Expr: expr, Alias: p.current.Lexeme}
			p.advance()
		} else if p.expectIdent() {
			expr = &AliasedExpr{Expr: expr, Alias: p.current.Lexeme}
			p.advance()
		}
		cols = append(cols, expr)
		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}
	return cols, distinct, nil
}

// parseFromClause parses FROM clause: table references, subqueries,
// aliases, comma-joins, and explicit JOINs.
// REQ001002: extracted from parseOneSelect.
func (p *Parser) parseFromClause() (from string, fromAlias string, joins []JoinClause, subqueryFrom Stmt, indexHint *IndexHint, err error) {
	if p.current.Type != LX.T_FROM {
		return
	}
	p.advance()

	if p.current.Type == LX.T_LPAREN {
		p.advance()
		if p.current.Type == LX.T_SELECT {
			sub, err2 := p.parseSelect()
			if err2 != nil {
				err = err2
				return
			}
			if err2 := p.expect(LX.T_RPAREN); err2 != nil {
				err = err2
				return
			}
			p.advance()
			var subAlias string
			if p.current.Type == LX.T_AS || p.expectIdent() {
				if p.current.Type == LX.T_AS {
					p.advance()
				}
				if p.expectIdent() {
					subAlias = p.current.Lexeme
					p.advance()
				}
			}
			if subAlias != "" {
				from = subAlias
			} else {
				from = "$$subquery$$"
			}
			p.pendingSubquery = sub
			subqueryFrom = sub
		} else {
			p.parenTableExpr = true
			fromRef, err2 := p.parseTableRef()
			if err2 != nil {
				err = err2
				return
			}
			from = fromRef
		}
	} else {
		fromRef, err2 := p.parseTableRef()
		if err2 != nil {
			err = err2
			return
		}
		from = fromRef
	}

	if p.current.Type == LX.T_AS {
		p.advance()
		if err2 := p.expectIdentOrErr(); err2 != nil {
			err = err2
			return
		}
		fromAlias = p.current.Lexeme
		p.advance()
	} else if p.expectIdent() {
		fromAlias = p.current.Lexeme
		p.advance()
	}

	for p.current.Type == LX.T_COMMA {
		p.advance()
		rightRef, err2 := p.parseTableRef()
		if err2 != nil {
			err = err2
			return
		}
		var rightAlias string
		if p.current.Type == LX.T_AS {
			p.advance()
			if p.expectIdent() {
				rightAlias = p.current.Lexeme
				p.advance()
			}
		} else if p.expectIdent() {
			rightAlias = p.current.Lexeme
			p.advance()
		}
		p.pendingJoins = append(p.pendingJoins, rightRef)
		p.pendingJoinAliases = append(p.pendingJoinAliases, rightAlias)
	}

	indexHint = p.parseIndexHint()

	for i, right := range p.pendingJoins {
		var alias string
		if i < len(p.pendingJoinAliases) {
			alias = p.pendingJoinAliases[i]
		}
		joins = append(joins, JoinClause{Kind: "CROSS", Right: right, RightAlias: alias})
	}
	p.pendingJoins = nil
	p.pendingJoinAliases = nil

	for p.current.Type == LX.T_JOIN || p.current.Type == LX.T_LEFT ||
		p.current.Type == LX.T_RIGHT || p.current.Type == LX.T_INNER ||
		p.current.Type == LX.T_CROSS || p.current.Type == LX.T_NATURAL {
		natural := false
		if p.current.Type == LX.T_NATURAL {
			natural = true
			p.advance()
		}
		kind := "INNER"
		switch p.current.Type {
		case LX.T_LEFT:
			kind = "LEFT"
		case LX.T_RIGHT:
			kind = "RIGHT"
		case LX.T_FULL:
			kind = "FULL"
		case LX.T_CROSS:
			kind = "CROSS"
		}
		if !natural || p.current.Type != LX.T_JOIN {
			if kind == "INNER" && p.current.Type != LX.T_CROSS {
				// natural alone without LEFT/RIGHT/etc implies INNER
			}
		}
		p.advance()
		if p.current.Type == LX.T_OUTER {
			p.advance()
		}
		if p.current.Type == LX.T_JOIN {
			p.advance()
		}
		rightRef, err2 := p.parseTableRef()
		if err2 != nil {
			err = err2
			return
		}
		var rightAlias string
		if p.current.Type == LX.T_AS {
			p.advance()
			if p.expectIdent() {
				rightAlias = p.current.Lexeme
				p.advance()
			}
		} else if p.expectIdent() {
			rightAlias = p.current.Lexeme
			p.advance()
		}
		var on Expr
		var usingCols []string
		if natural {
			// NATURAL JOIN — ON/USING not allowed; planner synthesizes
			// the equi-join from common column names.
		} else if p.current.Type == LX.T_ON {
			p.advance()
			e, err2 := p.parseExpr()
			if err2 != nil {
				err = err2
				return
			}
			on = e
		} else if p.current.Type == LX.T_USING {
			// REQ001361: JOIN ... USING (col1, col2, ...)
			p.advance()
			if err2 := p.expect(LX.T_LPAREN); err2 != nil {
				err = err2
				return
			}
			p.advance()
			for {
				if err2 := p.expect(LX.T_IDENT); err2 != nil {
					err = err2
					return
				}
				usingCols = append(usingCols, strings.ToLower(p.current.Lexeme))
				p.advance()
				if p.current.Type != LX.T_COMMA {
					break
				}
				p.advance()
			}
			if err2 := p.expect(LX.T_RPAREN); err2 != nil {
				err = err2
				return
			}
			p.advance()
		}
		joins = append(joins, JoinClause{Kind: kind, Right: rightRef, RightAlias: rightAlias, On: on, Using: usingCols, Natural: natural})
	}

	if p.parenTableExpr {
		p.parenTableExpr = false
		if err2 := p.expect(LX.T_RPAREN); err2 != nil {
			err = err2
			return
		}
		p.advance()
	}
	return
}

// parseWhereGroupHaving parses WHERE, GROUP BY, and HAVING clauses.
// REQ001002: extracted from parseOneSelect.
func (p *Parser) parseWhereGroupHaving() (where Expr, groupBy []Expr, having Expr, err error) {
	if p.current.Type == LX.T_WHERE {
		p.advance()
		w, err2 := p.parseExpr()
		if err2 != nil {
			err = err2
			return
		}
		where = w
	}

	if p.current.Type == LX.T_GROUP {
		p.advance()
		if err2 := p.expect(LX.T_BY); err2 != nil {
			err = err2
			return
		}
		p.advance()
		for {
			expr, err2 := p.parseExpr()
			if err2 != nil {
				err = err2
				return
			}
			groupBy = append(groupBy, expr)
			if p.current.Type != LX.T_COMMA {
				break
			}
			p.advance()
		}
	}

	if p.current.Type == LX.T_HAVING {
		p.advance()
		h, err2 := p.parseExpr()
		if err2 != nil {
			err = err2
			return
		}
		having = h
	}
	return
}

// parseOneSelect parses a single SELECT statement (no compound
// chain). REQ000383: split out from parseSelect.
func (p *Parser) parseOneSelect() (*Select, error) {
	p.pendingSubquery = nil
	p.advance()

	cols, distinct, err := p.parseSelectList()
	if err != nil {
		return nil, err
	}

	from, fromAlias, joins, subqueryFrom, indexHint, err := p.parseFromClause()
	if err != nil {
		return nil, err
	}

	where, groupBy, having, err := p.parseWhereGroupHaving()
	if err != nil {
		return nil, err
	}

	// Trailing ORDER BY / LIMIT / OFFSET / FETCH are parsed by the caller.

	return &Select{
		Cols:         cols,
		From:         from,
		FromAlias:    fromAlias,
		Joins:        joins,
		Where:        where,
		GroupBy:      groupBy,
		Having:       having,
		Distinct:     distinct,
		SubqueryFrom: subqueryFrom,
		IndexHint:    indexHint,
	}, nil
}

func (p *Parser) parseWith() (*WithStmt, error) {
	p.advance() // consume WITH

	stmt := &WithStmt{}
	// REQ000436: WITH [RECURSIVE]
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "RECURSIVE") {
		stmt.Recursive = true
		p.advance()
	}

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

		query, err := p.parseCteBody()
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

	// Parse the main query. We call parseSelect() directly rather
	// than Parse() because Parse() calls reset()+advance() which
	// would skip the current token (SELECT) and reset parser state.
	inner, err := p.parseSelect()
	if err != nil {
		return nil, err
	}

	return &WithStmt{Recursive: stmt.Recursive, CTEs: ctes, Inner: inner}, nil
}

// parseCteBody parses a CTE body that may be a single SELECT or a
// compound SELECT (UNION/INTERSECT/EXCEPT). The parser's parseSelect
// already handles compound operators via parseIntersectChain and the
// union/except loop, so this just delegates. REQ000436.
func (p *Parser) parseCteBody() (Stmt, error) {
	if p.current.Type == LX.T_SELECT {
		return p.parseSelect()
	}
	return p.Parse()
}
