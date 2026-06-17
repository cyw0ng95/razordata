package EX

import (
	"context"
	"fmt"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	LX "github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

type Insert struct {
	table          string
	cols           []string
	values         [][]PS.Expr
	returning      []PS.Expr
	onConflict     *PS.OnConflict
	conflictAction PS.ConflictAction
	store          Store
	schema         *storeSchema
	txWriter       TxWriter
	rows           int64
	done           bool
	params         []interface{}
	resultRows     []Row
	resultPos      int
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (i *Insert) WithParams(p []interface{}) Operator {
	i.params = p
	return i
}

func NewInsert(table string, cols []string, values [][]PS.Expr, returning []PS.Expr, onConflict *PS.OnConflict) *Insert {
	return &Insert{
		table:      table,
		cols:       cols,
		values:     values,
		returning:  returning,
		onConflict: onConflict,
	}
}

// NewInsertWithStore builds an Insert that writes through the engine. The
// table must have been registered. REQ000367: tables without a declared
// PRIMARY KEY get a synthetic int64 rowid and remain writable.
func NewInsertWithStore(store Store, table string, cols []string, values [][]PS.Expr, returning []PS.Expr, onConflict *PS.OnConflict) (*Insert, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &Insert{
		table:      table,
		cols:       cols,
		values:     values,
		returning:  returning,
		onConflict: onConflict,
		store:      store,
		schema:     ss,
	}, nil
}

func (i *Insert) Next(ctx context.Context) (Row, error) {
	// If we have RETURNING results, return them
	if len(i.resultRows) > 0 {
		if i.resultPos < len(i.resultRows) {
			row := i.resultRows[i.resultPos]
			i.resultPos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}

	if i.done {
		return Row{}, ErrNoRows
	}
	i.done = true
	if i.store != nil {
		return i.nextFromStore(ctx)
	}
	schema := Schema(i.table)
	if schema == nil && len(i.cols) > 0 {
		schema = i.cols
	}
	// Resolve the constraint-aware schema for NOT NULL / DEFAULT
	// enforcement. Falls back to nil for ad-hoc schemas.
	var cschema *storeSchema
	if ss, ok := schemaFor(i.table); ok {
		cschema = ss
	}
	tablesMu.Lock()
	defer tablesMu.Unlock()
	existing := tables[i.table]
	pending := make(map[string]struct{}, len(i.values))
	lookup := inMemoryLookup(i.table)
	for _, row := range i.values {
		out, err := buildInsertRow(schema, i.cols, row, i.params)
		if err != nil {
			return Row{}, err
		}
		if cschema != nil {
			if out, err = fillDefaults(cschema, out); err != nil {
				return Row{}, err
			}
			if err := validateRow(cschema, out); err != nil {
				return Row{}, err
			}
			if err := validateCheck(cschema, out); err != nil {
				return Row{}, err
			}
			if err := checkUnique(cschema, out, pending, Row{}, asUniqueLookup(lookup)); err != nil {
				if i.conflictAction == PS.ConflictActionReplace {
					existing = removeConflicting(existing, cschema, out)
					pending = make(map[string]struct{}, len(i.values))
					goto doInsertReplace
				}
				if i.conflictAction == PS.ConflictActionIgnore {
					continue
				}
				if i.onConflict == nil {
					return Row{}, err
				}
				// ON CONFLICT: handle unique violation. REQ000511.
				if i.onConflict.DoNothing {
					// DO NOTHING: skip this row
					continue
				}
				// DO UPDATE: locate the conflicting row and apply
				// the SET clauses. We re-use the lookup closure
				// to find the existing row and mutate it in place.
				if apply, ok := lookup.(uniqueLookupWithApply); ok {
					if err := applyConflictUpdate(cschema, existing, out, i.onConflict.SetClauses, i.params, apply); err != nil {
						return Row{}, err
					}
					// Note: we already counted the row's impact in
					// applyConflictUpdate (it updated an existing
					// row in place). Do not append to `existing` and
					// do not increment i.rows — that would create a
					// duplicate.
					continue
				}
				// Fallback: no mutating lookup available — silently
				// skip the row. The in-memory uniqueLookup does
				// support the apply path above, so this branch is
				// only hit in degenerate cases.
				continue
			}
		doInsertReplace:
		}
		// REPLACE handling for in-memory path without constraint enforcement
		if i.conflictAction == PS.ConflictActionReplace && cschema == nil {
			pkName := tablePKs[i.table]
			existing = removeConflictingInMemory(existing, schema, pkName, out)
			pending = make(map[string]struct{}, len(i.values))
		}
		// REQ000126: FK validation on INSERT
		if cschema != nil && len(cschema.foreignKeys) > 0 {
			if err := validateForeignKeyInsert(cschema, out.Data, i.store); err != nil {
				return Row{}, err
			}
		}
		existing = append(existing, out)
		i.rows++

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(i.returning) > 0 {
			expanded := expandReturningStar(i.returning, out.Cols)
			resultRow := Row{
				Cols:  make([]string, len(expanded)),
				Types: make([]int, len(expanded)),
				Data:  make([]interface{}, len(expanded)),
			}
			for j, expr := range expanded {
				val, err := Eval(expr, &out, i.params)
				if err != nil {
					return Row{}, err
				}
				resultRow.Cols[j] = colNameForReturning(expr, out.Cols, j)
				resultRow.Data[j] = val
			}
			i.resultRows = append(i.resultRows, resultRow)
		}
	}
	tables[i.table] = existing

	// Return first RETURNING result if any
	if len(i.resultRows) > 0 {
		row := i.resultRows[0]
		i.resultPos = 1
		return row, nil
	}
	return Row{}, ErrNoRows
}

func (i *Insert) nextFromStore(ctx context.Context) (Row, error) {
	// If we have RETURNING results, return them
	if len(i.resultRows) > 0 {
		if i.resultPos < len(i.resultRows) {
			row := i.resultRows[i.resultPos]
			i.resultPos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}

	prefix := tablePrefix(i.table)
	pending := make(map[string]struct{}, len(i.values))
	// In the engine path, unique lookups are best-effort: the LSM
	// iterator would need a composite-key range scan. For v1, we
	// check pending-batch duplicates only and skip the in-store
	// lookup (correctness note: true cross-row UNIQUE in the engine
	// path is deferred until REQ000045 / index work).
	noopLookup := func(cols []int, vals []interface{}) (bool, error) { return false, nil }
	for _, row := range i.values {
		out, err := buildInsertRow(i.schema.cols, i.cols, row, i.params)
		if err != nil {
			return Row{}, err
		}
		if out, err = fillDefaults(i.schema, out); err != nil {
			return Row{}, err
		}
		if err := validateRow(i.schema, out); err != nil {
			return Row{}, err
		}
		if err := validateCheck(i.schema, out); err != nil {
			return Row{}, err
		}
		if err := checkUnique(i.schema, out, pending, Row{}, noopLookup); err != nil {
			return Row{}, err
		}
		// REQ000126: FK validation on INSERT (store path)
		if len(i.schema.foreignKeys) > 0 {
			if err := validateForeignKeyInsert(i.schema, out.Data, i.store); err != nil {
				return Row{}, err
			}
		}
		pk, err := extractPK(i.schema, out)
		if err != nil {
			return Row{}, err
		}
		buf, err := encodeRow(i.schema, out)
		if err != nil {
			return Row{}, err
		}
		key := rowKey(prefix, pk)
		if err := i.store.Insert(key, buf); err != nil {
			return Row{}, err
		}
		if i.txWriter != nil {
			i.txWriter.RecordWrite(key, buf)
		}
		// Maintain secondary indexes (iter-22).
		if err := maintainIndexesOnInsert(i.store, i.table, i.schema, out); err != nil {
			return Row{}, err
		}
		i.rows++

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(i.returning) > 0 {
			expanded := expandReturningStar(i.returning, out.Cols)
			resultRow := Row{
				Cols:  make([]string, len(expanded)),
				Types: make([]int, len(expanded)),
				Data:  make([]interface{}, len(expanded)),
			}
			for j, expr := range expanded {
				val, err := Eval(expr, &out, i.params)
				if err != nil {
					return Row{}, err
				}
				resultRow.Cols[j] = colNameForReturning(expr, out.Cols, j)
				resultRow.Data[j] = val
			}
			i.resultRows = append(i.resultRows, resultRow)
		}
	}
	_ = ctx

	// Return first RETURNING result if any
	if len(i.resultRows) > 0 {
		row := i.resultRows[0]
		i.resultPos = 1
		return row, nil
	}
	return Row{}, ErrNoRows
}

func (i *Insert) Close() error {
	return nil
}

func (i *Insert) RowsAffected() int64 {
	return i.rows
}

type Update struct {
	table      string
	set        []PS.Pair
	where      PS.Expr
	returning  []PS.Expr
	iter       Operator
	store      Store
	schema     *storeSchema
	txWriter   TxWriter
	rows       int64
	done       bool
	params     []interface{}
	resultRows []Row
	resultPos  int
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (u *Update) WithParams(p []interface{}) Operator {
	u.params = p
	if u.iter != nil {
		if w, ok := u.iter.(interface{ WithParams([]interface{}) Operator }); ok {
			w.WithParams(p)
		}
	}
	return u
}

func NewUpdate(table string, set []PS.Pair, where PS.Expr, iter Operator, returning []PS.Expr) *Update {
	return &Update{table: table, set: set, where: where, iter: iter, returning: returning}
}

// NewUpdateWithStore builds an Update that reads the old row via the engine
// iterator and writes the new version through engine.Insert. REQ000367:
// tables without a declared PRIMARY KEY are writable via synthetic rowid.
func NewUpdateWithStore(store Store, table string, set []PS.Pair, where PS.Expr, iter Operator, returning []PS.Expr) (*Update, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &Update{
		table:     table,
		set:       set,
		where:     where,
		iter:      iter,
		returning: returning,
		store:     store,
		schema:    ss,
	}, nil
}

func (u *Update) Next(ctx context.Context) (Row, error) {
	// If we have RETURNING results, return them
	if len(u.resultRows) > 0 {
		if u.resultPos < len(u.resultRows) {
			row := u.resultRows[u.resultPos]
			u.resultPos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}

	if u.done {
		return Row{}, ErrNoRows
	}
	u.done = true
	if u.store != nil {
		return u.nextFromStore(ctx)
	}
	// Resolve the constraint-aware schema for NOT NULL / DEFAULT
	// enforcement on the new row.
	var cschema *storeSchema
	if ss, ok := schemaFor(u.table); ok {
		cschema = ss
	}
	for {
		row, err := u.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if u.where != nil {
			ok, err := Eval(u.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}
		snapshot := cloneRow(row)
		if err := applyUpdate(&row, u.set, u.params); err != nil {
			return Row{}, err
		}
		if cschema != nil {
			if row, err = fillDefaults(cschema, row); err != nil {
				return Row{}, err
			}
			if err := validateRow(cschema, row); err != nil {
				return Row{}, err
			}
			if err := validateCheck(cschema, row); err != nil {
				return Row{}, err
			}
			// REQ000513: FK re-validation when FK columns are updated.
			if err := validateForeignKeyUpdateInMemory(cschema, snapshot.Data, row.Data); err != nil {
				return Row{}, err
			}
			// REQ000516: UNIQUE enforcement on UPDATE. Use a real
			// in-memory lookup instead of noopLookup so that
			// updating a row to a value that collides with another
			// row's UNIQUE key is caught.  The snapshot is passed
			// to checkUnique for self-exclusion.
			tablesMu.RLock()
			ul := inMemoryLookup(u.table).Lookup
			uidErr := checkUnique(cschema, row, nil, snapshot, ul)
			tablesMu.RUnlock()
			if uidErr != nil {
				return Row{}, uidErr
			}
		}
		if err := replaceBySnapshot(u.table, snapshot, row); err != nil {
			return Row{}, err
		}
		u.rows++

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(u.returning) > 0 {
			expanded := expandReturningStar(u.returning, row.Cols)
			resultRow := Row{
				Cols:  make([]string, len(expanded)),
				Types: make([]int, len(expanded)),
				Data:  make([]interface{}, len(expanded)),
			}
			for j, expr := range expanded {
				val, err := Eval(expr, &row, u.params)
				if err != nil {
					return Row{}, err
				}
				resultRow.Cols[j] = colNameForReturning(expr, row.Cols, j)
				resultRow.Data[j] = val
			}
			u.resultRows = append(u.resultRows, resultRow)
		}
	}

	// Return first RETURNING result if any
	if len(u.resultRows) > 0 {
		row := u.resultRows[0]
		u.resultPos = 1
		return row, nil
	}
	return Row{}, ErrNoRows
}

func (u *Update) nextFromStore(ctx context.Context) (Row, error) {
	// If we have RETURNING results, return them
	if len(u.resultRows) > 0 {
		if u.resultPos < len(u.resultRows) {
			row := u.resultRows[u.resultPos]
			u.resultPos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}

	prefix := tablePrefix(u.table)
	for {
		row, err := u.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if u.where != nil {
			ok, err := Eval(u.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}
		oldRow := cloneRow(row)
		if err := applyUpdate(&row, u.set, u.params); err != nil {
			return Row{}, err
		}
		if row, err = fillDefaults(u.schema, row); err != nil {
			return Row{}, err
		}
		if err := validateRow(u.schema, row); err != nil {
			return Row{}, err
		}
		if err := validateCheck(u.schema, row); err != nil {
			return Row{}, err
		}
		// Engine-path unique: best-effort no-op (correct UNIQUE in the
		// engine path requires a real index, deferred to REQ000045).
		noopLookup := func(cols []int, vals []interface{}) (bool, error) { return false, nil }
		if err := checkUnique(u.schema, row, nil, Row{}, noopLookup); err != nil {
			return Row{}, err
		}
		pk, err := extractPK(u.schema, row)
		if err != nil {
			return Row{}, err
		}
		buf, err := encodeRow(u.schema, row)
		if err != nil {
			return Row{}, err
		}
		key := rowKey(prefix, pk)
		if err := u.store.Insert(key, buf); err != nil {
			return Row{}, err
		}
		if u.txWriter != nil {
			u.txWriter.RecordWrite(key, buf)
		}
		if err := maintainIndexesOnUpdate(u.store, u.table, u.schema, oldRow, row); err != nil {
			return Row{}, err
		}
		u.rows++

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(u.returning) > 0 {
			expanded := expandReturningStar(u.returning, row.Cols)
			resultRow := Row{
				Cols:  make([]string, len(expanded)),
				Types: make([]int, len(expanded)),
				Data:  make([]interface{}, len(expanded)),
			}
			for j, expr := range expanded {
				val, err := Eval(expr, &row, u.params)
				if err != nil {
					return Row{}, err
				}
				resultRow.Cols[j] = colNameForReturning(expr, row.Cols, j)
				resultRow.Data[j] = val
			}
			u.resultRows = append(u.resultRows, resultRow)
		}
	}

	// Return first RETURNING result if any
	if len(u.resultRows) > 0 {
		row := u.resultRows[0]
		u.resultPos = 1
		return row, nil
	}
	return Row{}, ErrNoRows
}

func (u *Update) Close() error {
	return u.iter.Close()
}

func (u *Update) RowsAffected() int64 {
	return u.rows
}

type Delete struct {
	table      string
	where      PS.Expr
	returning  []PS.Expr
	iter       Operator
	store      Store
	schema     *storeSchema
	txWriter   TxWriter
	rows       int64
	done       bool
	params     []interface{}
	resultRows []Row
	resultPos  int
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (d *Delete) WithParams(p []interface{}) Operator {
	d.params = p
	if d.iter != nil {
		if w, ok := d.iter.(interface{ WithParams([]interface{}) Operator }); ok {
			w.WithParams(p)
		}
	}
	return d
}

func NewDelete(table string, where PS.Expr, iter Operator, returning []PS.Expr) *Delete {
	return &Delete{table: table, where: where, iter: iter, returning: returning}
}

// NewDeleteWithStore builds a Delete that removes rows through engine.Delete.
// REQ000367: tables without a declared PRIMARY KEY are deletable via
// the synthetic rowid.
func NewDeleteWithStore(store Store, table string, where PS.Expr, iter Operator, returning []PS.Expr) (*Delete, error) {
	ss, ok := schemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTableNotRegisteredForStorage, table)
	}
	return &Delete{
		table:     table,
		where:     where,
		iter:      iter,
		returning: returning,
		store:     store,
		schema:    ss,
	}, nil
}

func (d *Delete) Next(ctx context.Context) (Row, error) {
	// If we have RETURNING results, return them
	if len(d.resultRows) > 0 {
		if d.resultPos < len(d.resultRows) {
			row := d.resultRows[d.resultPos]
			d.resultPos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}

	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true
	if d.store != nil {
		return d.nextFromStore(ctx)
	}
	// REQ000514: resolve the schema for FK validation, then
	// collect to-be-deleted row indices and run FK checks.
	var dschema *storeSchema
	if ss, ok := schemaFor(d.table); ok {
		dschema = ss
	}
	toDelete := map[int]bool{}
	var fkRows [][]interface{}
	for {
		row, err := d.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if d.where != nil {
			ok, err := Eval(d.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}
		idx, ok := rowIndex(d.table, row)
		if ok {
			toDelete[idx] = true
			fkRows = append(fkRows, append([]interface{}(nil), row.Data...))

			// Evaluate RETURNING expressions before deleting (REQ000518: expand *)
			if len(d.returning) > 0 {
				expanded := expandReturningStar(d.returning, row.Cols)
				resultRow := Row{
					Cols:  make([]string, len(expanded)),
					Types: make([]int, len(expanded)),
					Data:  make([]interface{}, len(expanded)),
				}
				for j, expr := range expanded {
					val, err := Eval(expr, &row, d.params)
					if err != nil {
						return Row{}, err
					}
					resultRow.Cols[j] = colNameForReturning(expr, row.Cols, j)
					resultRow.Data[j] = val
				}
				d.resultRows = append(d.resultRows, resultRow)
			}
		}
	}
	if len(toDelete) > 0 {
		// REQ000514: FK checks must run before mutating the table.
		if dschema != nil {
			for _, rowData := range fkRows {
				if err := validateForeignKeyDeleteInMemory(d.table, rowData, dschema); err != nil {
					return Row{}, err
				}
			}
		}
		tablesMu.Lock()
		defer tablesMu.Unlock()
		existing := tables[d.table]
		out := existing[:0]
		for i, r := range existing {
			if !toDelete[i] {
				out = append(out, r)
			}
		}
		tables[d.table] = out
		d.rows = int64(len(toDelete))
	}

	// Return first RETURNING result if any
	if len(d.resultRows) > 0 {
		row := d.resultRows[0]
		d.resultPos = 1
		return row, nil
	}
	return Row{}, ErrNoRows
}

func (d *Delete) nextFromStore(ctx context.Context) (Row, error) {
	// If we have RETURNING results, return them
	if len(d.resultRows) > 0 {
		if d.resultPos < len(d.resultRows) {
			row := d.resultRows[d.resultPos]
			d.resultPos++
			return row, nil
		}
		return Row{}, ErrNoRows
	}

	prefix := tablePrefix(d.table)
	for {
		row, err := d.iter.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		if d.where != nil {
			ok, err := Eval(d.where, &row, nil)
			if err != nil {
				return Row{}, err
			}
			if !truthy(ok) {
				continue
			}
		}

		// Evaluate RETURNING expressions before deleting (REQ000518: expand *)
		if len(d.returning) > 0 {
			expanded := expandReturningStar(d.returning, row.Cols)
			resultRow := Row{
				Cols:  make([]string, len(expanded)),
				Types: make([]int, len(expanded)),
				Data:  make([]interface{}, len(expanded)),
			}
			for j, expr := range expanded {
				val, err := Eval(expr, &row, d.params)
				if err != nil {
					return Row{}, err
				}
				resultRow.Cols[j] = colNameForReturning(expr, row.Cols, j)
				resultRow.Data[j] = val
			}
			d.resultRows = append(d.resultRows, resultRow)
		}

		pk, err := extractPK(d.schema, row)
		if err != nil {
			return Row{}, err
		}
		key := rowKey(prefix, pk)
		if err := d.store.Delete(key); err != nil {
			return Row{}, err
		}
		if d.txWriter != nil {
			d.txWriter.RecordWrite(key, nil)
		}
		d.rows++
	}

	// Return first RETURNING result if any
	if len(d.resultRows) > 0 {
		row := d.resultRows[0]
		d.resultPos = 1
		return row, nil
	}
	return Row{}, ErrNoRows
}

func (d *Delete) Close() error {
	return d.iter.Close()
}

func (d *Delete) RowsAffected() int64 {
	return d.rows
}

// Trigger is a stub operator for CREATE TRIGGER. REQ000435.
// The body is parsed and stored; executor surface is a no-op that
// returns ErrNoRows after one iteration (similar to AlterTable).
// The trigger is registered in the package-level trigger registry
// so future INSERT/UPDATE/DELETE statements can fire it.
type Trigger struct {
	stmt *PS.TriggerStmt
	done bool
}

func NewTrigger(stmt *PS.TriggerStmt) *Trigger {
	if stmt != nil {
		registerTrigger(stmt)
	}
	return &Trigger{stmt: stmt}
}

func (t *Trigger) Next(ctx context.Context) (Row, error) {
	if t.done {
		return Row{}, ErrNoRows
	}
	t.done = true
	return Row{}, ErrNoRows
}

func (t *Trigger) Close() error                        { return nil }
func (t *Trigger) WithParams(p []interface{}) Operator { return t }
func (t *Trigger) RowsAffected() int64                 { return 0 }

type CreateTable struct {
	stmt       *PS.CreateTable
	done       bool
	selectPlan Operator // non-nil for CREATE TABLE AS SELECT (REQ000520)
}

func NewCreateTable(stmt *PS.CreateTable) *CreateTable {
	return &CreateTable{stmt: stmt}
}

// NewCreateTableAs builds a CREATE TABLE AS SELECT operator. The
// selectPlan is the planned SELECT tree that produces the rows to
// insert into the new table. REQ000520.
func NewCreateTableAs(stmt *PS.CreateTable, selectPlan Operator) *CreateTable {
	return &CreateTable{stmt: stmt, selectPlan: selectPlan}
}

func (c *CreateTable) Next(ctx context.Context) (Row, error) {
	if c.done {
		return Row{}, ErrNoRows
	}
	c.done = true

	// CREATE TABLE AS SELECT (REQ000520): the schema comes from
	// the SELECT output. Register the table, run the SELECT, and
	// insert rows.
	if c.stmt.Select != nil && c.selectPlan != nil {
		return c.nextAsSelect(ctx)
	}

	tablesMu.Lock()
	if _, ok := tables[c.stmt.Name]; ok {
		tablesMu.Unlock()
		return Row{}, errTableExists
	}
	cols := make([]string, len(c.stmt.Cols))
	nullable := make([]bool, len(c.stmt.Cols))
	defaults := make([]PS.Expr, len(c.stmt.Cols))
	colTypes := make([]int, len(c.stmt.Cols))
	for i, col := range c.stmt.Cols {
		cols[i] = col.Name
		nullable[i] = col.Nullable
		defaults[i] = col.Default
		colTypes[i] = col.Type
	}
	tables[c.stmt.Name] = []Row{}
	schemas[c.stmt.Name] = cols
	tablesMu.Unlock()
	var pk string
	if c.stmt.PK != nil {
		pk = *c.stmt.PK
	}
	// PRIMARY KEY implies NOT NULL. If PK is one of the cols, flip its
	// nullable bit so validateRow rejects NULL PK inserts.
	if pk != "" {
		for i, n := range cols {
			if n == pk {
				nullable[i] = false
			}
		}
	}
	// Build unique constraints: column-level ColDef.Unique + table-level
	// UniqueConstraints from the AST. Resolve names to indices.
	var unique []UniqueKey
	colIndex := make(map[string]int, len(cols))
	for i, n := range cols {
		colIndex[n] = i
	}
	for _, col := range c.stmt.Cols {
		if col.Unique {
			if idx, ok := colIndex[col.Name]; ok {
				unique = append(unique, UniqueKey{Cols: []int{idx}})
			}
		}
	}
	for _, uk := range c.stmt.UniqueConstraints {
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
			unique = append(unique, UniqueKey{Cols: idxs})
		}
	}
	// REQ000126: extract FK constraints from column-level and table-level
	var fks []ForeignKeyConstraint
	for _, col := range c.stmt.Cols {
		if col.ReferencesTable != "" {
			fk := ForeignKeyConstraint{
				Columns:    []string{col.Name},
				RefTable:   col.ReferencesTable,
				RefColumns: []string{col.ReferencesColumn},
				OnDelete:   col.OnDelete,
				OnUpdate:   col.OnUpdate,
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
	for _, fkAST := range c.stmt.ForeignKeys {
		fk := ForeignKeyConstraint{
			Columns:    fkAST.Columns,
			RefTable:   fkAST.RefTable,
			RefColumns: fkAST.RefColumns,
			OnDelete:   fkAST.OnDelete,
			OnUpdate:   fkAST.OnUpdate,
		}
		if fk.OnDelete == "" {
			fk.OnDelete = "NO ACTION"
		}
		if fk.OnUpdate == "" {
			fk.OnUpdate = "NO ACTION"
		}
		fks = append(fks, fk)
	}
	// REQ000248/249: capture generated column expressions so the
	// INSERT/UPDATE path can materialize them.
	generated := make([]PS.Expr, len(c.stmt.Cols))
	for i, col := range c.stmt.Cols {
		if !col.Virtual && col.Generated != nil {
			generated[i] = col.Generated
		}
	}

	// REQ000484: capture CHECK constraint expressions so
	// validateCheck can enforce them on INSERT/UPDATE.
	checks := make([]PS.Expr, 0, len(c.stmt.Cols))
	for _, col := range c.stmt.Cols {
		checks = append(checks, col.Check)
	}

	id := registerStoreSchemaWithFK(c.stmt.Name, cols, nullable, defaults, unique, pk, fks)
	// R16-3: record each column's SQL type token alongside the
	// schema so ExtractParamTypes can resolve `column = ?`
	// placeholders to their column type at Prepare time.
	storeMu.Lock()
	if ss, ok := storeSchemas[id]; ok {
		ss.colTypes = append([]int(nil), colTypes...)
		ss.generated = generated
		ss.checks = append([]PS.Expr(nil), checks...)
		// REQ000367: tables without a PRIMARY KEY that are
		// registered for storage get a synthetic int64 rowid.
		// This makes them writable to the engine store while
		// keeping the user-visible schema unchanged.
		if pk == "" {
			ss.hiddenPK = true
		}
	}
	storeMu.Unlock()

	// Persist to the system catalog if one is wired in (iter-12).
	// The catalog write is best-effort: a failure does not roll
	// back the in-memory registration because the user-visible
	// operation has already succeeded. A subsequent Open will
	// re-replay the catalog and re-register the schema.
	if cat := Catalog(); cat != nil {
		catCols := make([]ls.CatalogColumn, len(cols))
		for i, n := range cols {
			catCols[i] = ls.CatalogColumn{Name: n, Type: colTypes[i], Nullable: nullable[i]}
		}
		catUnique := make([]ls.CatalogUnique, len(unique))
		for i, u := range unique {
			catUnique[i] = ls.CatalogUnique{Cols: append([]int(nil), u.Cols...)}
		}
		id, _ := tableIDFor(c.stmt.Name)
		if id == 0 {
			id, _ = cat.NextID()
		}
		_ = cat.Put(ls.CatalogEntry{
			TableID:    id,
			Name:       c.stmt.Name,
			Columns:    catCols,
			PrimaryKey: pk,
			Unique:     catUnique,
			CreateSQL:  buildCreateSQL(c.stmt),
		})
	}
	return Row{}, ErrNoRows
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
func (c *CreateTable) nextAsSelect(ctx context.Context) (Row, error) {
	// Read first row to discover schema.
	firstRow, err := c.selectPlan.Next(ctx)
	if err != nil {
		if err == ErrNoRows {
			// Empty SELECT: register table with no columns.
			tablesMu.Lock()
			tables[c.stmt.Name] = []Row{}
			schemas[c.stmt.Name] = nil
			tablesMu.Unlock()
			return Row{}, ErrNoRows
		}
		return Row{}, err
	}
	cols := append([]string(nil), firstRow.Cols...)
	tablesMu.Lock()
	if _, ok := tables[c.stmt.Name]; ok {
		tablesMu.Unlock()
		return Row{}, errTableExists
	}
	tables[c.stmt.Name] = []Row{firstRow}
	schemas[c.stmt.Name] = cols
	tablesMu.Unlock()
	// Drain remaining rows.
	for {
		row, err := c.selectPlan.Next(ctx)
		if err != nil {
			if err == ErrNoRows {
				break
			}
			return Row{}, err
		}
		tablesMu.Lock()
		tables[c.stmt.Name] = append(tables[c.stmt.Name], row)
		tablesMu.Unlock()
	}
	return Row{}, ErrNoRows
}

type DropTable struct {
	stmt *PS.DropTable
	done bool
	rows int64
}

func NewDropTable(stmt *PS.DropTable) *DropTable {
	return &DropTable{stmt: stmt}
}

func (d *DropTable) Next(ctx context.Context) (Row, error) {
	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true
	tablesMu.Lock()
	if existing, ok := tables[d.stmt.Name]; ok {
		d.rows = int64(len(existing))
		delete(tables, d.stmt.Name)
	}
	tablesMu.Unlock()
	// Drop the store schema mapping; actual data is left in the engine and
	// unreachable until the same tableID is reused.
	storeMu.Lock()
	id, ok := tableIDs[d.stmt.Name]
	if ok {
		delete(storeSchemas, id)
		delete(tableIDs, d.stmt.Name)
	}
	storeMu.Unlock()
	// Persist the drop to the system catalog (iter-12). The
	// catalog write is best-effort; a failure leaves the
	// in-memory state already gone, so the table is no longer
	// queryable in this process.
	if ok {
		if cat := Catalog(); cat != nil {
			_ = cat.Delete(id)
		}
	}
	return Row{}, ErrNoRows
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
func typeToken(t int) string {
	switch t {
	case int(LX.T_INT_KW):
		return "INTEGER"
	case int(LX.T_BIGINT):
		return "BIGINT"
	case int(LX.T_TEXT):
		return "TEXT"
	case int(LX.T_VARCHAR):
		return "VARCHAR"
	case int(LX.T_BOOL):
		return "BOOLEAN"
	case int(LX.T_FLOAT_KW):
		return "FLOAT"
	case int(LX.T_BLOB):
		return "BLOB"
	case int(LX.T_TIMESTAMP):
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
		return fmt.Sprintf("%d", v.Val)
	case *PS.FloatLiteral:
		return fmt.Sprintf("%v", v.Val)
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
	stmt    *PS.CreateIndexStmt
	done    bool
	rowsAff int64
}

func NewCreateIndex(stmt *PS.CreateIndexStmt) *CreateIndex {
	return &CreateIndex{stmt: stmt}
}

func (c *CreateIndex) Next(ctx context.Context) (Row, error) {
	if c.done {
		return Row{}, ErrNoRows
	}
	c.done = true
	// REQ000479: IF NOT EXISTS — skip if index already exists
	if c.stmt.IfExists {
		exists := false
		storeMu.Lock()
		for _, idxs := range registeredIndexes {
			for _, idx := range idxs {
				if idx.Name == c.stmt.Name {
					exists = true
					break
				}
			}
			if exists {
				break
			}
		}
		storeMu.Unlock()
		if exists {
			return Row{}, ErrNoRows
		}
	}
	// Register for writer maintenance
	RegisterIndexWithID(c.stmt.Table, RegisteredIndex{
		Name:    c.stmt.Name,
		Columns: c.stmt.Columns,
		Unique:  c.stmt.Unique,
	})
	// Persist to catalog if available
	if cat := Catalog(); cat != nil {
		// Find the tableID
		if tableID, ok := tableIDFor(c.stmt.Table); ok {
			idx := ls.CatalogIndex{
				Name:      c.stmt.Name,
				Columns:   c.stmt.Columns,
				Unique:    c.stmt.Unique,
				CreateSQL: "CREATE INDEX " + c.stmt.Name + " ON " + c.stmt.Table + " (" + joinStrings(c.stmt.Columns, ", ") + ")",
			}
			if err := cat.PutIndex(tableID, idx); err != nil {
				// Duplicate or other error — surface it.
				return Row{}, err
			}
		}
	}
	c.rowsAff = 0
	return Row{}, ErrNoRows
}

func (c *CreateIndex) Close() error        { return nil }
func (c *CreateIndex) RowsAffected() int64 { return c.rowsAff }

// DropIndex is the DDL operator for DROP INDEX. iter-22.
type DropIndex struct {
	stmt    *PS.DropIndexStmt
	done    bool
	rowsAff int64
}

func NewDropIndex(stmt *PS.DropIndexStmt) *DropIndex {
	return &DropIndex{stmt: stmt}
}

func (d *DropIndex) Next(ctx context.Context) (Row, error) {
	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true
	// Remove from EX-layer writer registry
	storeMu.Lock()
	for table, idxs := range registeredIndexes {
		filtered := idxs[:0]
		for _, idx := range idxs {
			if idx.Name != d.stmt.Name {
				filtered = append(filtered, idx)
			}
		}
		if len(filtered) == 0 {
			delete(registeredIndexes, table)
		} else {
			registeredIndexes[table] = filtered
		}
	}
	storeMu.Unlock()
	// Remove from catalog
	if cat := Catalog(); cat != nil {
		// Find the table that owns this index
		for _, tableID := range tableIDs {
			_ = tableID
			// Try delete (ignore if not found)
			if err := cat.DeleteIndex(tableID, d.stmt.Name); err == nil {
				break
			}
		}
	}
	d.rowsAff = 0
	return Row{}, ErrNoRows
}

func (d *DropIndex) Close() error        { return nil }
func (d *DropIndex) RowsAffected() int64 { return d.rowsAff }

// Pragma is a writer-op stub for PRAGMA name [= value]. REQ000490.
type Pragma struct {
	stmt *PS.PragmaStmt
	done bool
}

func NewPragma(stmt *PS.PragmaStmt) *Pragma { return &Pragma{stmt: stmt} }

func (p *Pragma) Next(ctx context.Context) (Row, error) {
	if p.done {
		return Row{}, ErrNoRows
	}
	p.done = true
	return Row{}, ErrNoRows
}

func (p *Pragma) Close() error                        { return nil }
func (p *Pragma) WithParams(_ []interface{}) Operator { return p }
func (p *Pragma) RowsAffected() int64                 { return 0 }

// Explain runs the inner plan and returns a textual description of it
// as a single-row result. REQ000481, REQ000500.
type Explain struct {
	stmt   *PS.ExplainStmt
	plan   Operator
	done   bool
	rowOut bool
	desc   string
}

func NewExplain(stmt *PS.ExplainStmt) *Explain { return &Explain{stmt: stmt} }

func (e *Explain) WithPlanner(p Planner) Operator {
	if e.stmt != nil && e.stmt.Inner != nil {
		// The inner statement has already been planned by buildWriterOp or
		// the caller. Stash the planner so the EXPLAIN text can mention
		// the planner name.
		_ = p
	}
	return e
}

func (e *Explain) Next(ctx context.Context) (Row, error) {
	if e.done && e.rowOut {
		return Row{}, ErrNoRows
	}
	if !e.done {
		e.done = true
		e.desc = e.explain()
		return Row{
			Cols:  []string{"plan"},
			Types: []int{0},
			Data:  []interface{}{e.desc},
		}, nil
	}
	e.rowOut = true
	return Row{}, ErrNoRows
}

func (e *Explain) explain() string {
	if e.stmt == nil || e.stmt.Inner == nil {
		return "EXPLAIN: no statement"
	}
	switch s := e.stmt.Inner.(type) {
	case *PS.Select:
		return fmt.Sprintf("EXPLAIN: SELECT from %s", s.From)
	case *PS.Insert:
		return fmt.Sprintf("EXPLAIN: INSERT INTO %s", s.Table)
	case *PS.Update:
		return fmt.Sprintf("EXPLAIN: UPDATE %s", s.Table)
	case *PS.Delete:
		return fmt.Sprintf("EXPLAIN: DELETE FROM %s", s.Table)
	default:
		return fmt.Sprintf("EXPLAIN: %T", e.stmt.Inner)
	}
}

func (e *Explain) Close() error                        { return nil }
func (e *Explain) WithParams(_ []interface{}) Operator { return e }
func (e *Explain) RowsAffected() int64                 { return 0 }

// Truncate is a writer-op stub for TRUNCATE [TABLE] name. REQ000476.
type Truncate struct {
	stmt *PS.TruncateStmt
	done bool
	rows int64
}

func NewTruncate(stmt *PS.TruncateStmt) *Truncate { return &Truncate{stmt: stmt} }

func (t *Truncate) Next(ctx context.Context) (Row, error) {
	if t.done {
		return Row{}, ErrNoRows
	}
	t.done = true
	// Truncate = DELETE without WHERE; reuse the in-memory delete path.
	if Schema(t.stmt.Table) != nil {
		tablesMu.Lock()
		if existing, ok := tables[t.stmt.Table]; ok {
			t.rows = int64(len(existing))
		}
		tables[t.stmt.Table] = nil
		tablesMu.Unlock()
	}
	return Row{}, ErrNoRows
}

func (t *Truncate) Close() error                        { return nil }
func (t *Truncate) WithParams(_ []interface{}) Operator { return t }
func (t *Truncate) RowsAffected() int64                 { return t.rows }

// Reindex is a writer-op stub for REINDEX. REQ000478.
type Reindex struct {
	stmt *PS.ReindexStmt
	done bool
}

func NewReindex(stmt *PS.ReindexStmt) *Reindex { return &Reindex{stmt: stmt} }

func (r *Reindex) Next(ctx context.Context) (Row, error) {
	if r.done {
		return Row{}, ErrNoRows
	}
	r.done = true
	return Row{}, ErrNoRows
}

func (r *Reindex) Close() error                        { return nil }
func (r *Reindex) WithParams(_ []interface{}) Operator { return r }
func (r *Reindex) RowsAffected() int64                 { return 0 }

// DropView is a writer-op for DROP VIEW [IF EXISTS] name. REQ000494.
type DropView struct {
	stmt *PS.DropViewStmt
	done bool
}

func NewDropView(stmt *PS.DropViewStmt) *DropView { return &DropView{stmt: stmt} }

func (d *DropView) Next(ctx context.Context) (Row, error) {
	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true
	if d.stmt == nil {
		return Row{}, ErrNoRows
	}
	UnregisterView(d.stmt.Name)
	return Row{}, ErrNoRows
}

func (d *DropView) Close() error                        { return nil }
func (d *DropView) WithParams(_ []interface{}) Operator { return d }
func (d *DropView) RowsAffected() int64                 { return 0 }

// DropTrigger is a writer-op for DROP TRIGGER [IF EXISTS] name. REQ000496.
type DropTrigger struct {
	stmt *PS.DropTriggerStmt
	done bool
}

func NewDropTrigger(stmt *PS.DropTriggerStmt) *DropTrigger {
	return &DropTrigger{stmt: stmt}
}

func (d *DropTrigger) Next(ctx context.Context) (Row, error) {
	if d.done {
		return Row{}, ErrNoRows
	}
	d.done = true
	if d.stmt == nil {
		return Row{}, ErrNoRows
	}
	unregisterTrigger(d.stmt.Name)
	return Row{}, ErrNoRows
}

func (d *DropTrigger) Close() error                        { return nil }
func (d *DropTrigger) WithParams(_ []interface{}) Operator { return d }
func (d *DropTrigger) RowsAffected() int64                 { return 0 }

// applyConflictUpdate locates the conflicting row by unique-key match
// and applies the SET clauses. Used by INSERT ... ON CONFLICT DO
// UPDATE. REQ000511.
func applyConflictUpdate(schema *storeSchema, existing []Row, out Row, sets []PS.Pair, params []interface{}, apply uniqueLookupWithApply) error {
	if apply == nil {
		return nil
	}
	// Build the lookup key from the PK column (we use PK as the
	// canonical conflict target when OnConflict.Columns is empty,
	// matching the most common SQLite UPSERT pattern).
	idxs, vals, err := conflictKey(schema, out)
	if err != nil {
		return err
	}
	rowIdx, ok, err := apply.FindAndLock(idxs, vals)
	if err != nil {
		return err
	}
	if !ok {
		// No matching row found (race with another writer).
		// Skip silently per UPSERT semantics.
		return nil
	}
	_ = existing
	return apply.Mutate(rowIdx, func(target Row) Row {
		updated := cloneRow(target)
		for _, p := range sets {
			ci := -1
			for i, c := range schema.cols {
				if c == p.Col {
					ci = i
					break
				}
			}
			if ci < 0 {
				continue
			}
			v, err := Eval(p.Val, &out, params)
			if err != nil {
				// Best-effort: leave column unchanged on eval error.
				continue
			}
			if ci < len(updated.Data) {
				updated.Data[ci] = v
			}
		}
		return updated
	})
}

// conflictKey returns the column indices and values used to look up
// a row for ON CONFLICT. If the schema has a PK, that is the conflict
// target. Otherwise the first unique key is used. REQ000511.
func conflictKey(schema *storeSchema, row Row) ([]int, []interface{}, error) {
	if schema.pk != "" {
		for i, c := range schema.cols {
			if c == schema.pk {
				if i >= len(row.Data) {
					return nil, nil, fmt.Errorf("ex: PK column %q out of range", schema.pk)
				}
				return []int{i}, []interface{}{row.Data[i]}, nil
			}
		}
	}
	if len(schema.unique) > 0 {
		uk := schema.unique[0]
		vals := make([]interface{}, len(uk.Cols))
		for i, idx := range uk.Cols {
			if idx < len(row.Data) {
				vals[i] = row.Data[idx]
			}
		}
		return uk.Cols, vals, nil
	}
	return nil, nil, fmt.Errorf("ex: ON CONFLICT requires PK or UNIQUE constraint")
}

// joinStrings is a tiny helper for formatting column lists.
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

// expandReturningStar expands StarExpr entries in the RETURNING list
// into individual column references. REQ000518: RETURNING * returns
// all columns of the inserted/updated/deleted row.
func expandReturningStar(exprs []PS.Expr, colNames []string) []PS.Expr {
	var out []PS.Expr
	for _, e := range exprs {
		if _, ok := e.(*PS.StarExpr); ok {
			for _, name := range colNames {
				out = append(out, &PS.Ident{Name: name})
			}
		} else {
			out = append(out, e)
		}
	}
	return out
}

// colNameForReturning returns the column name for a RETURNING expression.
// For expanded star expressions, it uses the column name. For other
// expressions, it uses the alias or a positional label.
func colNameForReturning(expr PS.Expr, colNames []string, idx int) string {
	if ident, ok := expr.(*PS.Ident); ok {
		return ident.Name
	}
	if idx < len(colNames) {
		return colNames[idx]
	}
	return fmt.Sprintf("col%d", idx)
}
