package PS

import (
	"strings"

	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func (p *Parser) parseInsert() (*Insert, error) {
	p.advance() // consume INSERT

	var action ConflictAction
	if p.current.Type == LX.T_OR {
		p.advance() // consume OR
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
			return nil, &SyntaxError{
				Input:    p.lex.Input(),
				Line:     p.current.Line,
				Col:      p.current.Col,
				Expected: "ROLLBACK, ABORT, FAIL, IGNORE, or REPLACE after INSERT OR",
				Got:      tokenName(p.current.Type),
				Lexeme:   p.current.Lexeme,
			}
		}
		p.advance()
	}

	if err := p.expect(LX.T_INTO); err != nil {
		return nil, err
	}
	p.advance()

	return p.parseInsertTail(action)
}

// parseReplace parses REPLACE INTO ..., equivalent to INSERT OR REPLACE INTO ...
func (p *Parser) parseReplace() (*Insert, error) {
	p.advance() // consume REPLACE, p.current should be INTO
	if err := p.expect(LX.T_INTO); err != nil {
		return nil, err
	}
	p.advance()
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

	// REQ000707: INSERT INTO t SELECT ... (bulk insert from query)
	if p.current.Type == LX.T_SELECT {
		sel, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		returning, err := p.parseReturning()
		if err != nil {
			return nil, err
		}
		return &Insert{Table: table, Cols: cols, Select: sel, Returning: returning, ConflictAction: action}, nil
	}

	var err error

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

	// Parse ON CONFLICT clause if present (must come before RETURNING).
	// REQ001383: RETURNING can also follow ON CONFLICT.
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

	// Parse RETURNING (now placed after ON CONFLICT so UPSERT RETURNING works).
	returning, err := p.parseReturning()
	if err != nil {
		return nil, err
	}

	// Legacy path: ON CONFLICT after RETURNING was parsed. Since we now
	// parse ON CONFLICT first, this block is unreachable but kept for
	// defense-in-depth against grammar ambiguity.

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

	// REQ001364: optional WHERE on the conflict target (partial-index WHERE).
	var targetWhere Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		pred, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		targetWhere = pred
	}

	// DO NOTHING or DO UPDATE SET
	if err := p.expect(LX.T_DO); err != nil {
		return nil, err
	}
	p.advance()

	if p.current.Type == LX.T_NOTHING {
		p.advance()
		return &OnConflict{Columns: columns, TargetWhere: targetWhere, DoNothing: true}, nil
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

	// REQ001365: optional WHERE on DO UPDATE.
	var updateWhere Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		pred, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		updateWhere = pred
	}

	return &OnConflict{
		Columns:     columns,
		TargetWhere: targetWhere,
		SetClauses:  setClauses,
		UpdateWhere: updateWhere,
	}, nil
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
