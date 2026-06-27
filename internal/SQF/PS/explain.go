package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"strings"
)

func (p *Parser) parseExplain() (*ExplainStmt, error) {
	p.advance() // consume EXPLAIN

	mode := ExplainNormal
	format := ExplainFormatText

	if p.current.Type == LX.T_ANALYZE {
		// Disambiguate: EXPLAIN ANALYZE followed by SELECT/INSERT etc.
		// is the EXPLAIN ANALYZE feature. Bare EXPLAIN ANALYZE or
		// EXPLAIN ANALYZE table is the ANALYZE statement.
		next := p.lex.Peek()
		isExplainAnalyze := next.Type == LX.T_SELECT || next.Type == LX.T_INSERT ||
			next.Type == LX.T_UPDATE || next.Type == LX.T_DELETE ||
			next.Type == LX.T_CREATE || next.Type == LX.T_DROP ||
			next.Type == LX.T_EXPLAIN || next.Type == LX.T_WITH ||
			next.Type == LX.T_VALUES || next.Type == LX.T_BEGIN ||
			next.Type == LX.T_COMMIT || next.Type == LX.T_ROLLBACK ||
			next.Type == LX.T_SAVEPOINT || next.Type == LX.T_RELEASE ||
			next.Type == LX.T_REINDEX || next.Type == LX.T_VACUUM ||
			next.Type == LX.T_TRUNCATE || next.Type == LX.T_PRAGMA
		if isExplainAnalyze {
			mode = ExplainAnalyze
			p.advance() // consume ANALYZE
		}
		// else: fall through to normal mode, ANALYZE stays as current
		//       token, and parseTableName below will handle it.
	} else if p.current.Type == LX.T_QUERY {
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

	// Parse optional FORMAT = text|tree|json|dot clause
	// FORMAT is treated as a keyword only in EXPLAIN context
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "FORMAT") {
		p.advance() // consume FORMAT
		if p.current.Type == LX.T_EQ {
			p.advance() // consume "="
			// Accept IDENT or JSON keyword for format value
			if p.current.Type == LX.T_IDENT || p.current.Type == LX.T_JSON {
				lower := strings.ToLower(p.current.Lexeme)
				if p.current.Type == LX.T_JSON {
					lower = "json"
				}
				switch lower {
				case "text":
					format = ExplainFormatText
				case "tree":
					format = ExplainFormatTree
				case "json":
					format = ExplainFormatJSON
				case "dot":
					format = ExplainFormatDOT
				}
				p.advance() // consume format value
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
		inner, innerErr = p.parseCreateDispatch()
	case LX.T_DROP:
		inner, innerErr = p.parseDropDispatch()
	case LX.T_EXPLAIN:
		inner, innerErr = p.parseExplain()
	case LX.T_ANALYZE:
		inner, innerErr = p.parseAnalyze()
	case LX.T_VACUUM:
		inner, innerErr = p.parseVacuum()
	case LX.T_TRUNCATE:
		inner, innerErr = p.parseTruncate()
	case LX.T_REINDEX:
		inner, innerErr = p.parseReindex()
	case LX.T_PRAGMA:
		inner, innerErr = p.parsePragma()
	case LX.T_WITH:
		inner, innerErr = p.parseWith()
	case LX.T_SAVEPOINT:
		inner, innerErr = p.parseSavepoint()
	case LX.T_RELEASE:
		inner, innerErr = p.parseReleaseSavepoint()
	case LX.T_ROLLBACK:
		inner, innerErr = p.parseRollbackTo()
	case LX.T_BEGIN:
		inner, innerErr = p.parseBegin()
	case LX.T_COMMIT:
		inner, innerErr = p.parseCommit()
	case LX.T_VALUES:
		// EXPLAIN VALUES ... is unusual but valid per the parser.
		inner, innerErr = p.parseValues()
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

	return &ExplainStmt{Mode: mode, Format: format, Inner: inner}, nil
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

	// Support PRAGMA name(value) syntax (e.g., PRAGMA table_info(users))
	if p.current.Type == LX.T_LPAREN {
		p.advance() // consume (
		if p.current.Type == LX.T_IDENT || p.current.Type == LX.T_STRING {
			stmt.Value = p.current.Lexeme
			p.advance()
		} else if p.current.Type == LX.T_INT {
			stmt.Value = p.current.Lexeme
			p.advance()
		}
		if p.current.Type == LX.T_RPAREN {
			p.advance() // consume )
		}
		return stmt, nil
	}

	if p.current.Type == LX.T_EQ {
		p.advance()
		if p.current.Type == LX.T_IDENT || p.current.Type == LX.T_STRING {
			stmt.Value = p.current.Lexeme
			p.advance()
		} else if p.current.Type == LX.T_INT {
			stmt.Value = p.current.Lexeme
			p.advance()
		} else if p.current.Type == LX.T_ON {
			// REQ000905: accept ON keyword for PRAGMA foreign_keys = ON
			stmt.Value = p.current.Lexeme
			p.advance()
		}
	}

	return stmt, nil
}
