package PS

import (
	"fmt"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	"strings"
)

func (p *Parser) parseCreateTable() (*CreateTable, error) {
	p.advance()

	if err := p.expect(LX.T_TABLE); err != nil {
		return nil, err
	}
	p.advance()

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	// CREATE TABLE <name> AS SELECT ... (REQ000520)
	if p.current.Type == LX.T_AS {
		p.advance()
		raw, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		sel, ok := raw.(*Select)
		if !ok {
			return nil, fmt.Errorf("expected SELECT after AS, got %T", raw)
		}
		return &CreateTable{Name: name, Select: sel}, nil
	}

	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()

	var cols []ColDef
	var pk *string
	for {
		if p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE || p.current.Type == LX.T_FOREIGN {
			break
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		colName := p.current.Lexeme
		p.advance()

		// Parse type with optional size/precision (REQ000207)
		typeInfo, err := p.parseCastType()
		if err != nil {
			return nil, err
		}

		col := NewColDef(colName, typeInfo.Type)
		col.Size = typeInfo.Size

		for p.current.Type == LX.T_NOTNULL || p.current.Type == LX.T_PRIMARY ||
			p.current.Type == LX.T_DEFAULT || p.current.Type == LX.T_UNIQUE ||
			p.current.Type == LX.T_NOT || p.current.Type == LX.T_CHECK {
			if p.current.Type == LX.T_PRIMARY {
				peek := p.lex.Peek()
				if peek.Type == LX.T_KEY {
					p.advance()
					p.advance()
					if p.current.Type == LX.T_LPAREN {
						break
					}
					col.PK = true
					// REQ000482: accept AUTOINCREMENT after PRIMARY KEY
					if p.current.Type == LX.T_AUTOINCREMENT {
						col.Autoincrement = true
						p.advance()
					}
					continue
				}
			}
			switch p.current.Type {
			case LX.T_NOTNULL:
				col.Nullable = false
				p.advance()
			case LX.T_NOT:
				p.advance()
				if p.current.Type == LX.T_NULL {
					col.Nullable = false
					p.advance()
				}
			case LX.T_PRIMARY:
				col.PK = true
				col.Nullable = false
				p.advance()
				if p.current.Type == LX.T_KEY {
					p.advance()
				}
				// REQ000482: accept AUTOINCREMENT after PRIMARY KEY
				if p.current.Type == LX.T_AUTOINCREMENT {
					col.Autoincrement = true
					p.advance()
				}
			case LX.T_DEFAULT:
				p.advance()
				d, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				col.Default = d
			case LX.T_CHECK:
				p.advance()
				if err := p.expect(LX.T_LPAREN); err != nil {
					return nil, err
				}
				p.advance()
				checkExpr, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				if err := p.expect(LX.T_RPAREN); err != nil {
					return nil, err
				}
				p.advance()
				col.Check = checkExpr
			case LX.T_UNIQUE:
				col.Unique = true
				p.advance()
				if p.current.Type == LX.T_KEY {
					p.advance()
				}
			}
		}

		// REQ000248: generated column syntax `AS (expr) STORED` or
		// `AS (expr) VIRTUAL`. We support STORED only in v0.27.0
		// (materialized on write). VIRTUAL is accepted in the
		// parser and the column is treated like a normal column at
		// the storage layer (deferred materialization).
		if p.current.Type == LX.T_AS {
			p.advance()
			if err := p.expect(LX.T_LPAREN); err != nil {
				return nil, err
			}
			p.advance()
			genExpr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expect(LX.T_RPAREN); err != nil {
				return nil, err
			}
			p.advance()
			col.Generated = genExpr
			col.Virtual = false
			// STORED and VIRTUAL are not reserved keywords; we
			// accept them as identifiers after the expression.
			if p.current.Type == LX.T_IDENT {
				switch strings.ToUpper(p.current.Lexeme) {
				case "VIRTUAL":
					col.Virtual = true
					p.advance()
				case "STORED":
					p.advance()
				}
			}
		}

		// REQ000126: column-level REFERENCES clause (after other constraints)
		if p.current.Type == LX.T_REFERENCES {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			col.ReferencesTable = p.current.Lexeme
			p.advance()
			if p.current.Type == LX.T_LPAREN {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				col.ReferencesColumn = p.current.Lexeme
				p.advance()
				if err := p.expect(LX.T_RPAREN); err != nil {
					return nil, err
				}
				p.advance()
			}
			for p.current.Type == LX.T_ON {
				p.advance()
				if p.current.Type == LX.T_DELETE {
					p.advance()
					col.OnDelete = p.parseFKAction()
				} else if p.current.Type == LX.T_UPDATE {
					p.advance()
					col.OnUpdate = p.parseFKAction()
				}
			}
			// REQ000561: optional MATCH name at column level
			if p.current.Type == LX.T_MATCH {
				p.advance()
				if p.current.Type != LX.T_RPAREN && p.current.Type != LX.T_COMMA &&
					p.current.Type != LX.T_ON && p.current.Type != LX.T_NOT &&
					p.current.Type != LX.T_DEFERRABLE && p.current.Type != LX.T_INITIALLY {
					col.Match = p.current.Lexeme
					p.advance()
				}
			}
			// REQ000561: optional [NOT] DEFERRABLE at column level
			if p.current.Type == LX.T_NOT {
				if p.lex.Peek().Type == LX.T_DEFERRABLE {
					p.advance()
					p.advance()
					col.Deferrable = "NOT DEFERRABLE"
				}
			} else if p.current.Type == LX.T_DEFERRABLE {
				p.advance()
				col.Deferrable = "DEFERRABLE"
			}
			if p.current.Type == LX.T_INITIALLY {
				p.advance()
				if p.current.Type == LX.T_DEFERRED || p.current.Type == LX.T_IMMEDIATE {
					col.Initially = p.current.Lexeme
					p.advance()
				}
			}
		}

		cols = append(cols, col)

		if p.current.Type == LX.T_COMMA {
			p.advance()
			continue
		}
		if p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE || p.current.Type == LX.T_FOREIGN {
			break
		}
		if p.current.Type == LX.T_RPAREN {
			break
		}
		break
	}

	var uniqueConstraints []UniqueKey
	var foreignKeys []ForeignKeyConstraint
	for p.current.Type == LX.T_PRIMARY || p.current.Type == LX.T_UNIQUE || p.current.Type == LX.T_FOREIGN {
		isPK := p.current.Type == LX.T_PRIMARY
		isFK := p.current.Type == LX.T_FOREIGN
		p.advance()
		if isPK {
			if err := p.expect(LX.T_KEY); err != nil {
				return nil, err
			}
			p.advance()
		} else if isFK {
			if err := p.expect(LX.T_KEY); err != nil {
				return nil, err
			}
			p.advance()
		} else if p.current.Type == LX.T_KEY {
			p.advance()
		}
		if err := p.expect(LX.T_LPAREN); err != nil {
			return nil, err
		}
		p.advance()
		// Parse one or more identifiers inside the parens.
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		firstName := p.current.Lexeme
		p.advance()
		names := []string{firstName}
		for p.current.Type == LX.T_COMMA {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			names = append(names, p.current.Lexeme)
			p.advance()
		}
		if err := p.expect(LX.T_RPAREN); err != nil {
			return nil, err
		}
		p.advance()
		if isFK {
			// FOREIGN KEY (cols) REFERENCES table(refCols) [ON DELETE/UPDATE action]
			if err := p.expect(LX.T_REFERENCES); err != nil {
				return nil, err
			}
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			refTable := p.current.Lexeme
			p.advance()
			var refCols []string
			if p.current.Type == LX.T_LPAREN {
				p.advance()
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				refCols = append(refCols, p.current.Lexeme)
				p.advance()
				for p.current.Type == LX.T_COMMA {
					p.advance()
					if err := p.expect(LX.T_IDENT); err != nil {
						return nil, err
					}
					refCols = append(refCols, p.current.Lexeme)
					p.advance()
				}
				if err := p.expect(LX.T_RPAREN); err != nil {
					return nil, err
				}
				p.advance()
			}
			fk := ForeignKeyConstraint{Columns: names, RefTable: refTable, RefColumns: refCols}
			for p.current.Type == LX.T_ON {
				p.advance()
				if p.current.Type == LX.T_DELETE {
					p.advance()
					fk.OnDelete = p.parseFKAction()
				} else if p.current.Type == LX.T_UPDATE {
					p.advance()
					fk.OnUpdate = p.parseFKAction()
				}
			}
			// REQ000561: optional MATCH name (can appear before or after ON)
			if p.current.Type == LX.T_MATCH {
				p.advance()
				if p.current.Type != LX.T_RPAREN && p.current.Type != LX.T_COMMA &&
					p.current.Type != LX.T_ON && p.current.Type != LX.T_NOT &&
					p.current.Type != LX.T_DEFERRABLE && p.current.Type != LX.T_INITIALLY {
					fk.Match = p.current.Lexeme
					p.advance()
				}
			}
			// Re-check for ON clause after MATCH (loop for multiple ON actions)
			for p.current.Type == LX.T_ON {
				p.advance()
				if p.current.Type == LX.T_DELETE {
					p.advance()
					fk.OnDelete = p.parseFKAction()
				} else if p.current.Type == LX.T_UPDATE {
					p.advance()
					fk.OnUpdate = p.parseFKAction()
				}
			}
			// REQ000561: optional [NOT] DEFERRABLE [INITIALLY DEFERRED|IMMEDIATE]
			if p.current.Type == LX.T_NOT {
				if p.lex.Peek().Type == LX.T_DEFERRABLE {
					p.advance() // consume NOT
					p.advance() // consume DEFERRABLE
					fk.Deferrable = "NOT DEFERRABLE"
				}
			} else if p.current.Type == LX.T_DEFERRABLE {
				p.advance()
				fk.Deferrable = "DEFERRABLE"
			}
			if p.current.Type == LX.T_INITIALLY {
				p.advance()
				if p.current.Type == LX.T_DEFERRED || p.current.Type == LX.T_IMMEDIATE {
					fk.Initially = p.current.Lexeme
					p.advance()
				}
			}
			foreignKeys = append(foreignKeys, fk)
		} else if isPK {
			// REQ000519: composite PRIMARY KEY. Use the first
			// column as the primary key; remaining columns are
			// treated as part of a UNIQUE constraint.
			pkName := names[0]
			pk = &pkName
			if len(names) > 1 {
				uniqueConstraints = append(uniqueConstraints, UniqueKey{Cols: names})
			}
		} else {
			uniqueConstraints = append(uniqueConstraints, UniqueKey{Cols: names})
		}
		// Allow comma between multiple UNIQUE / PRIMARY clauses.
		if p.current.Type == LX.T_COMMA {
			p.advance()
		}
	}

	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()

	var withoutRowid, strict bool

	// REQ000738: optional WITHOUT ROWID suffix
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "WITHOUT") {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		if !strings.EqualFold(p.current.Lexeme, "ROWID") {
			return nil, fmt.Errorf("expected ROWID after WITHOUT, got %s", p.current.Lexeme)
		}
		p.advance()
		withoutRowid = true
		// Optional comma before subsequent clauses.
		if p.current.Type == LX.T_COMMA {
			p.advance()
		}
	}

	// REQ000739: optional STRICT suffix
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "STRICT") {
		p.advance()
		strict = true
	}

	for _, col := range cols {
		if col.PK {
			pk = &col.Name
			break
		}
	}

	return &CreateTable{Name: name, Cols: cols, PK: pk, UniqueConstraints: uniqueConstraints, ForeignKeys: foreignKeys, WithoutRowid: withoutRowid, Strict: strict}, nil
}

// parseFKAction parses CASCADE / RESTRICT / SET NULL / SET DEFAULT / NO ACTION.
func (p *Parser) parseFKAction() string {
	switch p.current.Type {
	case LX.T_CASCADE:
		p.advance()
		return "CASCADE"
	case LX.T_RESTRICT:
		p.advance()
		return "RESTRICT"
	case LX.T_SET:
		p.advance()
		if p.current.Type == LX.T_NULL {
			p.advance()
			return "SET NULL"
		}
		if p.current.Type == LX.T_DEFAULT {
			p.advance()
			return "SET DEFAULT"
		}
		return "SET"
	case LX.T_NO:
		p.advance()
		if p.current.Type == LX.T_ACTION {
			p.advance()
			return "NO ACTION"
		}
		return "NO"
	default:
		return "NO ACTION"
	}
}

func (p *Parser) parseDropTable() (*DropTable, error) {
	p.advance()

	if err := p.expect(LX.T_TABLE); err != nil {
		return nil, err
	}
	p.advance()

	// REQ000497: DROP TABLE IF EXISTS
	ifExists := false
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if p.current.Type == LX.T_EXISTS {
			ifExists = true
			p.advance()
		}
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &DropTable{Name: name, IfExists: ifExists}, nil
}

func (p *Parser) parseCreateIndex() (*CreateIndexStmt, error) {
	unique := false
	p.advance() // consume CREATE
	// current is now INDEX, or UNIQUE if CREATE UNIQUE INDEX
	if p.current.Type == LX.T_UNIQUE {
		unique = true
		p.advance() // consume UNIQUE
	}
	if err := p.expect(LX.T_INDEX); err != nil {
		return nil, err
	}
	p.advance() // consume INDEX
	// REQ000479: CREATE INDEX IF NOT EXISTS
	ifExists := false
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if p.current.Type == LX.T_NOT {
			p.advance()
			if p.current.Type == LX.T_EXISTS {
				ifExists = true
				p.advance()
			}
		}
	}
	// Read index name
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()
	if err := p.expect(LX.T_ON); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()
	if err := p.expect(LX.T_LPAREN); err != nil {
		return nil, err
	}
	p.advance()
	cols, err := p.parseIndexColumnList()
	if err != nil {
		return nil, err
	}
	if err := p.expect(LX.T_RPAREN); err != nil {
		return nil, err
	}
	p.advance()
	// REQ000566: optional WHERE clause for partial index
	var where Expr
	if p.current.Type == LX.T_WHERE {
		p.advance()
		where, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	return &CreateIndexStmt{
		Name:           name,
		Table:          table,
		IndexedColumns: cols,
		Unique:         unique,
		IfExists:       ifExists,
		Where:          where,
	}, nil
}

// parseIndexColumnList parses a comma-separated list of identifiers with optional COLLATE.
func (p *Parser) parseIndexColumnList() ([]IndexedColumn, error) {
	var cols []IndexedColumn
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	col := IndexedColumn{Name: p.current.Lexeme}
	p.advance()
	// optional COLLATE name
	if p.current.Type == LX.T_COLLATE {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		col.Collation = p.current.Lexeme
		p.advance()
	}
	cols = append(cols, col)
	for p.current.Type == LX.T_COMMA {
		p.advance()
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		c := IndexedColumn{Name: p.current.Lexeme}
		p.advance()
		if p.current.Type == LX.T_COLLATE {
			p.advance()
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			c.Collation = p.current.Lexeme
			p.advance()
		}
		cols = append(cols, c)
	}
	return cols, nil
}

// parseIdentList parses a comma-separated list of identifiers
// until the closing paren or end of statement.
func (p *Parser) parseDropIndex() (*DropIndexStmt, error) {
	p.advance() // consume DROP
	if err := p.expect(LX.T_INDEX); err != nil {
		return nil, err
	}
	p.advance() // consume INDEX
	// REQ000480: DROP INDEX IF EXISTS
	ifExists := false
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if p.current.Type == LX.T_EXISTS {
			ifExists = true
			p.advance()
		}
	}
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()
	return &DropIndexStmt{Name: name, IfExists: ifExists}, nil
}

// REQ000529/569: parseIndexHint parses INDEXED BY name or NOT INDEXED.
// Returns nil if neither clause is present.
func (p *Parser) parseIndexHint() *IndexHint {
	if p.current.Type != LX.T_INDEXED && !(p.current.Type == LX.T_NOT && p.lex.Peek().Type == LX.T_INDEXED) {
		return nil
	}
	if p.current.Type == LX.T_NOT {
		p.advance() // consume NOT
		p.advance() // consume INDEXED
		return &IndexHint{}
	}
	// INDEXED BY name
	p.advance() // consume INDEXED
	if p.current.Type != LX.T_BY {
		return nil
	}
	p.advance() // consume BY
	if p.current.Type != LX.T_IDENT {
		return nil
	}
	hint := &IndexHint{IndexedBy: p.current.Lexeme}
	p.advance()
	return hint
}

// parseSet parses SET TRANSACTION ISOLATION LEVEL ... (REQ000123).
func (p *Parser) parseCreateView() (*CreateViewStmt, error) {
	p.advance() // consume CREATE
	var temporary bool
	if p.current.Type == LX.T_TEMP || p.current.Type == LX.T_TEMPORARY {
		temporary = true
		p.advance() // consume TEMP/TEMPORARY
	}
	if err := p.expect(LX.T_VIEW); err != nil {
		return nil, err
	}
	p.advance() // consume VIEW
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()
	if err := p.expect(LX.T_AS); err != nil {
		return nil, err
	}
	p.advance() // consume AS
	sel, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	return &CreateViewStmt{Name: name, As: sel, Temporary: temporary}, nil
}

// parseAlterTable parses ALTER TABLE name ADD/DROP COLUMN col (REQ000243).
func (p *Parser) parseAlterTable() (*AlterTableStmt, error) {
	p.advance() // consume ALTER
	if err := p.expect(LX.T_TABLE); err != nil {
		return nil, err
	}
	p.advance() // consume TABLE
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	table := p.current.Lexeme
	p.advance()

	switch p.current.Type {
	case LX.T_ADD:
		p.advance() // consume ADD
		if p.current.Type == LX.T_COLUMN {
			p.advance() // consume COLUMN (optional)
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		colName := p.current.Lexeme
		p.advance()

		// Parse type with optional size/precision
		typeInfo, err := p.parseCastType()
		if err != nil {
			return nil, err
		}
		col := NewColDef(colName, typeInfo.Type)
		col.Size = typeInfo.Size

		// Parse column constraints (NOT NULL, DEFAULT, etc.)
		for p.current.Type == LX.T_NOTNULL || p.current.Type == LX.T_DEFAULT ||
			p.current.Type == LX.T_NOT {
			if p.current.Type == LX.T_NOT {
				p.advance()
				if p.current.Type == LX.T_NULL {
					col.Nullable = false
					p.advance()
				} else {
					return nil, &SyntaxError{
						Input:  p.lex.Input(),
						Line:   p.current.Line,
						Col:    p.current.Col,
						Got:    tokenName(p.current.Type),
						Lexeme: p.current.Lexeme,
					}
				}
			} else if p.current.Type == LX.T_NOTNULL {
				col.Nullable = false
				p.advance()
			} else if p.current.Type == LX.T_DEFAULT {
				p.advance()
				d, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				col.Default = d
			}
		}

		return &AlterTableStmt{Table: table, Action: "ADD COLUMN", Column: colName, NewCol: &col}, nil

	case LX.T_DROP:
		p.advance() // consume DROP
		if p.current.Type == LX.T_COLUMN {
			p.advance() // consume COLUMN (optional)
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		col := p.current.Lexeme
		p.advance()
		return &AlterTableStmt{Table: table, Action: "DROP COLUMN", Column: col}, nil

	case LX.T_RENAME:
		p.advance() // consume RENAME
		// REQ000498: ALTER TABLE t RENAME COLUMN old TO new
		if p.current.Type == LX.T_COLUMN {
			p.advance() // consume COLUMN
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			oldCol := p.current.Lexeme
			p.advance()
			if err := p.expect(LX.T_TO); err != nil {
				return nil, err
			}
			p.advance() // consume TO
			if err := p.expect(LX.T_IDENT); err != nil {
				return nil, err
			}
			newCol := p.current.Lexeme
			p.advance()
			return &AlterTableStmt{Table: table, Action: "RENAME COLUMN", Column: oldCol, NewName: newCol}, nil
		}
		// ALTER TABLE t RENAME TO new_name (table rename)
		if p.current.Type == LX.T_TO {
			p.advance() // consume TO (optional)
		}
		if err := p.expect(LX.T_IDENT); err != nil {
			return nil, err
		}
		newName := p.current.Lexeme
		p.advance()
		return &AlterTableStmt{Table: table, Action: "RENAME", Column: newName}, nil

	default:
		return nil, &SyntaxError{
			Input:  p.lex.Input(),
			Line:   p.current.Line,
			Col:    p.current.Col,
			Got:    tokenName(p.current.Type),
			Lexeme: p.current.Lexeme,
		}
	}
}

// parseCreateTrigger parses `CREATE [TEMP|TEMPORARY] TRIGGER [IF NOT EXISTS]
// name (BEFORE|AFTER|INSTEAD OF) (INSERT|DELETE|UPDATE [OF cols]) ON table
// [FOR EACH ROW] BEGIN stmt; stmt; ... END`. REQ000435.
// On entry, current token is CREATE.
func (p *Parser) parseCreateTrigger() (*TriggerStmt, error) {
	p.advance() // consume CREATE
	if err := p.expect(LX.T_TRIGGER); err != nil {
		return nil, err
	}
	p.advance() // consume TRIGGER

	trigger := &TriggerStmt{Time: "BEFORE", Event: "INSERT", ForEach: "FOR EACH ROW"}

	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if err := p.expect(LX.T_NOT); err != nil {
			return nil, err
		}
		p.advance()
		if err := p.expect(LX.T_EXISTS); err != nil {
			return nil, err
		}
		p.advance()
		trigger.IfNotExists = true
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	trigger.Name = p.current.Lexeme
	p.advance()

	if p.current.Type == LX.T_BEFORE || p.current.Type == LX.T_AFTER {
		trigger.Time = strings.ToUpper(p.current.Lexeme)
		p.advance()
	} else if p.current.Type == LX.T_INSTEAD {
		p.advance()
		if err := p.expect(LX.T_OF); err != nil {
			return nil, err
		}
		p.advance()
		trigger.Time = "INSTEAD OF"
	}

	switch p.current.Type {
	case LX.T_INSERT, LX.T_DELETE:
		trigger.Event = strings.ToUpper(p.current.Lexeme)
		p.advance()
	case LX.T_UPDATE:
		trigger.Event = "UPDATE"
		p.advance()
		if p.current.Type == LX.T_OF {
			p.advance()
			for {
				if err := p.expect(LX.T_IDENT); err != nil {
					return nil, err
				}
				trigger.OfCols = append(trigger.OfCols, p.current.Lexeme)
				p.advance()
				if p.current.Type != LX.T_COMMA {
					break
				}
				p.advance()
			}
		}
	default:
		return nil, fmt.Errorf("ps: syntax error at line %d col %d: expected INSERT/UPDATE/DELETE, got %s", p.current.Line, p.current.Col, tokenName(p.current.Type))
	}

	if err := p.expect(LX.T_ON); err != nil {
		return nil, err
	}
	p.advance()
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	trigger.OnTable = p.current.Lexeme
	p.advance()

	if p.current.Type == LX.T_FOR {
		p.advance()
		if err := p.expect(LX.T_EACH); err != nil {
			return nil, err
		}
		p.advance()
		if err := p.expect(LX.T_ROW); err != nil {
			return nil, err
		}
		p.advance()
	}

	if p.current.Type == LX.T_WHEN {
		p.advance()
		w, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		trigger.When = w
	}

	if p.current.Type == LX.T_BEGIN {
		p.advance()
		for p.current.Type != LX.T_END && p.current.Type != LX.T_EOF {
			if p.current.Type == LX.T_SEMICOLON {
				p.advance()
				continue
			}
			stmt, err := p.parseTriggerBodyStmt()
			if err != nil {
				return nil, err
			}
			if stmt != nil {
				trigger.Body = append(trigger.Body, stmt)
			}
		}
		if p.current.Type == LX.T_END {
			p.advance()
		}
		if p.current.Type == LX.T_SEMICOLON {
			p.advance()
		}
	} else {
		stmt, err := p.parseTriggerBodyStmt()
		if err != nil {
			return nil, err
		}
		trigger.Body = append(trigger.Body, stmt)
		if p.current.Type == LX.T_SEMICOLON {
			p.advance()
		}
	}

	return trigger, nil
}

// parseTriggerBodyStmt parses a single statement inside a trigger body
// without resetting parser state. Mirrors the dispatch in Parse() for
// the statement types commonly used in trigger bodies. REQ000435.
func (p *Parser) parseTriggerBodyStmt() (Stmt, error) {
	switch p.current.Type {
	case LX.T_SELECT:
		return p.parseSelect()
	case LX.T_INSERT:
		return p.parseInsert()
	case LX.T_UPDATE:
		return p.parseUpdate()
	case LX.T_DELETE:
		return p.parseDelete()
	default:
		// Unknown statement type inside trigger body: skip until
		// next semicolon or END so we don't fail on partial parses.
		for p.current.Type != LX.T_SEMICOLON && p.current.Type != LX.T_END && p.current.Type != LX.T_EOF {
			p.advance()
		}
		return nil, nil
	}
}

// parseTruncate parses TRUNCATE [TABLE] name. REQ000476 (iter-28).
func (p *Parser) parseTruncate() (*TruncateStmt, error) {
	p.advance() // consume TRUNCATE

	if p.current.Type == LX.T_TABLE {
		p.advance()
	}

	if p.current.Type != LX.T_IDENT {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "table name after TRUNCATE",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}

	name := p.current.Lexeme
	p.advance()

	return &TruncateStmt{Table: name}, nil
}

// parseReindex parses REINDEX [name]. REQ000478 (iter-28).
func (p *Parser) parseReindex() (*ReindexStmt, error) {
	p.advance() // consume REINDEX

	stmt := &ReindexStmt{}
	if p.current.Type == LX.T_IDENT {
		stmt.Target = p.current.Lexeme
		p.advance()
	}

	return stmt, nil
}

// parseDropView parses DROP VIEW [IF EXISTS] name. REQ000494 (iter-28).
func (p *Parser) parseDropView() (*DropViewStmt, error) {
	p.advance() // consume DROP
	p.advance() // consume VIEW

	stmt := &DropViewStmt{}
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if p.current.Type == LX.T_EXISTS {
			p.advance()
			stmt.IfExists = true
		}
	}

	if p.current.Type != LX.T_IDENT {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "view name after DROP VIEW",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	stmt.Name = p.current.Lexeme
	p.advance()

	return stmt, nil
}

// parseDropMaterializedView parses DROP MATERIALIZED VIEW [IF EXISTS] name
func (p *Parser) parseDropMaterializedView() (*DropMatViewStmt, error) {
	p.advance() // consume DROP
	if err := p.expect(LX.T_MATERIALIZED); err != nil {
		return nil, err
	}
	p.advance() // consume MATERIALIZED
	if err := p.expect(LX.T_VIEW); err != nil {
		return nil, err
	}
	p.advance() // consume VIEW

	stmt := &DropMatViewStmt{}

	// Optional IF EXISTS
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if err := p.expect(LX.T_EXISTS); err != nil {
			return nil, err
		}
		p.advance()
		stmt.IfExists = true
	}

	if p.current.Type != LX.T_IDENT {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "materialized view name after DROP MATERIALIZED VIEW",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	stmt.Name = p.current.Lexeme
	p.advance()

	return stmt, nil
}

// parseDropTrigger parses DROP TRIGGER [IF EXISTS] name. REQ000496 (iter-28).
func (p *Parser) parseDropTrigger() (*DropTriggerStmt, error) {
	p.advance() // consume DROP
	p.advance() // consume TRIGGER

	stmt := &DropTriggerStmt{}
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if p.current.Type == LX.T_EXISTS {
			p.advance()
			stmt.IfExists = true
		}
	}

	if p.current.Type != LX.T_IDENT {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "trigger name after DROP TRIGGER",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	stmt.Name = p.current.Lexeme
	p.advance()

	return stmt, nil
}

// parseCreateMaterializedView parses CREATE MATERIALIZED VIEW [IF NOT EXISTS]
// name [INCREMENTAL] AS SELECT ... REQ000316.
// On entry, current token is CREATE.
func (p *Parser) parseCreateMaterializedView() (*CreateMatViewStmt, error) {
	p.advance() // consume CREATE

	if err := p.expect(LX.T_MATERIALIZED); err != nil {
		return nil, err
	}
	p.advance() // consume MATERIALIZED

	if err := p.expect(LX.T_VIEW); err != nil {
		return nil, err
	}
	p.advance() // consume VIEW

	// Optional IF NOT EXISTS
	ifNotExists := false
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "IF") {
		p.advance()
		if err := p.expect(LX.T_NOT); err != nil {
			return nil, err
		}
		p.advance()
		if err := p.expect(LX.T_EXISTS); err != nil {
			return nil, err
		}
		p.advance()
		ifNotExists = true
	}

	// Optional INCREMENTAL keyword (default: manual refresh)
	incremental := false
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "INCREMENTAL") {
		p.advance()
		incremental = true
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	if err := p.expect(LX.T_AS); err != nil {
		return nil, err
	}
	p.advance() // consume AS

	sel, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	createSel, ok := sel.(*Select)
	if !ok {
		return nil, fmt.Errorf("expected SELECT after AS, got %T", sel)
	}

	return &CreateMatViewStmt{
		Name:        name,
		As:          createSel,
		IfNotExists: ifNotExists,
		Incremental: incremental,
	}, nil
}

// parseRefreshMatView parses REFRESH MATERIALIZED VIEW [CONCURRENTLY] name.
// On entry, current token is REFRESH.
func (p *Parser) parseRefreshMatView() (*RefreshMatViewStmt, error) {
	p.advance() // consume REFRESH

	if err := p.expect(LX.T_MATERIALIZED); err != nil {
		return nil, err
	}
	p.advance() // consume MATERIALIZED

	if err := p.expect(LX.T_VIEW); err != nil {
		return nil, err
	}
	p.advance() // consume VIEW

	// Optional CONCURRENTLY (accepted but not yet implemented)
	concurrently := false
	if p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "CONCURRENTLY") {
		p.advance()
		concurrently = true
	}

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &RefreshMatViewStmt{
		Name:         name,
		Concurrently: concurrently,
	}, nil
}

// parseAttach parses `ATTACH DATABASE expr AS name`. REQ000557.
// On entry the current token is T_ATTACH.
func (p *Parser) parseAttach() (*AttachStmt, error) {
	p.advance() // consume ATTACH
	// DATABASE is not a hard keyword; accept either T_DATABASE
	// or an identifier whose lexeme (case-insensitive) is
	// "DATABASE".
	if !(p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "DATABASE")) {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "DATABASE keyword after ATTACH",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	p.advance() // consume DATABASE

	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	if err := p.expect(LX.T_AS); err != nil {
		return nil, err
	}
	p.advance() // consume AS

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &AttachStmt{Expr: expr, Name: name}, nil
}

// parseDetach parses `DETACH DATABASE name`. REQ000557.
// On entry the current token is T_DETACH.
func (p *Parser) parseDetach() (*DetachStmt, error) {
	p.advance() // consume DETACH
	if !(p.current.Type == LX.T_IDENT && strings.EqualFold(p.current.Lexeme, "DATABASE")) {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "DATABASE keyword after DETACH",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	p.advance() // consume DATABASE

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance()

	return &DetachStmt{Name: name}, nil
}

// parseCreateVirtualTable parses CREATE VIRTUAL TABLE name USING module (args).
// The runtime does not execute virtual table modules (FTS5, R-Tree, etc.)
// so this is a syntax-only parse. REQ000737.
func (p *Parser) parseCreateVirtualTable() (*CreateVirtualTableStmt, error) {
	p.advance() // consume CREATE
	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	// We expect "VIRTUAL" as the identifier
	if !strings.EqualFold(p.current.Lexeme, "VIRTUAL") {
		return nil, &SyntaxError{
			Input:    p.lex.Input(),
			Line:     p.current.Line,
			Col:      p.current.Col,
			Expected: "VIRTUAL",
			Got:      tokenName(p.current.Type),
			Lexeme:   p.current.Lexeme,
		}
	}
	p.advance() // consume VIRTUAL

	if err := p.expect(LX.T_TABLE); err != nil {
		return nil, err
	}
	p.advance() // consume TABLE

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	name := p.current.Lexeme
	p.advance() // consume table name

	if err := p.expect(LX.T_USING); err != nil {
		return nil, err
	}
	p.advance() // consume USING

	if err := p.expect(LX.T_IDENT); err != nil {
		return nil, err
	}
	module := p.current.Lexeme
	p.advance() // consume module name

	var args []string
	if p.current.Type == LX.T_LPAREN {
		p.advance() // consume (
		for p.current.Type != LX.T_RPAREN && p.current.Type != LX.T_EOF {
			if len(args) > 0 {
				if p.current.Type != LX.T_COMMA {
					break
				}
				p.advance() // consume ,
			}
			if p.current.Type == LX.T_IDENT || p.current.Type == LX.T_STRING {
				args = append(args, p.current.Lexeme)
			} else if p.current.Type == LX.T_INT {
				args = append(args, p.current.Lexeme)
			}
			p.advance()
		}
		if p.current.Type == LX.T_RPAREN {
			p.advance() // consume )
		}
	}

	return &CreateVirtualTableStmt{Name: name, Module: module, Args: args}, nil
}
