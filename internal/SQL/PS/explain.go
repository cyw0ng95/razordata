package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

func (p *Parser) parseExplain() (*ExplainStmt, error) {
	p.advance() // consume EXPLAIN

	mode := ExplainNormal
	if p.current.Type == LX.T_QUERY {
		// Use lexer Peek to check if QUERY is followed by PLAN
		next := p.lex.Peek()
		if next.Type == LX.T_PLAN {
			mode = ExplainQueryPlan
			p.advance() // consume QUERY
			p.advance() // consume PLAN
		} else {
			// EXPLAIN QUERY without PLAN is invalid SQL
			return nil, &SyntaxError{
				Input:    p.lex.Input(),
				Line:     p.current.Line,
				Col:      p.current.Col,
				Expected: "PLAN",
				Got:      tokenName(p.current.Type),
				Lexeme:   p.current.Lexeme,
			}
		}
	}

	// Parse the inner statement without resetting the parser state.
	// We use the same dispatch logic as Parse() but without reset().
	var inner Stmt
	var innerErr error
	switch p.current.Type {
	case LX.T_SELECT:
		inner, innerErr = p.parseSelect()
	case LX.T_INSERT:
		inner, innerErr = p.parseInsert()
	case LX.T_UPDATE:
		inner, innerErr = p.parseUpdate()
	case LX.T_DELETE:
		inner, innerErr = p.parseDelete()
	case LX.T_CREATE:
		next := p.lex.Peek().Type
		if next == LX.T_INDEX {
			inner, innerErr = p.parseCreateIndex()
		} else if next == LX.T_UNIQUE && p.lex.Peek2().Type == LX.T_INDEX {
			inner, innerErr = p.parseCreateIndex()
		} else if next == LX.T_VIEW {
			inner, innerErr = p.parseCreateView()
		} else {
			inner, innerErr = p.parseCreateTable()
		}
	case LX.T_DROP:
		switch p.lex.Peek().Type {
		case LX.T_INDEX:
			inner, innerErr = p.parseDropIndex()
		case LX.T_VIEW:
			inner, innerErr = p.parseDropView()
		case LX.T_TRIGGER:
			inner, innerErr = p.parseDropTrigger()
		default:
			inner, innerErr = p.parseDropTable()
		}
	case LX.T_EXPLAIN:
		inner, innerErr = p.parseExplain()
	case LX.T_ANALYZE:
		inner, innerErr = p.parseAnalyze()
	case LX.T_VACUUM:
		inner, innerErr = p.parseVacuum()
	default:
		innerErr = &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "statement",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	if innerErr != nil {
		return nil, innerErr
	}

	return &ExplainStmt{Mode: mode, Inner: inner}, nil
}

func (p *Parser) parseAnalyze() (*AnalyzeStmt, error) {
	p.advance() // consume ANALYZE

	stmt := &AnalyzeStmt{}
	if p.current.Type == LX.T_IDENT {
		stmt.Table = p.current.Lexeme
		p.advance()
	}

	return stmt, nil
}

func (p *Parser) parseVacuum() (*VacuumStmt, error) {
	p.advance() // consume VACUUM

	stmt := &VacuumStmt{}
	if p.current.Type == LX.T_IDENT {
		stmt.Table = p.current.Lexeme
		p.advance()
	}

	return stmt, nil
}

func (p *Parser) parsePragma() (*PragmaStmt, error) {
	p.advance() // consume PRAGMA

	if p.current.Type != LX.T_IDENT {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "identifier after PRAGMA",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}

	stmt := &PragmaStmt{Name: p.current.Lexeme}
	p.advance()

	// Optional: PRAGMA name = value
	if p.current.Type == LX.T_EQ {
		p.advance()
		if p.current.Type == LX.T_IDENT || p.current.Type == LX.T_STRING {
			stmt.Value = p.current.Lexeme
			p.advance()
		} else if p.current.Type == LX.T_INT {
			stmt.Value = p.current.Lexeme
			p.advance()
		}
	}

	return stmt, nil
}
