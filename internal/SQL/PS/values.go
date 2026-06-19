package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

func (p *Parser) parseValues() (*ValuesStmt, error) {
	p.advance()
	var rows [][]Expr
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
		rows = append(rows, row)
		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}
	return &ValuesStmt{Rows: rows}, nil
}
