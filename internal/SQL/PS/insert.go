package PS

import (
	"fmt"
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"strings"
)

func (p *Parser) parseInsert() (*Insert, error) {
	p.advance() // consume INSERT

	var action ConflictAction
	if p.current.Type == LX.T_OR {
		p.advance() // consume OR
		// INSERT OR ROLLBACK/ABORT/FAIL/IGNORE/REPLACE
		// These are not hard keywords; compare lexeme strings.
		switch strings.ToUpper(p.current.Lexeme) {
		case "ROLLBACK":
			action = ConflictActionRollback
		case "ABORT":
			action = ConflictActionAbort
		case "FAIL":
			action = ConflictActionFail
		case "IGNORE":
			action = ConflictActionIgnore
		case "REPLACE":
			action = ConflictActionReplace
		default:
			return nil, fmt.Errorf("expected ROLLBACK/ABORT/FAIL/IGNORE/REPLACE after INSERT OR, got %s", p.current.Lexeme)
		}
		p.advance() // consume action keyword
	}

	if err := p.expect(LX.T_INTO); err != nil {
		return nil, err
	}
	p.advance() // consume INTO

	return p.parseInsertTail(action)
}

// parseReplace parses REPLACE INTO ..., equivalent to INSERT OR REPLACE INTO ...
func (p *Parser) parseReplace() (*Insert, error) {
	p.advance() // consume REPLACE, p.current should be INTO
	if err := p.expect(LX.T_INTO); err != nil {
		return nil, err
	}
	p.advance() // consume INTO
	return p.parseInsertTail(ConflictActionReplace)
}

// parseInsertTail parses the common body of INSERT and REPLACE after INTO is consumed.
func (p *Parser) parseInsertTail(action ConflictAction) (*Insert, error) {

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

	// REQ000563: INSERT INTO t DEFAULT VALUES
	if p.current.Type == LX.T_DEFAULT {
		p.advance()
		if err := p.expect(LX.T_VALUES); err != nil {
			return nil, err
		}
		p.advance()
		returning, err := p.parseReturning()
		if err != nil {
			return nil, err
		}
		return &Insert{Table: table, Cols: cols, DefaultValues: true, Returning: returning, ConflictAction: action}, nil
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

	returning, err := p.parseReturning()
	if err != nil {
		return nil, err
	}

	// Parse ON CONFLICT clause if present
	var onConflict *OnConflict
	if p.current.Type == LX.T_ON {
		// Peek to check if this is ON CONFLICT (not ON for JOIN)
		next := p.lex.Peek()
		if next.Type == LX.T_CONFLICT {
			onConflict, err = p.parseOnConflict()
			if err != nil {
				return nil, err
			}
		}
	}

	return &Insert{Table: table, Cols: cols, Values: values, Returning: returning, OnConflict: onConflict, ConflictAction: action}, nil
}

func (p *Parser) parseOnConflict() (*OnConflict, error) {
	// ON CONFLICT
	if err := p.expect(LX.T_ON); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.expect(LX.T_CONFLICT); err != nil {
		return nil, err
	}
	p.advance()

	var columns []string
	if p.current.Type == LX.T_LPAREN {
		p.advance()
		for {
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			columns = append(columns, p.current.Lexeme)
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

	// DO NOTHING or DO UPDATE SET
	if err := p.expect(LX.T_DO); err != nil {
		return nil, err
	}
	p.advance()

	if p.current.Type == LX.T_NOTHING {
		p.advance()
		return &OnConflict{Columns: columns, DoNothing: true}, nil
	}

	if err := p.expect(LX.T_UPDATE); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.expect(LX.T_SET); err != nil {
		return nil, err
	}
	p.advance()

	var setClauses []Pair
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

		// REQ000291: parseExpr handles EXCLUDED.col as QualifiedName
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		setClauses = append(setClauses, Pair{Col: col, Val: val})

		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}

	return &OnConflict{Columns: columns, SetClauses: setClauses}, nil
}

func (p *Parser) parseReturning() ([]Expr, error) {
	if p.current.Type != LX.T_RETURNING {
		return nil, nil
	}
	p.advance() // consume RETURNING

	var cols []Expr
	for {
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		cols = append(cols, expr)
		if p.current.Type != LX.T_COMMA {
			break
		}
		p.advance()
	}
	return cols, nil
}
