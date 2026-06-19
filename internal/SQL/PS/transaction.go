package PS

import (
	"github.com/cyw0ng95/razordata/internal/SQL/LX"
)

func (p *Parser) parseBegin() (*BeginTX, error) {
	p.advance()
	bt := &BeginTX{}
	if p.current.Type == LX.T_DEFERRED || p.current.Type == LX.T_IMMEDIATE || p.current.Type == LX.T_EXCLUSIVE {
		bt.Mode = p.current.Lexeme
		p.advance()
	}
	// Optional TRANSACTION keyword
	if p.current.Type == LX.T_TRANSACTION {
		p.advance()
	}
	return bt, nil
}

// REQ000570: COMMIT / END [TRANSACTION] — COMMIT and END are synonyms.
func (p *Parser) parseCommit() (*CommitTX, error) {
	p.advance()
	if p.current.Type == LX.T_TRANSACTION {
		p.advance()
	}
	return &CommitTX{}, nil
}

// REQ000564: parseValues parses a standalone VALUES statement.
// VALUES (row1), (row2), ...
func (p *Parser) parseSavepoint() (*SavepointStmt, error) {
	p.advance() // consume SAVEPOINT

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &SavepointStmt{Name: name}, nil
}

func (p *Parser) parseReleaseSavepoint() (*ReleaseSavepointStmt, error) {
	p.advance() // consume RELEASE

	if p.current.Type == LX.T_SAVEPOINT {
		p.advance()
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &ReleaseSavepointStmt{Name: name}, nil
}

func (p *Parser) parseRollbackTo() (*RollbackToStmt, error) {
	p.advance() // consume ROLLBACK
	p.advance() // consume TO

	if p.current.Type == LX.T_SAVEPOINT {
		p.advance()
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &RollbackToStmt{Name: name}, nil
}

// parseCreateIndex parses `CREATE [UNIQUE] INDEX <name> ON <table> (<cols>)`.
// REQ000251 — secondary indexes MVP.
// On entry, current token is CREATE.
func (p *Parser) parseSet() (*SetTransactionStmt, error) {
	p.advance() // consume SET
	if err := p.expect(LX.T_TRANSACTION); err != nil {
		return nil, err
	}
	p.advance() // consume TRANSACTION
	if err := p.expect(LX.T_ISOLATION); err != nil {
		return nil, err
	}
	p.advance() // consume ISOLATION
	if err := p.expect(LX.T_LEVEL); err != nil {
		return nil, err
	}
	p.advance() // consume LEVEL

	// Parse isolation level
	level := ""
	switch p.current.Type {
	case LX.T_READ:
		p.advance()
		switch p.current.Type {
		case LX.T_UNCOMMITTED:
			level = "READ UNCOMMITTED"
			p.advance()
		case LX.T_COMMITTED:
			level = "READ COMMITTED"
			p.advance()
		default:
			return nil, &SyntaxError{
				Input:  p.lex.Input(),
				Line:   p.current.Line,
				Col:    p.current.Col,
				Got:    tokenName(p.current.Type),
				Lexeme: p.current.Lexeme,
			}
		}
	case LX.T_REPEATABLE:
		p.advance()
		if err := p.expect(LX.T_READ); err != nil {
			return nil, err
		}
		level = "REPEATABLE READ"
		p.advance()
	case LX.T_SERIALIZABLE:
		level = "SERIALIZABLE"
		p.advance()
	default:
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
	return &SetTransactionStmt{Level: level}, nil
}

// parseCreateView parses CREATE [TEMP|TEMPORARY] VIEW name AS SELECT ... (REQ000240).
