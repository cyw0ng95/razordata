package WT

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type CreateTable struct {
	Stmt       *PS.CreateTable
	done       bool
	selectPlan DT.Operator // non-nil for CREATE TABLE AS SELECT (REQ000520)
}

// registerTableSchema registers a table in the in-memory DT.Tables and
// DT.Schemas maps. Returns the column metadata extracted from the AST.
// REQ000982: extracted from CreateTable.Next.
func registerTableSchema(stmt *PS.CreateTable) ([]string, []bool, []PS.Expr, []LX.TokenType, []int, []int, error) {
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	if _, ok := DT.Tables[stmt.Name]; ok {
		return nil, nil, nil, nil, nil, nil, DT.ErrTableExists
	}
	cols := make([]string, len(stmt.Cols))
	nullable := make([]bool, len(stmt.Cols))
	defaults := make([]PS.Expr, len(stmt.Cols))
	colTypes := make([]LX.TokenType, len(stmt.Cols))
	precisions := make([]int, len(stmt.Cols))
	scales := make([]int, len(stmt.Cols))
	for i, col := range stmt.Cols {
		cols[i] = col.Name
		nullable[i] = col.Nullable
		defaults[i] = col.Default
		colTypes[i] = col.Type
		precisions[i] = col.Precision
		scales[i] = col.Scale
	}
	DT.Tables[stmt.Name] = []DT.Row{}
	DT.Schemas[stmt.Name] = cols
	return cols, nullable, defaults, colTypes, precisions, scales, nil
}

// buildUniqueConstraints builds a list of DT.UniqueKey constraints from
// column-level ColDef.Unique and table-level UniqueConstraints.
// REQ000982: extracted from CreateTable.Next.
func buildUniqueConstraints(cols []string, stmt *PS.CreateTable) []DT.UniqueKey {
	var unique []DT.UniqueKey
	colIndex := make(map[string]int, len(cols))
	for i, n := range cols {
		colIndex[n] = i
	}
	for _, col := range stmt.Cols {
		if col.Unique {
			if idx, ok := colIndex[col.Name]; ok {
				unique = append(unique, DT.UniqueKey{Cols: []int{idx}})
			}
		}
	}
	for _, uk := range stmt.UniqueConstraints {
		idxs := make([]int, 0, len(uk.Cols))
		allFound := true
		for _, name := range uk.Cols {
			idx, ok := colIndex[name]
			if !ok {
				allFound = false
				break
			}
			idxs = append(idxs, idx)
		}
		if allFound && len(idxs) > 0 {
			unique = append(unique, DT.UniqueKey{Cols: idxs})
		}
	}
	return unique
}

// buildFKConstraints extracts DT.ForeignKeyConstraint from column-level
// and table-level foreign key definitions.
// REQ000982: extracted from CreateTable.Next.
func buildFKConstraints(stmt *PS.CreateTable) []DT.ForeignKeyConstraint {
	var fks []DT.ForeignKeyConstraint
		for _, col := range stmt.Cols {
			if col.ReferencesTable != "" {
				fk := DT.ForeignKeyConstraint{
					Columns:    []string{col.Name},
					RefTable:   col.ReferencesTable,
					RefColumns: []string{col.ReferencesColumn},
					OnDelete:   col.OnDelete,
					OnUpdate:   col.OnUpdate,
					Match:      col.Match,
				}
			if fk.OnDelete == "" {
				fk.OnDelete = "NO ACTION"
			}
			if fk.OnUpdate == "" {
				fk.OnUpdate = "NO ACTION"
			}
			fks = append(fks, fk)
		}
	}
	for _, fkAST := range stmt.ForeignKeys {
		fk := DT.ForeignKeyConstraint{
			Columns:    fkAST.Columns,
			RefTable:   fkAST.RefTable,
			RefColumns: fkAST.RefColumns,
			OnDelete:   fkAST.OnDelete,
			OnUpdate:   fkAST.OnUpdate,
			Match:      fkAST.Match,
		}
		if fk.OnDelete == "" {
			fk.OnDelete = "NO ACTION"
		}
		if fk.OnUpdate == "" {
			fk.OnUpdate = "NO ACTION"
		}
		fks = append(fks, fk)
	}
	return fks
}

// buildCheckConstraints captures CHECK constraint expressions from
// column definitions.
// REQ000982: extracted from CreateTable.Next.
func buildCheckConstraints(stmt *PS.CreateTable) []PS.Expr {
	checks := make([]PS.Expr, 0, len(stmt.Cols))
	for _, col := range stmt.Cols {
		checks = append(checks, col.Check)
	}
	return checks
}

// buildGeneratedColumns captures generated column expressions so the
// INSERT/UPDATE path can materialize them.
// REQ000982: extracted from CreateTable.Next.
func buildGeneratedColumns(stmt *PS.CreateTable) []PS.Expr {
	generated := make([]PS.Expr, len(stmt.Cols))
	for i, col := range stmt.Cols {
		if col.Generated != nil {
			generated[i] = col.Generated
		}
	}
	return generated
}

// persistToCatalog persists a CREATE TABLE to the system catalog.
// The catalog write is best-effort: a failure does not roll back
// the in-memory registration.
// REQ000982: extracted from CreateTable.Next.
func persistToCatalog(stmt *PS.CreateTable, cols []string, nullable []bool, colTypes []LX.TokenType, unique []DT.UniqueKey, pk string) {
	cat := DT.Catalog()
	if cat == nil {
		return
	}
	catCols := make([]ls.CatalogColumn, len(cols))
	for i, n := range cols {
		catCols[i] = ls.CatalogColumn{Name: n, Type: sc.TokenType(colTypes[i]), Nullable: nullable[i]}
	}
	catUnique := make([]ls.CatalogUnique, len(unique))
	for i, u := range unique {
		catUnique[i] = ls.CatalogUnique{Cols: append([]int(nil), u.Cols...)}
	}
	id, _ := DT.TableIDFor(stmt.Name)
	if id == 0 {
		id, _ = cat.NextID()
	}
	_ = cat.Put(ls.CatalogEntry{
		TableID:    id,
		Name:       stmt.Name,
		Columns:    catCols,
		PrimaryKey: pk,
		Unique:     catUnique,
		CreateSQL:  buildCreateSQL(stmt),
	})
}

func NewCreateTable(stmt *PS.CreateTable) *CreateTable {
	return &CreateTable{Stmt: stmt}
}

// NewCreateTableAs builds a CREATE TABLE AS SELECT operator. The
// selectPlan is the planned SELECT tree that produces the rows to
// insert into the new table. REQ000520.
func NewCreateTableAs(stmt *PS.CreateTable, selectPlan DT.Operator) *CreateTable {
	return &CreateTable{Stmt: stmt, selectPlan: selectPlan}
}

func (c *CreateTable) Next(ctx context.Context) (DT.Row, error) {
	if c.done {
		return DT.Row{}, DT.ErrNoRows
	}
	c.done = true

	// REQ001326: TEMP/TEMPORARY TABLE — register in-memory only,
	// no persistent storage or catalog entry.
	if c.Stmt.Temporary {
		cols := make([]string, len(c.Stmt.Cols))
		for i, col := range c.Stmt.Cols {
			cols[i] = col.Name
		}
		if len(cols) == 0 && c.Stmt.Select != nil && c.selectPlan != nil {
			// CREATE TEMP TABLE AS SELECT — schema from SELECT output.
			return c.nextAsSelectTemp(ctx)
		}
		DT.RegisterTempTable(c.Stmt.Name, cols)
		return DT.Row{}, DT.ErrNoRows
	}

	// REQ000910: WITHOUT ROWID storage is not yet implemented.
	if c.Stmt.WithoutRowid {
		return DT.Row{}, errors.New("ex: WITHOUT ROWID not yet supported")
	}

	// CREATE TABLE AS SELECT (REQ000520): the schema comes from
	// the SELECT output. Register the table, run the SELECT, and
	// insert rows.
	if c.Stmt.Select != nil && c.selectPlan != nil {
		return c.nextAsSelect(ctx)
	}

	// Extract column metadata and register the table.
	cols, nullable, defaults, colTypes, precisions, scales, err := registerTableSchema(c.Stmt)
	if err != nil {
		return DT.Row{}, err
	}

	var pk string
	if c.Stmt.PK != nil {
		pk = *c.Stmt.PK
	}
	// PRIMARY KEY implies NOT NULL. If PK is one of the cols, flip its
	// nullable bit so ValidateRow rejects NULL PK inserts.
	if pk != "" {
		for i, n := range cols {
			if n == pk {
				nullable[i] = false
			}
		}
	}

	// Build constraints.
	unique := buildUniqueConstraints(cols, c.Stmt)
	fks := buildFKConstraints(c.Stmt)
	generated := buildGeneratedColumns(c.Stmt)
	checks := buildCheckConstraints(c.Stmt)

	id := DT.RegisterStoreSchemaWithFK(c.Stmt.Name, cols, nullable, defaults, unique, pk, fks)
	// R16-3: record each column's SQL type token alongside the
	// schema so ExtractParamTypes can resolve `column = ?`
	// placeholders to their column type at Prepare time.
	DT.StoreMu.Lock()
	if ss, ok := DT.StoreSchemas[id]; ok {
		ss.ColTypes = append([]LX.TokenType(nil), colTypes...)
		ss.Precision = append([]int(nil), precisions...)
		ss.Scale = append([]int(nil), scales...)
		ss.Generated = generated
		ss.Checks = append([]PS.Expr(nil), checks...)
		// REQ001369: STRICT table type enforcement.
		ss.Strict = c.Stmt.Strict
		// REQ000367: DT.Tables without a PRIMARY KEY that are
		// registered for storage get a synthetic int64 rowid.
		// This makes them writable to the engine store while
		// keeping the user-visible schema unchanged.
		if pk == "" {
			ss.HiddenPK = true
		}
	}
	_ = ctx
	DT.StoreMu.Unlock()

	// Persist to the system catalog if one is wired in (iter-12).
	persistToCatalog(c.Stmt, cols, nullable, colTypes, unique, pk)

	return DT.Row{}, DT.ErrNoRows
}
func (c *CreateTable) Close() error {
	if c.selectPlan != nil {
		return c.selectPlan.Close()
	}
	return nil
}

// nextAsSelect implements CREATE TABLE AS SELECT: register the
// table using the SELECT's output schema, then iterate the SELECT
// plan and insert each row. REQ000520.
func (c *CreateTable) nextAsSelect(ctx context.Context) (DT.Row, error) {
	// Read first row to discover schema.
	firstRow, err := c.selectPlan.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			// Empty SELECT: register table with no columns.
			DT.TablesMu.Lock()
			DT.Tables[c.Stmt.Name] = []DT.Row{}
			DT.Schemas[c.Stmt.Name] = nil
			DT.TablesMu.Unlock()
			return DT.Row{}, DT.ErrNoRows
		}
		return DT.Row{}, err
	}
	cols := append([]string(nil), firstRow.Cols...)
	DT.TablesMu.Lock()
	if _, ok := DT.Tables[c.Stmt.Name]; ok {
		DT.TablesMu.Unlock()
		return DT.Row{}, DT.ErrTableExists
	}
	DT.Tables[c.Stmt.Name] = []DT.Row{firstRow}
	DT.Schemas[c.Stmt.Name] = cols
	DT.TablesMu.Unlock()
	// Drain remaining rows.
	for {
		row, err := c.selectPlan.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		DT.TablesMu.Lock()
		DT.Tables[c.Stmt.Name] = append(DT.Tables[c.Stmt.Name], row)
		DT.TablesMu.Unlock()
	}
	return DT.Row{}, DT.ErrNoRows
}

// nextAsSelectTemp implements CREATE TEMP TABLE AS SELECT. REQ001326.
func (c *CreateTable) nextAsSelectTemp(ctx context.Context) (DT.Row, error) {
	// Read first row to discover schema.
	firstRow, err := c.selectPlan.Next(ctx)
	if err != nil {
		if err == DT.ErrNoRows {
			DT.TablesMu.Lock()
			DT.TempTables[c.Stmt.Name] = []DT.Row{}
			DT.TempSchemas[c.Stmt.Name] = nil
			DT.TempTableNames[c.Stmt.Name] = true
			DT.TablesMu.Unlock()
			return DT.Row{}, DT.ErrNoRows
		}
		return DT.Row{}, err
	}
	cols := append([]string(nil), firstRow.Cols...)
	DT.TablesMu.Lock()
	DT.TempTables[c.Stmt.Name] = []DT.Row{firstRow}
	DT.TempSchemas[c.Stmt.Name] = cols
	DT.TempTableNames[c.Stmt.Name] = true
	DT.TablesMu.Unlock()
	for {
		row, err := c.selectPlan.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		DT.TablesMu.Lock()
		DT.TempTables[c.Stmt.Name] = append(DT.TempTables[c.Stmt.Name], row)
		DT.TablesMu.Unlock()
	}
	return DT.Row{}, DT.ErrNoRows
}

type DropTable struct {
	Stmt *PS.DropTable
	done bool
	rows int64
}

func NewDropTable(stmt *PS.DropTable) *DropTable {
	return &DropTable{Stmt: stmt}
}

func (d *DropTable) Next(ctx context.Context) (DT.Row, error) {
	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true

	DT.TablesMu.Lock()
	existing, tableOk := DT.Tables[d.Stmt.Name]
	_, tempOk := DT.TempTables[d.Stmt.Name]
	if !tableOk && !tempOk && !d.Stmt.IfExists {
		DT.TablesMu.Unlock()
		return DT.Row{}, fmt.Errorf("ex: no such table: %s", d.Stmt.Name)
	}
	if tableOk {
		d.rows = int64(len(existing))
		delete(DT.Tables, d.Stmt.Name)
		delete(DT.Schemas, d.Stmt.Name)
		delete(DT.TablePKs, d.Stmt.Name)
		delete(DT.TempTableNames, d.Stmt.Name)
	}
	if tempOk {
		d.rows = int64(len(DT.TempTables[d.Stmt.Name]))
		delete(DT.TempTables, d.Stmt.Name)
		delete(DT.TempSchemas, d.Stmt.Name)
		delete(DT.TempTableNames, d.Stmt.Name)
	}
	DT.TablesMu.Unlock()

	// Drop the store schema mapping.
	DT.StoreMu.Lock()
	id, idOk := DT.TableIDs[d.Stmt.Name]
	if idOk {
		delete(DT.StoreSchemas, id)
		delete(DT.TableIDs, d.Stmt.Name)
	}
	// Drop indexes associated with this table (REQ000828).
	delete(DT.RegisteredIndexes, d.Stmt.Name)
	DT.StoreMu.Unlock()

	// Drop triggers associated with this table (REQ000828).
	DropTriggersForTable(d.Stmt.Name)

	// Persist the drop to the system catalog.
	if idOk {
		if cat := DT.Catalog(); cat != nil {
			_ = cat.Delete(id)
		}
	}
	return DT.Row{}, DT.ErrNoRows
}

// buildCreateSQL reconstructs a canonical CREATE TABLE statement
// from a parsed PS.CreateTable. The output is best-effort — it is
// used for catalog persistence (display + admin dumps), not for
// re-parsing.
func buildCreateSQL(stmt *PS.CreateTable) string {
	b := []byte("CREATE TABLE ")
	b = append(b, stmt.Name...)
	b = append(b, []byte(" (")...)
	for i, col := range stmt.Cols {
		if i > 0 {
			b = append(b, []byte(", ")...)
		}
		b = append(b, col.Name...)
		if tok := typeToken(col.Type); tok != "" {
			b = append(b, ' ')
			b = append(b, []byte(tok)...)
		}
		if !col.Nullable {
			b = append(b, []byte(" NOT NULL")...)
		}
		if col.Unique {
			b = append(b, []byte(" UNIQUE")...)
		}
		if col.Default != nil {
			b = append(b, []byte(" DEFAULT ")...)
			b = append(b, []byte(defaultLiteral(col.Default))...)
		}
	}
	if stmt.PK != nil {
		b = append(b, []byte(", PRIMARY KEY (")...)
		b = append(b, *stmt.PK...)
		b = append(b, ')')
	}
	for _, uk := range stmt.UniqueConstraints {
		b = append(b, []byte(", UNIQUE (")...)
		for i, c := range uk.Cols {
			if i > 0 {
				b = append(b, []byte(", ")...)
			}
			b = append(b, c...)
		}
		b = append(b, ')')
	}
	b = append(b, ')')
	return string(b)
}

// typeToken maps a parser column-type token to its SQL spelling.
// The token IDs are the LX.T_* constants stored as int on the
// ColDef. Returns "" if the type is unknown.
func typeToken(t LX.TokenType) string {
	switch t {
	case LX.T_INT_KW:
		return "INTEGER"
	case LX.T_BIGINT:
		return "BIGINT"
	case LX.T_TEXT:
		return "TEXT"
	case LX.T_VARCHAR:
		return "VARCHAR"
	case LX.T_BOOL:
		return "BOOLEAN"
	case LX.T_FLOAT_KW:
		return "FLOAT"
	case LX.T_BLOB:
		return "BLOB"
	case LX.T_TIMESTAMP:
		return "TIMESTAMP"
	default:
		return ""
	}
}

// defaultLiteral renders a parser Expr as a SQL literal. The
// catalog only needs a faithful display string; for expressions
// other than the four built-in literal kinds we fall back to "?"
// rather than risking a wrong rendering.
func defaultLiteral(e PS.Expr) string {
	switch v := e.(type) {
	case *PS.NumberLiteral:
		return strconv.FormatInt(v.Val, 10)
	case *PS.FloatLiteral:
		return strconv.FormatFloat(v.Val, 'f', -1, 64)
	case *PS.StringLiteral:
		return "'" + v.Val + "'"
	case *PS.BoolLiteral:
		if v.Val {
			return "TRUE"
		}
		return "FALSE"
	default:
		return "?"
	}
}

func (d *DropTable) Close() error {
	return nil
}

func (d *DropTable) RowsAffected() int64 {
	return d.rows
}

// CreateIndex is the DDL operator for CREATE INDEX. iter-22.
// It registers the index in the EX layer (for writer maintenance)
// and persists the metadata to the catalog.
type CreateIndex struct {
	Stmt    *PS.CreateIndexStmt
	done    bool
	rowsAff int64
}

func NewCreateIndex(stmt *PS.CreateIndexStmt) *CreateIndex {
	return &CreateIndex{Stmt: stmt}
}

func (c *CreateIndex) Next(ctx context.Context) (DT.Row, error) {
	if c.done {
		return DT.Row{}, DT.ErrNoRows
	}
	c.done = true
	// REQ000479: IF NOT EXISTS — skip if index already exists
	if c.Stmt.IfExists {
		exists := false
		DT.StoreMu.Lock()
		for _, idxs := range DT.RegisteredIndexes {
			for _, idx := range idxs {
				if idx.Name == c.Stmt.Name {
					exists = true
					break
				}
			}
			if exists {
				break
			}
		}
		DT.StoreMu.Unlock()
		if exists {
			return DT.Row{}, DT.ErrNoRows
		}
	}
	// extract column name strings from IndexedColumns
	indexCols := make([]string, len(c.Stmt.IndexedColumns))
	for i, ic := range c.Stmt.IndexedColumns {
		indexCols[i] = ic.Name
	}
	// REQ001386: serialize partial index WHERE clause as predicate text.
	predText := ""
	if c.Stmt.Where != nil {
		predText = sprintWhere(c.Stmt.Where)
	}
	// Register for writer maintenance
	DT.RegisterIndexWithID(c.Stmt.Table, DT.RegisteredIndex{
		Name:      c.Stmt.Name,
		Columns:   indexCols,
		Unique:    c.Stmt.Unique,
		Predicate: predText,
	})
	// Persist to catalog if available
	if cat := DT.Catalog(); cat != nil {
		// Find the tableID
		if tableID, ok := DT.TableIDFor(c.Stmt.Table); ok {
			idx := ls.CatalogIndex{
				Name:      c.Stmt.Name,
				Columns:   indexCols,
				Unique:    c.Stmt.Unique,
				CreateSQL: "CREATE INDEX " + c.Stmt.Name + " ON " + c.Stmt.Table + " (" + joinStrings(indexCols, ", ") + ")",
			}
			if err := cat.PutIndex(tableID, idx); err != nil {
				// Duplicate or other error — surface it.
				return DT.Row{}, err
			}
		}
	}
	c.rowsAff = 0
	return DT.Row{}, DT.ErrNoRows
}

func (c *CreateIndex) Close() error        { return nil }
func (c *CreateIndex) RowsAffected() int64 { return c.rowsAff }

// DropIndex is the DDL operator for DROP INDEX. iter-22.
type DropIndex struct {
	Stmt    *PS.DropIndexStmt
	done    bool
	rowsAff int64
}

func NewDropIndex(stmt *PS.DropIndexStmt) *DropIndex {
	return &DropIndex{Stmt: stmt}
}

func (d *DropIndex) Next(ctx context.Context) (DT.Row, error) {
	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true

	// Check if index exists before modifying.
	DT.StoreMu.Lock()
	indexFound := false
	for _, idxs := range DT.RegisteredIndexes {
		for _, idx := range idxs {
			if idx.Name == d.Stmt.Name {
				indexFound = true
				break
			}
		}
		if indexFound {
			break
		}
	}
	if !indexFound && !d.Stmt.IfExists {
		DT.StoreMu.Unlock()
		return DT.Row{}, fmt.Errorf("ex: no such index: %s", d.Stmt.Name)
	}

	// Remove from DT.RegisteredIndexes.
	for table, idxs := range DT.RegisteredIndexes {
		filtered := idxs[:0]
		for _, idx := range idxs {
			if idx.Name != d.Stmt.Name {
				filtered = append(filtered, idx)
			}
		}
		if len(filtered) == 0 {
			delete(DT.RegisteredIndexes, table)
		} else {
			DT.RegisteredIndexes[table] = filtered
		}
	}
	snapshot := make([]uint64, 0, len(DT.TableIDs))
	for _, tid := range DT.TableIDs {
		snapshot = append(snapshot, tid)
	}
	DT.StoreMu.Unlock()
	// Remove from catalog
	if cat := DT.Catalog(); cat != nil {
		for _, tableID := range snapshot {
			if err := cat.DeleteIndex(tableID, d.Stmt.Name); err == nil {
				break
			}
		}
	}
	d.rowsAff = 0
	return DT.Row{}, DT.ErrNoRows
}

func (d *DropIndex) Close() error        { return nil }
func (d *DropIndex) RowsAffected() int64 { return d.rowsAff }
func joinStrings(s []string, sep string) string {
	if len(s) == 0 {
		return ""
	}
	out := s[0]
	for i := 1; i < len(s); i++ {
		out += sep + s[i]
	}
	return out
}

// sprintWhere serializes a partial index WHERE expression to SQL text
// for storage in RegisteredIndex.Predicate. REQ001386.
func sprintWhere(e PS.Expr) string {
	if e == nil {
		return ""
	}
	switch x := e.(type) {
	case *PS.BinaryExpr:
		op := ""
		switch x.Op {
		case LX.T_EQ:
			op = "="
		case LX.T_NE:
			op = "!="
		case LX.T_LT:
			op = "<"
		case LX.T_GT:
			op = ">"
		case LX.T_LE:
			op = "<="
		case LX.T_GE:
			op = ">="
		case LX.T_AND:
			op = "AND"
		case LX.T_OR:
			op = "OR"
		case LX.T_IS:
			op = "IS"
		case LX.T_NOT:
			op = "NOT"
		case LX.T_IN:
			op = "IN"
		case LX.T_LIKE:
			op = "LIKE"
		default:
			return "(" + sprintWhere(x.Left) + " " + fmt.Sprintf("%v", x.Op) + " " + sprintWhere(x.Right) + ")"
		}
		return "(" + sprintWhere(x.Left) + " " + op + " " + sprintWhere(x.Right) + ")"
	case *PS.UnaryExpr:
		return fmt.Sprintf("%v", x.Op) + " " + sprintWhere(x.Operand)
	case *PS.Ident:
		return x.Name
	case *PS.NumberLiteral:
		return strconv.FormatInt(x.Val, 10)
	case *PS.FloatLiteral:
		return strconv.FormatFloat(x.Val, 'g', -1, 64)
	case *PS.StringLiteral:
		return "'" + x.Val + "'"
	case *PS.BoolLiteral:
		if x.Val {
			return "1"
		}
		return "0"
	case *PS.NullLiteral:
		return "NULL"
	case *PS.Param:
		return "?"
	default:
		return fmt.Sprintf("(%v)", e)
	}
}
