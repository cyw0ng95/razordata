package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
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
	ob, lim, off, offFirst, err := p.parseTrailingClauses()
	if err != nil {
		return nil, err
	}
	switch s := left.(type) {
	case *CompoundStmt:
		s.OrderBy = ob
		s.Limit = lim
		s.Offset = off
		s.OffsetFirst = offFirst
	case *Select:
		s.OrderBy = ob
		s.Limit = lim
		s.Offset = off
		s.OffsetFirst = offFirst
	}
	return left, nil
}

// parseTrailingClauses parses optional ORDER BY / LIMIT / OFFSET.
// REQ000383. Returns offsetFirst=true when OFFSET appears before
// LIMIT in the SQL (REQ000521).
func (p *Parser) parseTrailingClauses() ([]OrderItem, Expr, Expr, bool, error) {
	var orderBy []OrderItem
	if p.current.Type == LX.T_ORDER {
		p.advance()
		if err := p.expect(LX.T_BY); err != nil {
			return nil, nil, nil, false, err
		}
		p.advance()
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, nil, nil, false, err
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
					return nil, nil, nil, false, err
				}
				collation = p.current.Lexeme
				p.advance()
			}
			nullsOrder := int8(0)
			if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "NULLS") {
				p.advance()
				if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "FIRST") {
					nullsOrder = 1
					p.advance()
				} else if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "LAST") {
					nullsOrder = -1
					p.advance()
				} else {
					return nil, nil, nil, false, &SyntaxError{
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
			return nil, nil, nil, false, err
		}
		offset = o
	}
	if p.current.Type == LX.T_LIMIT {
		p.advance()
		l, err := p.parseExpr()
		if err != nil {
			return nil, nil, nil, false, err
		}
		limit = l
	}
	// In form (1), OFFSET may follow LIMIT
	if !offsetFirst && p.current.Type == LX.T_OFFSET {
		p.advance()
		o, err := p.parseExpr()
		if err != nil {
			return nil, nil, nil, false, err
		}
		offset = o
	}
	return orderBy, limit, offset, offsetFirst, nil
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

// parseOneSelect parses a single SELECT statement (no compound
// chain). REQ000383: split out from parseSelect.
func (p *Parser) parseOneSelect() (*Select, error) {
	p.pendingSubquery = nil
	p.advance()

	var distinct bool
	if p.current.Type == LX.T_DISTINCT {
		distinct = true
		p.advance()
	} else if p.current.Type == LX.T_ALL {
		// REQ000715: SELECT ALL is a synonym for plain SELECT
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
			} else if p.current.Type == LX.T_IDENT {
				// REQ000717: implicit alias without AS keyword
				// e.g., SELECT - 87 col0, SELECT col1 * 3 alias
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
		// REQ000436 + REQ000084: support subqueries in FROM.
		// `FROM (SELECT ...)` is a subquery source; the SELECT is
		// parsed inline (not delegated to Parse which would reset
		// parser state) and the result is stored in SubqueryFrom.
		if p.current.Type == LX.T_LPAREN {
			p.advance()
			if p.current.Type == LX.T_SELECT {
				sub, err := p.parseSelect()
				if err != nil {
					return nil, err
				}
				if err := p.expect(LX.T_RPAREN); err != nil {
					return nil, err
				}
				p.advance()
				var subAlias string
				if p.current.Type == LX.T_AS || p.current.Type == LX.T_IDENT {
					if p.current.Type == LX.T_AS {
						p.advance()
					}
					if p.current.Type == LX.T_IDENT {
						subAlias = p.current.Lexeme
						p.advance()
					}
				}
				if subAlias != "" {
					from = subAlias
				} else {
					from = "$$subquery$$"
				}
				// Stash for later: the Select we're building will
				// have SubqueryFrom set after we return from this
				// function. We store it in a package-level slot
				// since we don't have a Select pointer yet.
				p.pendingSubquery = sub
			} else {
				// REQ000834: parenthesized table expression
				// (e.g., `FROM (tab0 AS cor0 CROSS JOIN tab0 cor1)`).
				// Parse the table name; alias, comma-joins, and
				// explicit joins are handled by the common code
				// that follows after this if/else block.
				p.parenTableExpr = true
				fromRef, err := p.parseTableRef()
				if err != nil {
					return nil, err
				}
				from = fromRef
			}
		} else {
			fromRef, err := p.parseTableRef()
			if err != nil {
				return nil, err
			}
			from = fromRef
		}
	}
	// REQ000705: parse alias for the first table BEFORE the
	// comma-join loop so that `FROM t a, t b` correctly
	// consumes `a` as the alias before seeing the comma.
	var fromAlias string
	if p.current.Type == LX.T_AS {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		fromAlias = p.current.Lexeme
		p.advance()
	} else if p.current.Type == LX.T_IDENT {
		fromAlias = p.current.Lexeme
		p.advance()
	}
	// REQ000368: implicit comma-join. `FROM a, b, c` is parsed
	// as `FROM a CROSS JOIN b CROSS JOIN c`. The first table
	// stays as `from`; each subsequent comma-separated identifier
	// becomes a CROSS join entry.
	for p.current.Type == LX.T_COMMA {
		p.advance()
		rightRef, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}
		rightName := rightRef
		// REQ000705: handle implicit alias for comma-separated tables
		var rightAlias string
		if p.current.Type == LX.T_AS {
			p.advance()
			if p.current.Type == LX.T_IDENT {
				rightAlias = p.current.Lexeme
				p.advance()
			}
		} else if p.current.Type == LX.T_IDENT {
			rightAlias = p.current.Lexeme
			p.advance()
		}
		p.pendingJoins = append(p.pendingJoins, rightName)
		if rightAlias != "" {
			p.pendingJoinAliases = append(p.pendingJoinAliases, rightAlias)
		} else {
			p.pendingJoinAliases = append(p.pendingJoinAliases, "")
		}
	}
	// REQ000529: INDEXED BY / NOT INDEXED after table reference
	indexHint := p.parseIndexHint()

	// REQ000368: promote any pending comma-separated tables
	// (collected above) into CROSS joins.
	var joins []JoinClause
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
		rightRef, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}
		right := rightRef
		// REQ000706: parse alias for JOIN table (implicit or explicit)
		var rightAlias string
		if p.current.Type == LX.T_AS {
			p.advance()
			if p.current.Type == LX.T_IDENT {
				rightAlias = p.current.Lexeme
				p.advance()
			}
		} else if p.current.Type == LX.T_IDENT {
			// Only treat as alias if not followed by ( (function call)
			// and not ON keyword
			rightAlias = p.current.Lexeme
			p.advance()
		}
		var on Expr
		if p.current.Type == LX.T_ON {
			p.advance()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			on = e
		}
		joins = append(joins, JoinClause{Kind: kind, Right: right, RightAlias: rightAlias, On: on})
	}

	// REQ000834: close parenthesized table expression
	if p.parenTableExpr {
		p.parenTableExpr = false
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
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
		Cols:         cols,
		From:         from,
		FromAlias:    fromAlias,
		Joins:        joins,
		Where:        where,
		GroupBy:      groupBy,
		Having:       having,
		Distinct:     distinct,
		SubqueryFrom: p.pendingSubquery,
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
