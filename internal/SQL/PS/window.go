package PS

import (
	"strings"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

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
			var collation string
			if p.current.Type == LX.T_COLLATE {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				collation = p.current.Lexeme
				p.advance()
			}
			spec.OrderBy = append(spec.OrderBy, OrderItem{Expr: e, Desc: desc, Collation: collation})
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
