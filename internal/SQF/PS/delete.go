package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

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

	// REQ000569: INDEXED BY / NOT INDEXED after table reference
	indexHint := p.parseIndexHint()

	var where Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		where = w
	}

	// REQ000475: parse optional ORDER BY / LIMIT / OFFSET
	orderBy, limit, offset, offsetFirst, err := p.parseTrailingClauses()
	if err != nil {
		return nil, err
	}

	returning, err := p.parseReturning()
	if err != nil {
		return nil, err
	}

	return &Delete{
		Table:       table,
		Where:       where,
		Returning:   returning,
		OrderBy:     orderBy,
		Limit:       limit,
		Offset:      offset,
		OffsetFirst: offsetFirst,
		IndexHint:   indexHint,
	}, nil
}
