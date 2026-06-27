package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func (p *Parser) parseUpdate() (*Update, error) {
	p.advance()

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()

	// REQ000569: INDEXED BY / NOT INDEXED after table reference
	indexHint := p.parseIndexHint()

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
	var from, fromAlias string

	// REQ000740: optional FROM clause (UPDATE ... FROM) — parsed before
	// WHERE since SQLite syntax is: UPDATE t SET c = e FROM t2 WHERE cond
	if p.current.Type == LX.T_FROM {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		from = p.current.Lexeme
		p.advance()
		// Optional AS alias
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
	}

	// Parse WHERE after FROM (if present)
	if p.current.Type == LX.T_WHERE {
		p.advance()
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		where = w
	}

	// REQ000558: parse optional ORDER BY / LIMIT / OFFSET / FETCH FIRST
	orderBy, limit, offset, offsetFirst, _, err := p.parseTrailingClauses()
	if err != nil {
		return nil, err
	}

	returning, err := p.parseReturning()
	if err != nil {
		return nil, err
	}

	return &Update{Table: table, Set: set, Where: where, From: from, FromAlias: fromAlias, Returning: returning,
		OrderBy: orderBy, Limit: limit, Offset: offset, OffsetFirst: offsetFirst, IndexHint: indexHint}, nil
}

// REQ000559: parseBegin parses BEGIN [DEFERRED|IMMEDIATE|EXCLUSIVE]
// [TRANSACTION].
