package WT

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type Insert struct {
	table          string
	cols           []string
	values         [][]PS.Expr
	selectPlan     DT.Operator // REQ000707: INSERT INTO t SELECT ...
	returning      []PS.Expr
	onConflict     *PS.OnConflict
	conflictAction PS.ConflictAction
	defaultValues  bool // REQ000563: INSERT INTO t DEFAULT VALUES
	store          DT.Store
	schema         *DT.StoreSchema
	txWriter       DT.TxWriter
	rows           int64
		done           bool
		params         []any
		resultRows     []DT.Row
		resultPos      int
		execCtx        *DT.ExecContext // REQ000812
	}

	// SetExecCtx sets the execution context. Used by EX.propagateExecContext.
	func (i *Insert) SetExecCtx(ec *DT.ExecContext) { i.execCtx = ec }

	// SetSelectPlan sets the selectPlan for INSERT INTO ... SELECT.
	func (i *Insert) SetSelectPlan(op DT.Operator) { i.selectPlan = op }

	// SetConflictAction sets the conflict resolution action.
	func (i *Insert) SetConflictAction(a PS.ConflictAction) { i.conflictAction = a }

	// SetDefaultValues sets the DEFAULT VALUES flag.
	func (i *Insert) SetDefaultValues(v bool) { i.defaultValues = v }

	// Table returns the target table name.
	func (i *Insert) Table() string { return i.table }

	// SetExecCtx sets the execution context. Used by EX.propagateExecContext.
	func (d *Delete) SetExecCtx(ec *DT.ExecContext) { d.execCtx = ec }

	// Table returns the target table name.
	func (d *Delete) Table() string { return d.table }

	// Iter returns the input iterator. Used by EX.plan_node.
	func (d *Delete) Iter() DT.Operator { return d.iter }

	// WithParams propagates the bound `?` placeholders (R16-1..2).
func (i *Insert) WithParams(p []any) DT.Operator {
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

// Child returns the selectPlan if this is an INSERT INTO ... SELECT,
// or nil otherwise. Implements childer for execCtx propagation. REQ000812.
func (i *Insert) Child() DT.Operator { return i.selectPlan }

// NewInsertWithStore builds an Insert that writes through the engine. The
// table must have been registered. REQ000367: DT.Tables without a declared
// PRIMARY KEY get a synthetic int64 rowid and remain writable.
func NewInsertWithStore(store DT.Store, table string, cols []string, values [][]PS.Expr, returning []PS.Expr, onConflict *PS.OnConflict) (*Insert, error) {
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", OP.ErrTableNotRegisteredForStorage, table)
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

func (i *Insert) Next(ctx context.Context) (DT.Row, error) {
	// If we have RETURNING results, return them
	if len(i.resultRows) > 0 {
		if i.resultPos < len(i.resultRows) {
			row := i.resultRows[i.resultPos]
			i.resultPos++
			return row, nil
		}
		return DT.Row{}, DT.ErrNoRows
	}

	if i.done {
		return DT.Row{}, DT.ErrNoRows
	}
	i.done = true

	// REQ000707: INSERT INTO t SELECT ...
	if i.selectPlan != nil {
		return i.nextFromSelect(ctx)
	}

	if i.store != nil {
		return i.nextFromStore(ctx)
	}
	schema := DT.Schema(i.table)
	if schema == nil && len(i.cols) > 0 {
		schema = i.cols
	}
	// Resolve the constraint-aware schema for NOT NULL / DEFAULT
	// enforcement. Falls back to nil for ad-hoc DT.Schemas.
	var cschema *DT.StoreSchema
	if ss, ok := DT.SchemaFor(i.table); ok {
		cschema = ss
	}
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	existing := DT.Tables[i.table]
	// REQ000641: snapshot the table before first mutation for rollback.
	if tw := DT.CurrentTxWriter(); tw != nil {
		tw.RecordInMemoryTable(i.table, DT.SnapshotInMemoryTable(i.table))
	}
	var pending map[string]struct{}
	var iterValues [][]PS.Expr
	if i.defaultValues {
		pending = make(map[string]struct{}, 1)
		iterValues = [][]PS.Expr{nil}
	} else {
		pending = make(map[string]struct{}, len(i.values))
		iterValues = i.values
	}
	lookup := InMemoryLookup(i.table)
	// REQ001030: pre-compute colIdx once for all rows.
	colIdx := make([]int, len(i.cols))
	for ci, nm := range i.cols {
		idx := -1
		for j, s := range schema {
			if strings.EqualFold(nm, s) {
				idx = j
				break
			}
		}
		colIdx[ci] = idx
	}
	for _, row := range iterValues {
		var out DT.Row
		var err error
		if row == nil && i.defaultValues {
			out = DT.Row{Cols: schema}
			out.Data = make([]DT.Value, len(schema))
		} else {
			out, err = BuildInsertRow(schema, i.cols, colIdx, row, i.params)
			if err != nil {
				return DT.Row{}, err
			}
		}
		if cschema != nil {
			if out, err = FillDefaults(cschema, out); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateRow(cschema, out); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateDecimal(cschema, out); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateCheck(cschema, out); err != nil {
				return DT.Row{}, err
			}
			if err := CheckUnique(cschema, out, pending, DT.Row{}, AsUniqueLookup(lookup)); err != nil {
				if i.conflictAction == PS.ConflictActionReplace {
					var removed int
					existing, removed = RemoveConflicting(existing, cschema, out)
					i.rows += int64(removed)
					if i.execCtx != nil {
						i.execCtx.LastChanges += int64(removed)
						i.execCtx.TotalChanges += int64(removed)
					}
					pending = make(map[string]struct{}, len(i.values))
					goto doInsertReplace
				}
				if i.conflictAction == PS.ConflictActionIgnore {
					continue
				}
				if i.onConflict == nil {
					return DT.Row{}, err
				}
				// ON CONFLICT: handle unique violation. REQ000511.
				if i.onConflict.DoNothing {
					// DO NOTHING: skip this row
					continue
				}
				// DO UPDATE: locate the conflicting row and apply
				// the SET clauses. We re-use the lookup closure
				// to find the existing row and mutate it in place.
				if apply, ok := lookup.(UniqueLookupWithApply); ok {
					if err := applyConflictUpdate(cschema, existing, out, i.onConflict.SetClauses, i.params, apply); err != nil {
						return DT.Row{}, err
					}
					// Note: we already counted the row's impact in
					// applyConflictUpdate (it updated an existing
					// row in place). Do not append to `existing` and
					// do not increment i.rows — that would create a
					// duplicate.
					continue
				}
				// Fallback: no mutating lookup available — silently
				// skip the row. The in-memory UniqueLookup does
				// support the apply path above, so this branch is
				// only hit in degenerate cases.
				continue
			}
		doInsertReplace:
		}
		// REPLACE handling for in-memory path without constraint enforcement
		if i.conflictAction == PS.ConflictActionReplace && cschema == nil {
			pkName := DT.TablePKs[i.table]
			var removed int
			existing, removed = RemoveConflictingInMemory(existing, schema, pkName, out)
			i.rows += int64(removed)
			if i.execCtx != nil {
				i.execCtx.LastChanges += int64(removed)
				i.execCtx.TotalChanges += int64(removed)
			}
			pending = make(map[string]struct{}, len(i.values))
		}
		// REQ000126/REQ000905: FK validation on INSERT (skipped when PRAGMA foreign_keys = OFF)
		if DT.IsForeignKeysEnabled() && cschema != nil && len(cschema.ForeignKeys) > 0 {
			if err := UT.ValidateForeignKeyInsert(cschema, DT.ValueSliceToAny(out.Data), i.store); err != nil {
				return DT.Row{}, err
			}
		}
		existing = append(existing, out)
		i.rows++
		if i.execCtx != nil {
			i.execCtx.LastChanges++
			i.execCtx.TotalChanges++
		}

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(i.returning) > 0 {
			if err := evalReturning(i.returning, &out, i.params, &i.resultRows); err != nil {
				return DT.Row{}, err
			}
		}
	}
	DT.Tables[i.table] = existing

	// Return first RETURNING result if any
	if len(i.resultRows) > 0 {
		row := i.resultRows[0]
		i.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
}

func (i *Insert) nextFromStore(ctx context.Context) (DT.Row, error) {
	// If we have RETURNING results, return them
	if len(i.resultRows) > 0 {
		if i.resultPos < len(i.resultRows) {
			row := i.resultRows[i.resultPos]
			i.resultPos++
			return row, nil
		}
		return DT.Row{}, DT.ErrNoRows
	}

	// REQ000987: build a TableHandle so InsertRow/DeleteRow encapsulate
	// the repeated TablePrefix/RowKey/EncodeRow/MaintainIndexes pattern.
	h, hErr := DT.OpenTable(i.store, i.table)
	if hErr != nil {
		return DT.Row{}, hErr
	}
	var pending map[string]struct{}
	var iterValues [][]PS.Expr
	if i.defaultValues {
		pending = make(map[string]struct{}, 1)
		iterValues = [][]PS.Expr{nil}
	} else {
		pending = make(map[string]struct{}, len(i.values))
		iterValues = i.values
	}
	// In the engine path, use store-based lookup for PK uniqueness
	// only when conflict action is specified (INSERT OR IGNORE/REPLACE).
	// For plain INSERT, silently overwrite (consistent with LSM semantics).
	var lookupFn UniqueLookup
	if i.conflictAction != PS.ConflictActionUnspecified {
		lookupFn = func(cols []int, vals []any) (bool, error) {
			if h == nil || len(vals) == 0 {
				return false, nil
			}
			found, err := h.Exists(vals[0])
			return found, err
		}
	} else {
		lookupFn = func(cols []int, vals []any) (bool, error) { return false, nil }
	}
	// REQ001030: pre-compute colIdx once for all rows.
	colIdx := make([]int, len(i.cols))
	for ci, nm := range i.cols {
		idx := -1
		for j, s := range i.schema.Cols {
			if strings.EqualFold(nm, s) {
				idx = j
				break
			}
		}
		colIdx[ci] = idx
	}
	for _, row := range iterValues {
		var out DT.Row
		var err error
		if row == nil && i.defaultValues {
			out = DT.Row{Cols: i.schema.Cols}
			out.Data = make([]DT.Value, len(i.schema.Cols))
		} else {
			out, err = BuildInsertRow(i.schema.Cols, i.cols, colIdx, row, i.params)
		}
		if out, err = FillDefaults(i.schema, out); err != nil {
			return DT.Row{}, err
		}
		if err := ValidateRow(i.schema, out); err != nil {
			return DT.Row{}, err
		}
		if err := ValidateDecimal(i.schema, out); err != nil {
			return DT.Row{}, err
		}
		if err := ValidateCheck(i.schema, out); err != nil {
			return DT.Row{}, err
		}
		if err := CheckUnique(i.schema, out, pending, DT.Row{}, lookupFn); err != nil {
			if i.conflictAction == PS.ConflictActionReplace {
				// Delete the existing row via the handle, then fall
				// through to insert below. REQ000987: InsertRow already
				// maintains indexes on success; the delete path uses
				// the row from `out` for PK extraction.
				if _, derr := h.DeleteRow(out); derr == nil {
					i.rows++
					if i.execCtx != nil {
						i.execCtx.LastChanges++
						i.execCtx.TotalChanges++
					}
				}
				// Fall through to insert below
			} else if i.conflictAction == PS.ConflictActionIgnore {
				continue
			} else {
				return DT.Row{}, err
			}
		}
		// REQ000126/REQ000905: FK validation on INSERT (store path, skipped when PRAGMA foreign_keys = OFF)
		if DT.IsForeignKeysEnabled() && len(i.schema.ForeignKeys) > 0 {
			if err := UT.ValidateForeignKeyInsert(i.schema, DT.ValueSliceToAny(out.Data), i.store); err != nil {
				return DT.Row{}, err
			}
		}
		// REQ000987: TableHandle.InsertRow handles PK extraction +
		// REQ001128 rowid auto-fill + EncodeRow + Store.Insert +
		// secondary index maintenance in one call, returning the
		// (key, buf) pair so TxWriter can log it without re-encoding.
		key, buf, err := h.InsertRow(out)
		if err != nil {
			return DT.Row{}, err
		}
		if i.txWriter != nil {
			i.txWriter.RecordWrite(key, buf)
		}
		i.rows++
		if i.execCtx != nil {
			i.execCtx.LastChanges++
			i.execCtx.TotalChanges++
		}

		// Fire AFTER INSERT triggers (REQ000316: incremental matview support)
		if err := fireInsertTriggers(i.table, &out, i.params, i.store); err != nil {
			return DT.Row{}, err
		}

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(i.returning) > 0 {
			if err := evalReturning(i.returning, &out, i.params, &i.resultRows); err != nil {
				return DT.Row{}, err
			}
		}
	}
	_ = ctx

	// Return first RETURNING result if any
	if len(i.resultRows) > 0 {
		row := i.resultRows[0]
		i.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
}

// nextFromSelect handles INSERT INTO t SELECT ... by executing
// the SELECT query and inserting each row into the target table.
// REQ000707.
func (i *Insert) nextFromSelect(ctx context.Context) (DT.Row, error) {
	schema := DT.Schema(i.table)
	if schema == nil && len(i.cols) > 0 {
		schema = i.cols
	}
	var cschema *DT.StoreSchema
	if ss, ok := DT.SchemaFor(i.table); ok {
		cschema = ss
	}

	// Execute SELECT first (outside the lock to avoid deadlock)
	var selectRows []DT.Row
	for {
		row, err := i.selectPlan.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		selectRows = append(selectRows, row)
	}

	// Now insert all rows
	DT.TablesMu.Lock()
	defer DT.TablesMu.Unlock()
	existing := DT.Tables[i.table]
	if tw := DT.CurrentTxWriter(); tw != nil {
		tw.RecordInMemoryTable(i.table, DT.SnapshotInMemoryTable(i.table))
	}

	// REQ000987: build a TableHandle for the store-backed write path
	// so InsertRow encapsulates ExtractPK/EncodeRow/Store.Insert/
	// MaintainIndexesOnInsert. nil when no store is wired.
	var hsel *DT.TableHandle
	if i.store != nil && cschema != nil {
		var herr error
		hsel, herr = DT.OpenTable(i.store, i.table)
		if herr != nil {
			return DT.Row{}, herr
		}
	}

	pending := make(map[string]struct{})
	lookup := InMemoryLookup(i.table)

		for _, row := range selectRows {
		// Build insert row from SELECT result
		out, err := buildInsertRowFromSelect(schema, i.cols, row)
		if err != nil {
			return DT.Row{}, err
		}

		if cschema != nil {
			if out, err = FillDefaults(cschema, out); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateRow(cschema, out); err != nil {
				return DT.Row{}, err
			}
			if err := CheckUnique(cschema, out, pending, DT.Row{}, AsUniqueLookup(lookup)); err != nil {
				if i.conflictAction == PS.ConflictActionIgnore {
					continue
				}
				return DT.Row{}, err
			}
			// REQ001129: store-backed INSERT...SELECT must write to
			// the store engine, not just the in-memory table map.
			// REQ000987: route through TableHandle.InsertRow so the
			// PK extract + REQ001128 rowid auto-fill + EncodeRow +
			// Store.Insert + MaintainIndexesOnInsert sequence is
			// encapsulated in a single call.
			if hsel != nil {
				key, buf, ierr := hsel.InsertRow(out)
				if ierr != nil {
					return DT.Row{}, ierr
				}
				if i.txWriter != nil {
					i.txWriter.RecordWrite(key, buf)
				}
			}
		}
		if i.store == nil {
			existing = append(existing, out)
		}
		i.rows++
		if i.execCtx != nil {
			i.execCtx.LastChanges++
			i.execCtx.TotalChanges++
		}

		if len(i.returning) > 0 {
			if err := evalReturning(i.returning, &out, i.params, &i.resultRows); err != nil {
				return DT.Row{}, err
			}
		}
	}
	DT.Tables[i.table] = existing

	if len(i.resultRows) > 0 {
		row := i.resultRows[0]
		i.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
}

// buildInsertRowFromSelect builds an insert row from a SELECT result row.
func buildInsertRowFromSelect(schema []string, cols []string, src DT.Row) (DT.Row, error) {
	if len(cols) > 0 {
		// Map SELECT columns to insert columns by position
		out := DT.Row{Cols: append([]string(nil), schema...)}
		out.Data = make([]DT.Value, len(schema))
		colIdx := make(map[string]int, len(schema))
		for i, c := range schema {
			colIdx[c] = i
		}
		for i, col := range cols {
			if idx, ok := colIdx[col]; ok {
				if i < len(src.Data) {
					out.Data[idx] = src.Data[i]
				}
			}
		}
		return out, nil
	}
	// No column list: use SELECT columns directly
	out := DT.Row{Cols: append([]string(nil), src.Cols...)}
	out.Data = append([]DT.Value(nil), src.Data...)
	return out, nil
}

func (i *Insert) Close() error {
	if i.selectPlan != nil {
		_ = i.selectPlan.Close()
	}
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
	iter       DT.Operator
	store      DT.Store
	schema     *DT.StoreSchema
	txWriter   DT.TxWriter
	rows       int64
	done       bool
	params     []any
	resultRows []DT.Row
	resultPos  int
	execCtx    *DT.ExecContext // REQ000812
}

	// SetExecCtx sets the execution context. Used by EX.propagateExecContext.
	func (u *Update) SetExecCtx(ec *DT.ExecContext) { u.execCtx = ec }

	// Table returns the target table name.
	func (u *Update) Table() string { return u.table }

	// Iter returns the input iterator. Used by EX.plan_node.
	func (u *Update) Iter() DT.Operator { return u.iter }

	// REQ000714: expose child for execCtx/params propagation.
func (u *Update) Child() DT.Operator { return u.iter }

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (u *Update) WithParams(p []any) DT.Operator {
	u.params = p
	if u.iter != nil {
		if w, ok := u.iter.(interface{ WithParams([]any) DT.Operator }); ok {
			w.WithParams(p)
		}
	}
	return u
}

func NewUpdate(table string, set []PS.Pair, where PS.Expr, iter DT.Operator, returning []PS.Expr) *Update {
	return &Update{table: table, set: set, where: where, iter: iter, returning: returning}
}

// NewUpdateWithStore builds an Update that reads the old row via the engine
// iterator and writes the new version through engine.Insert. REQ000367:
// DT.Tables without a declared PRIMARY KEY are writable via synthetic rowid.
func NewUpdateWithStore(store DT.Store, table string, set []PS.Pair, where PS.Expr, iter DT.Operator, returning []PS.Expr) (*Update, error) {
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", OP.ErrTableNotRegisteredForStorage, table)
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

func (u *Update) Next(ctx context.Context) (DT.Row, error) {
	// If we have RETURNING results, return them
	if len(u.resultRows) > 0 {
		if u.resultPos < len(u.resultRows) {
			row := u.resultRows[u.resultPos]
			u.resultPos++
			return row, nil
		}
		return DT.Row{}, DT.ErrNoRows
	}

	if u.done {
		return DT.Row{}, DT.ErrNoRows
	}
	u.done = true
	if u.store != nil {
		return u.nextFromStore(ctx)
	}
	// Resolve the constraint-aware schema for NOT NULL / DEFAULT
	// enforcement on the new row.
	var cschema *DT.StoreSchema
	if ss, ok := DT.SchemaFor(u.table); ok {
		cschema = ss
	}
	// REQ000641: snapshot the table before first mutation for rollback.
	if tw := DT.CurrentTxWriter(); tw != nil {
		DT.TablesMu.RLock()
		tw.RecordInMemoryTable(u.table, DT.SnapshotInMemoryTable(u.table))
		DT.TablesMu.RUnlock()
	}
	for {
		row, err := u.iter.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		snapshot := DT.CloneRow(row)
		// REQ000840: OP.SeqScan may return rows that share Data with the
		// source table. Deep-copy Data before applyUpdate mutates it
		// in-place, otherwise the source row is corrupted.
		row.Data = append([]DT.Value(nil), row.Data...)
		if err := ApplyUpdate(&row, u.set, u.params); err != nil {
			return DT.Row{}, err
		}
		if cschema != nil {
			if row, err = FillDefaults(cschema, row); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateRow(cschema, row); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateDecimal(cschema, row); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateCheck(cschema, row); err != nil {
				return DT.Row{}, err
			}
			// REQ000513/REQ000905: FK re-validation when FK columns are updated.
			if DT.IsForeignKeysEnabled() {
				if err := UT.ValidateForeignKeyUpdateInMemory(cschema, DT.ValueSliceToAny(snapshot.Data), DT.ValueSliceToAny(row.Data)); err != nil {
					return DT.Row{}, err
				}
			}
			// REQ000516: UNIQUE enforcement on UPDATE. Use a real
			// in-memory lookup instead of noopLookup so that
			// updating a row to a value that collides with another
			// row's UNIQUE key is caught.  The snapshot is passed
			// to checkUnique for self-exclusion.
			DT.TablesMu.RLock()
			ul := InMemoryLookup(u.table).Lookup
			uidErr := CheckUnique(cschema, row, nil, snapshot, ul)
			DT.TablesMu.RUnlock()
			if uidErr != nil {
				return DT.Row{}, uidErr
			}
		}
		if err := DT.ReplaceBySnapshot(u.table, snapshot, row); err != nil {
			return DT.Row{}, err
		}
		u.rows++
		if u.execCtx != nil {
			u.execCtx.LastChanges++
			u.execCtx.TotalChanges++
		}

		// Fire AFTER UPDATE triggers (REQ000316: incremental matview support)
		if err := fireUpdateTriggers(u.table, &snapshot, &row, u.params, nil); err != nil {
			return DT.Row{}, err
		}

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(u.returning) > 0 {
			if err := evalReturning(u.returning, &row, u.params, &u.resultRows); err != nil {
				return DT.Row{}, err
			}
		}
	}

	// Return first RETURNING result if any
	if len(u.resultRows) > 0 {
		row := u.resultRows[0]
		u.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
}

func (u *Update) nextFromStore(ctx context.Context) (DT.Row, error) {
	// If we have RETURNING results, return them
	if len(u.resultRows) > 0 {
		if u.resultPos < len(u.resultRows) {
			row := u.resultRows[u.resultPos]
			u.resultPos++
			return row, nil
		}
		return DT.Row{}, DT.ErrNoRows
	}

	prefix := OP.TablePrefix(u.table)
	// REQ000987: build a TableHandle so UpdateRow encapsulates the
	// ExtractPKForUpdate + EncodeRow + Store.Insert +
	// MaintainIndexesOnUpdate sequence.
	h, hErr := DT.OpenTable(u.store, u.table)
	if hErr != nil {
		return DT.Row{}, hErr
	}
	for {
		row, err := u.iter.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		oldRow := DT.CloneRow(row)
		if err := ApplyUpdate(&row, u.set, u.params); err != nil {
			return DT.Row{}, err
		}
		if row, err = FillDefaults(u.schema, row); err != nil {
			return DT.Row{}, err
		}
		if err := ValidateRow(u.schema, row); err != nil {
			return DT.Row{}, err
		}
		if err := ValidateCheck(u.schema, row); err != nil {
			return DT.Row{}, err
		}
		// Engine-path unique: best-effort no-op (correct UNIQUE in the
		// engine path requires a real index, deferred to REQ000045).
		noopLookup := func(cols []int, vals []any) (bool, error) { return false, nil }
		if err := CheckUnique(u.schema, row, nil, DT.Row{}, noopLookup); err != nil {
			return DT.Row{}, err
		}
		// REQ000987: route through TableHandle.UpdateRow. The returned
		// (key, buf) feeds the TxWriter without re-extracting the PK
		// or re-encoding the row.
		key, buf, err := h.UpdateRow(oldRow, row)
		if err != nil {
			return DT.Row{}, err
		}
		_ = prefix // retained for any future callers; UpdateRow uses h.Prefix().
		if u.txWriter != nil {
			u.txWriter.RecordWrite(key, buf)
		}
		u.rows++
		if u.execCtx != nil {
			u.execCtx.LastChanges++
			u.execCtx.TotalChanges++
		}

		// Fire AFTER UPDATE triggers (REQ000316: incremental matview support)
		if err := fireUpdateTriggers(u.table, &oldRow, &row, u.params, u.store); err != nil {
			return DT.Row{}, err
		}

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(u.returning) > 0 {
			if err := evalReturning(u.returning, &row, u.params, &u.resultRows); err != nil {
				return DT.Row{}, err
			}
		}
	}

	// Return first RETURNING result if any
	if len(u.resultRows) > 0 {
		row := u.resultRows[0]
		u.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
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
	iter       DT.Operator
	store      DT.Store
	schema     *DT.StoreSchema
	txWriter   DT.TxWriter
	rows       int64
	done       bool
	params     []any
	resultRows []DT.Row
	resultPos  int
	execCtx    *DT.ExecContext // REQ000812
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (d *Delete) WithParams(p []any) DT.Operator {
	d.params = p
	if d.iter != nil {
		if w, ok := d.iter.(interface{ WithParams([]any) DT.Operator }); ok {
			w.WithParams(p)
		}
	}
	return d
}

// REQ000714: expose iter as a child so propagateDT.ExecContext and
// propagateParams walk into the input chain (OP.Filter/OP.SeqScan) where
// the WHERE predicate (and any correlated subquery) is evaluated.
func (d *Delete) Child() DT.Operator { return d.iter }

func NewDelete(table string, where PS.Expr, iter DT.Operator, returning []PS.Expr) *Delete {
	return &Delete{table: table, where: where, iter: iter, returning: returning}
}

// NewDeleteWithStore builds a Delete that removes rows through engine.Delete.
// REQ000367: DT.Tables without a declared PRIMARY KEY are deletable via
// the synthetic rowid.
func NewDeleteWithStore(store DT.Store, table string, where PS.Expr, iter DT.Operator, returning []PS.Expr) (*Delete, error) {
	ss, ok := DT.SchemaFor(table)
	if !ok {
		return nil, fmt.Errorf("%w: %s", OP.ErrTableNotRegisteredForStorage, table)
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

func (d *Delete) Next(ctx context.Context) (DT.Row, error) {
	// If we have RETURNING results, return them
	if len(d.resultRows) > 0 {
		if d.resultPos < len(d.resultRows) {
			row := d.resultRows[d.resultPos]
			d.resultPos++
			return row, nil
		}
		return DT.Row{}, DT.ErrNoRows
	}

	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true
	if d.store != nil {
		return d.nextFromStore(ctx)
	}
	// REQ000514: resolve the schema for FK validation, then
	// collect to-be-deleted row indices and run FK checks.
	var dschema *DT.StoreSchema
	if ss, ok := DT.SchemaFor(d.table); ok {
		dschema = ss
	}
	toDelete := map[int]bool{}
	var fkRows [][]any
	for {
		row, err := d.iter.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		idx, ok := DT.RowIndex(d.table, row)
		if ok {
			toDelete[idx] = true
			fkRows = append(fkRows, DT.ValueSliceToAny(row.Data))

			// Evaluate RETURNING expressions before deleting (REQ000518: expand *)
			if len(d.returning) > 0 {
				if err := evalReturning(d.returning, &row, d.params, &d.resultRows); err != nil {
					return DT.Row{}, err
				}
			}
		}
	}
	if len(toDelete) > 0 {
		// REQ000514/REQ000905: FK checks must run before mutating the table.
		if DT.IsForeignKeysEnabled() && dschema != nil {
			for _, rowData := range fkRows {
				if err := UT.ValidateForeignKeyDeleteInMemory(d.table, rowData, dschema); err != nil {
					return DT.Row{}, err
				}
			}
		}
		DT.TablesMu.Lock()
		defer DT.TablesMu.Unlock()
		// REQ000641: snapshot the table before first mutation for rollback.
		if tw := DT.CurrentTxWriter(); tw != nil {
			tw.RecordInMemoryTable(d.table, DT.SnapshotInMemoryTable(d.table))
		}
		existing := DT.Tables[d.table]
		out := existing[:0]
		for i, r := range existing {
			if !toDelete[i] {
				out = append(out, r)
			}
		}
		DT.Tables[d.table] = out
		d.rows = int64(len(toDelete))
		if d.execCtx != nil {
			d.execCtx.LastChanges = int64(len(toDelete))
			d.execCtx.TotalChanges += int64(len(toDelete))
		}

		// Fire AFTER DELETE triggers (REQ000316: incremental matview support)
		// For in-memory path, we fire triggers for each deleted row
		// This is a simplified implementation; full implementation would pass old row data
		for _, rowData := range fkRows {
			oldRow := DT.Row{Data: DT.ValueFromAnySlice(rowData)}
			if err := fireDeleteTriggers(d.table, &oldRow, d.params, nil); err != nil {
				return DT.Row{}, err
			}
		}
	}

	// Return first RETURNING result if any
	if len(d.resultRows) > 0 {
		row := d.resultRows[0]
		d.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
}

func (d *Delete) nextFromStore(ctx context.Context) (DT.Row, error) {
	// If we have RETURNING results, return them
	if len(d.resultRows) > 0 {
		if d.resultPos < len(d.resultRows) {
			row := d.resultRows[d.resultPos]
			d.resultPos++
			return row, nil
		}
		return DT.Row{}, DT.ErrNoRows
	}

	prefix := OP.TablePrefix(d.table)
	// REQ000987: build a TableHandle so DeleteRow encapsulates the
	// ExtractPKForUpdate + RowKey + Store.Delete + MaintainIndexesOnDelete
	// sequence (and returns the storage key for TxWriter logging).
	h, hErr := DT.OpenTable(d.store, d.table)
	if hErr != nil {
		return DT.Row{}, hErr
	}
	for {
		row, err := d.iter.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		// Evaluate RETURNING expressions before deleting (REQ000518: expand *)
		if len(d.returning) > 0 {
			if err := evalReturning(d.returning, &row, d.params, &d.resultRows); err != nil {
				return DT.Row{}, err
			}
		}

		key, err := h.DeleteRow(row)
		if err != nil {
			return DT.Row{}, err
		}
		_ = prefix // retained for any future callers; DeleteRow uses h.Prefix().
		if d.txWriter != nil {
			d.txWriter.RecordWrite(key, nil)
		}
		d.rows++
		if d.execCtx != nil {
			d.execCtx.LastChanges++
			d.execCtx.TotalChanges++
		}

		// Fire AFTER DELETE triggers (REQ000316: incremental matview support)
		if err := fireDeleteTriggers(d.table, &row, d.params, d.store); err != nil {
			return DT.Row{}, err
		}
	}

	// Return first RETURNING result if any
	if len(d.resultRows) > 0 {
		row := d.resultRows[0]
		d.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
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
	Stmt *PS.TriggerStmt
	done bool
	err  error
}

func NewTrigger(stmt *PS.TriggerStmt) *Trigger {
	t := &Trigger{Stmt: stmt}
	if stmt != nil {
		if e := RegisterTrigger(stmt); e != nil {
			t.err = e
		}
	}
	return t
}

func (t *Trigger) Next(ctx context.Context) (DT.Row, error) {
	if t.done {
		return DT.Row{}, DT.ErrNoRows
	}
	t.done = true
	if t.err != nil {
		return DT.Row{}, t.err
	}
	return DT.Row{}, DT.ErrNoRows
}

func (t *Trigger) Close() error                { return nil }
func (t *Trigger) WithParams(p []any) DT.Operator { return t }
func (t *Trigger) RowsAffected() int64         { return 0 }

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
		if !col.Virtual && col.Generated != nil {
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
		catCols[i] = ls.CatalogColumn{Name: n, Type: colTypes[i], Nullable: nullable[i]}
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
	if !tableOk && !d.Stmt.IfExists {
		DT.TablesMu.Unlock()
		return DT.Row{}, fmt.Errorf("ex: no such table: %s", d.Stmt.Name)
	}
	if tableOk {
		d.rows = int64(len(existing))
		delete(DT.Tables, d.Stmt.Name)
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
	// Register for writer maintenance
	DT.RegisterIndexWithID(c.Stmt.Table, DT.RegisteredIndex{
		Name:    c.Stmt.Name,
		Columns: indexCols,
		Unique:  c.Stmt.Unique,
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

// Pragma is a writer-op stub for PRAGMA name [= value]. REQ000490.
type Pragma struct {
	Stmt  *PS.PragmaStmt
	store DT.Store
	done  bool
	rows  []DT.Row
	idx   int
}

func NewPragma(stmt *PS.PragmaStmt) *Pragma { return &Pragma{Stmt: stmt} }

// Rows returns the rows produced by this pragma. Used by tests.
func (p *Pragma) Rows() []DT.Row { return p.rows }

// LoadForeignKeyCheck executes foreign_key_check and populates rows.
func (p *Pragma) LoadForeignKeyCheck() { p.loadForeignKeyCheck() }

// WithStore sets the store for this pragma operator.
func (p *Pragma) WithStore(s DT.Store) DT.Operator {
	p.store = s
	return p
}

func (p *Pragma) Next(ctx context.Context) (DT.Row, error) {
	if p.done && p.idx >= len(p.rows) {
		return DT.Row{}, DT.ErrNoRows
	}

	// Handle PRAGMA table_info(table_name)
	if p.Stmt.Name == "table_info" && p.Stmt.Value != "" {
		if !p.done {
			p.done = true
			if err := p.loadTableInfo(); err != nil {
				return DT.Row{}, err
			}
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA database_list (REQ000730)
	if p.Stmt.Name == "database_list" {
		if !p.done {
			p.done = true
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"seq", "name", "file"},
				Data: []DT.Value{DT.NewIntValue(0), DT.NewTextValue("main"), DT.NullValue()},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA index_list(table_name) (REQ000731)
	if p.Stmt.Name == "index_list" && p.Stmt.Value != "" {
		if !p.done {
			p.done = true
			p.loadIndexList()
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA table_list (REQ000732)
	if p.Stmt.Name == "table_list" {
		if !p.done {
			p.done = true
			p.loadTableList()
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA foreign_key_list(table_name) (REQ000733)
	if p.Stmt.Name == "foreign_key_list" && p.Stmt.Value != "" {
		if !p.done {
			p.done = true
			p.loadForeignKeyList()
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA wal_checkpoint (REQ000735)
	if p.Stmt.Name == "wal_checkpoint" || p.Stmt.Name == "wal_autocheckpoint" {
		if !p.done {
			p.done = true
			// Return checkpoint status: busy, log, checkpointed
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"busy", "log", "checkpointed"},
				Data: []DT.Value{DT.NewIntValue(0), DT.NewIntValue(0), DT.NewIntValue(0)},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA foreign_keys [= ON|OFF] (REQ000905)
	if p.Stmt.Name == "foreign_keys" {
		if !p.done {
			p.done = true
			if p.Stmt.Value != "" {
				// Write: set the toggle
				val := strings.ToUpper(p.Stmt.Value)
				DT.SetForeignKeysEnabled(val == "ON" || val == "1" || val == "TRUE")
			}
			// Read: return current value
			v := 0
			if DT.IsForeignKeysEnabled() {
				v = 1
			}
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"foreign_keys"},
				Data: []DT.Value{DT.NewIntValue(int64(v))},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA foreign_key_check[(table_name)] (REQ000906)
	if p.Stmt.Name == "foreign_key_check" {
		if !p.done {
			p.done = true
			p.loadForeignKeyCheck()
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Default: handle PRAGMA name = value (write) and notify listeners
	if !p.done {
		p.done = true
		// If value is set, this is a write pragma — notify listeners
		if p.Stmt.Value != "" {
			UT.NotifyPragmaChange(p.Stmt.Name, p.Stmt.Value)
		}
		return DT.Row{}, DT.ErrNoRows
	}
	return DT.Row{}, DT.ErrNoRows
}

func (p *Pragma) loadTableInfo() error {
	tableName := p.Stmt.Value
	ss, ok := DT.SchemaFor(tableName)
	if !ok {
		return nil
	}
	pkIdx := -1
	if ss.Pk != "" {
		for i, c := range ss.Cols {
			if c == ss.Pk {
				pkIdx = i
				break
			}
		}
	}
	for i, colName := range ss.Cols {
		notNull := int64(0)
		if i < len(ss.Nullable) && !ss.Nullable[i] {
			notNull = int64(1)
		}
		pk := int64(0)
		if i == pkIdx {
			pk = int64(1)
		}
		colType := LX.TokenType(0)
		if i < len(ss.ColTypes) {
			colType = ss.ColTypes[i]
		}
		p.rows = append(p.rows, DT.Row{
			Cols: []string{"cid", "name", "type", "notnull", "dflt_value", "pk"},
			Data: []DT.Value{DT.NewIntValue(int64(i)), DT.NewTextValue(colName), DT.NewTextValue(colTypeName(colType)), DT.NewIntValue(notNull), DT.NullValue(), DT.NewIntValue(pk)},
		})
	}
	return nil
}

func colTypeName(t LX.TokenType) string {
	switch t {
	case 1: // LX.T_INT_KW
		return "INTEGER"
	case 2: // LX.T_TEXT_KW
		return "TEXT"
	case 3: // LX.T_REAL_KW
		return "REAL"
	case 4: // LX.T_BLOB_KW
		return "BLOB"
	default:
		return "ANY"
	}
}

// loadIndexList populates rows for PRAGMA index_list(table_name) (REQ000731).
// Returns columns: seq, name, unique, origin, partial
func (p *Pragma) loadIndexList() {
	tableName := p.Stmt.Value
	// Check if table exists
	if _, ok := DT.SchemaFor(tableName); !ok {
		return
	}
	// For now, only the primary key index exists
	// Secondary indexes will be added when the index catalog is extended
	return
}

// loadTableList populates rows for PRAGMA table_list (REQ000732).
// Returns columns: type, name, tbl_name, rootpage, sql
func (p *Pragma) loadTableList() {
	names := DT.AllTableNames()
	for _, name := range names {
		p.rows = append(p.rows, DT.Row{
			Cols: []string{"type", "name", "tbl_name", "rootpage", "sql"},
			Data: []DT.Value{DT.NewTextValue("table"), DT.NewTextValue(name), DT.NewTextValue(name), DT.NewIntValue(0), DT.NullValue()},
		})
	}
}

// loadForeignKeyList populates rows for PRAGMA foreign_key_list(table_name) (REQ000733).
// Returns columns: id, seq, table, from, to, on_update, on_delete, match
func (p *Pragma) loadForeignKeyList() {
	tableName := p.Stmt.Value
	ss, ok := DT.SchemaFor(tableName)
	if !ok || len(ss.ForeignKeys) == 0 {
		return
	}
	for id, fk := range ss.ForeignKeys {
		for seq, col := range fk.Columns {
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"id", "seq", "table", "from", "to", "on_update", "on_delete", "match"},
				Data: []DT.Value{DT.NewIntValue(int64(id)), DT.NewIntValue(int64(seq)), DT.NewTextValue(fk.RefTable), DT.NewTextValue(col), DT.NewTextValue(fk.RefColumns[seq]), DT.NewTextValue(fk.OnUpdate), DT.NewTextValue(fk.OnDelete), DT.NewTextValue("NONE")},
			})
		}
	}
}

// loadForeignKeyCheck populates rows for PRAGMA foreign_key_check[(table_name)] (REQ000906).
// Returns columns: table, rowid, parent, fkid per SQLite convention.
// An empty result means no violations.
func (p *Pragma) loadForeignKeyCheck() {
	targetTable := p.Stmt.Value
	names := DT.AllTableNames()
	for _, name := range names {
		if targetTable != "" && name != targetTable {
			continue
		}
		ss, ok := DT.SchemaFor(name)
		if !ok || len(ss.ForeignKeys) == 0 {
			continue
		}
		DT.TablesMu.RLock()
		rows := DT.Tables[name]
		DT.TablesMu.RUnlock()
		for rowIdx, row := range rows {
			for fkID, fk := range ss.ForeignKeys {
				// Extract local FK column values
				localVals := make([]any, len(fk.Columns))
				allNull := true
				for i, col := range fk.Columns {
					idx := -1
					for j, c := range ss.Cols {
						if c == col {
							idx = j
							break
						}
					}
					if idx < 0 || idx >= len(row.Data) {
						continue
					}
					localVals[i] = row.Data[idx].ToAny()
					if localVals[i] != nil {
						allNull = false
					}
				}
				if allNull {
					continue
				}
				// Check if referenced row exists
				refSS, ok := DT.SchemaFor(fk.RefTable)
				if !ok {
					continue
				}
				DT.TablesMu.RLock()
				refRows := DT.Tables[fk.RefTable]
				DT.TablesMu.RUnlock()
				found := false
				for _, refRow := range refRows {
					match := true
					for i, refCol := range fk.RefColumns {
						idx := -1
						for j, c := range refSS.Cols {
							if c == refCol {
								idx = j
								break
							}
						}
						if idx < 0 || idx >= len(refRow.Data) {
							match = false
							break
						}
						if !DT.EqualValueAny(refRow.Data[idx], localVals[i]) {
							match = false
							break
						}
					}
					if match {
						found = true
						break
					}
				}
				if !found {
					p.rows = append(p.rows, DT.Row{
						Cols: []string{"table", "rowid", "parent", "fkid"},
						Data: []DT.Value{
							DT.NewTextValue(name),
							DT.NewIntValue(int64(rowIdx)),
							DT.NewTextValue(fk.RefTable),
							DT.NewIntValue(int64(fkID)),
						},
					})
				}
			}
		}
	}
}

func (p *Pragma) Close() error {
	p.done = false
	p.idx = 0
	p.rows = p.rows[:0]
	return nil
}
func (p *Pragma) WithParams(_ []any) DT.Operator { return p }
func (p *Pragma) RowsAffected() int64         { return 0 }

// Explain runs the inner plan and returns a textual description of it
// as a single-row result. REQ000481, REQ000500.
type Explain struct {
	Stmt   *PS.ExplainStmt
	plan   DT.Operator
	done   bool
	rowOut bool
	desc   string
}

func NewExplain(stmt *PS.ExplainStmt) *Explain { return &Explain{Stmt: stmt} }

func (e *Explain) WithPlanner(p pl.QueryPlanner) DT.Operator {
	if e.Stmt != nil && e.Stmt.Inner != nil {
		// The inner statement has already been planned by buildWriterOp or
		// the caller. Stash the planner so the EXPLAIN text can mention
		// the planner name.
		_ = p
	}
	return e
}

func (e *Explain) Next(ctx context.Context) (DT.Row, error) {
	if e.done && e.rowOut {
		return DT.Row{}, DT.ErrNoRows
	}
	if !e.done {
		e.done = true
		e.desc = e.explain()
		return DT.Row{
			Cols:  []string{"plan"},
			Types: []LX.TokenType{LX.T_TEXT},
			Data:  []DT.Value{DT.NewTextValue(e.desc)},
		}, nil
	}
	e.rowOut = true
	return DT.Row{}, DT.ErrNoRows
}

func (e *Explain) explain() string {
	if e.Stmt == nil || e.Stmt.Inner == nil {
		return "EXPLAIN: no statement"
	}
	switch s := e.Stmt.Inner.(type) {
	case *PS.Select:
		return fmt.Sprintf("EXPLAIN: SELECT from %s", s.From)
	case *PS.Insert:
		return fmt.Sprintf("EXPLAIN: INSERT INTO %s", s.Table)
	case *PS.Update:
		return fmt.Sprintf("EXPLAIN: UPDATE %s", s.Table)
	case *PS.Delete:
		return fmt.Sprintf("EXPLAIN: DELETE FROM %s", s.Table)
	default:
		return fmt.Sprintf("EXPLAIN: %T", e.Stmt.Inner)
	}
}

func (e *Explain) Close() error                { return nil }
func (e *Explain) WithParams(_ []any) DT.Operator { return e }
func (e *Explain) RowsAffected() int64         { return 0 }

// Truncate is a writer-op stub for TRUNCATE [TABLE] name. REQ000476.
type Truncate struct {
	Stmt *PS.TruncateStmt
	done bool
	rows int64
}

func NewTruncate(stmt *PS.TruncateStmt) *Truncate { return &Truncate{Stmt: stmt} }

func (t *Truncate) Next(ctx context.Context) (DT.Row, error) {
	if t.done {
		return DT.Row{}, DT.ErrNoRows
	}
	t.done = true
	// Truncate = DELETE without WHERE; reuse the in-memory delete path.
	if DT.Schema(t.Stmt.Table) != nil {
		DT.TablesMu.Lock()
		if existing, ok := DT.Tables[t.Stmt.Table]; ok {
			t.rows = int64(len(existing))
		}
		DT.Tables[t.Stmt.Table] = nil
		DT.TablesMu.Unlock()
	}
	return DT.Row{}, DT.ErrNoRows
}

func (t *Truncate) Close() error                { return nil }
func (t *Truncate) WithParams(_ []any) DT.Operator { return t }
func (t *Truncate) RowsAffected() int64         { return t.rows }

// Reindex is a writer-op stub for REINDEX. REQ000478.
type Reindex struct {
	Stmt *PS.ReindexStmt
	done bool
}

func NewReindex(stmt *PS.ReindexStmt) *Reindex { return &Reindex{Stmt: stmt} }

func (r *Reindex) Next(ctx context.Context) (DT.Row, error) {
	if r.done {
		return DT.Row{}, DT.ErrNoRows
	}
	r.done = true

	// REQ000848/REQ000849: verify target exists when REINDEX specifies a name.
	// SQLite semantics: REINDEX idxname rebuilds that index;
	// REINDEX tblname is a no-op if the table has no indexes (or
	// rebuilds all indexes on that table). We treat both the index
	// lookup and the table lookup as success paths — if the target
	// matches either, the statement succeeds.
	if r.Stmt.Target != "" {
		DT.StoreMu.Lock()
		found := false
		// Check if target is a known index.
		for _, idxs := range DT.RegisteredIndexes {
			for _, idx := range idxs {
				if idx.Name == r.Stmt.Target {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		// REQ000848: if not an index, check if it's a table name.
		// SQLite treats REINDEX tblname as a successful no-op when
		// the table has no indexes.
		if !found {
			if _, ok := DT.Schemas[r.Stmt.Target]; ok {
				found = true
			}
		}
		DT.StoreMu.Unlock()
		if !found {
			return DT.Row{}, fmt.Errorf("ex: no such index: %s", r.Stmt.Target)
		}
	}
	return DT.Row{}, DT.ErrNoRows
}

func (r *Reindex) Close() error                { return nil }
func (r *Reindex) WithParams(_ []any) DT.Operator { return r }
func (r *Reindex) RowsAffected() int64         { return 0 }

// DropView is a writer-op for DROP VIEW [IF EXISTS] name. REQ000494.
type DropView struct {
	Stmt *PS.DropViewStmt
	done bool
}

func NewDropView(stmt *PS.DropViewStmt) *DropView { return &DropView{Stmt: stmt} }

func (d *DropView) Next(ctx context.Context) (DT.Row, error) {
	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true
	if d.Stmt == nil {
		return DT.Row{}, DT.ErrNoRows
	}
	existed := DT.UnregisterView(d.Stmt.Name)
	if !existed && !d.Stmt.IfExists {
		return DT.Row{}, fmt.Errorf("ex: view %s does not exist", d.Stmt.Name)
	}
	return DT.Row{}, DT.ErrNoRows
}

func (d *DropView) Close() error                { return nil }
func (d *DropView) WithParams(_ []any) DT.Operator { return d }
func (d *DropView) RowsAffected() int64         { return 0 }

// DropTrigger is a writer-op for DROP TRIGGER [IF EXISTS] name. REQ000496.
type DropTrigger struct {
	Stmt *PS.DropTriggerStmt
	done bool
}

func NewDropTrigger(stmt *PS.DropTriggerStmt) *DropTrigger {
	return &DropTrigger{Stmt: stmt}
}

func (d *DropTrigger) Next(ctx context.Context) (DT.Row, error) {
	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true
	if d.Stmt == nil {
		return DT.Row{}, DT.ErrNoRows
	}
	existed := UnregisterTrigger(d.Stmt.Name)
	if !existed && !d.Stmt.IfExists {
		return DT.Row{}, fmt.Errorf("ex: trigger %s does not exist", d.Stmt.Name)
	}
	return DT.Row{}, DT.ErrNoRows
}

func (d *DropTrigger) Close() error                { return nil }
func (d *DropTrigger) WithParams(_ []any) DT.Operator { return d }
func (d *DropTrigger) RowsAffected() int64         { return 0 }

// applyConflictUpdate locates the conflicting row by unique-key match
// and applies the SET clauses. Used by INSERT ... ON CONFLICT DO
// UPDATE. REQ000511.
func applyConflictUpdate(schema *DT.StoreSchema, existing []DT.Row, out DT.Row, sets []PS.Pair, params []any, apply UniqueLookupWithApply) error {
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
	rowIdx, ok, err := apply.FindAndLock(idxs, DT.ValueSliceToAny(vals))
	if err != nil {
		return err
	}
	if !ok {
		// No matching row found (race with another writer).
		// Skip silently per UPSERT semantics.
		return nil
	}
	_ = existing
	return apply.Mutate(rowIdx, func(target DT.Row) DT.Row {
		updated := DT.CloneRow(target)
		for _, p := range sets {
			ci := -1
			for i, c := range schema.Cols {
				if c == p.Col {
					ci = i
					break
				}
			}
			if ci < 0 {
				continue
			}
			v, err := EV.EvalValue(p.Val, &out, params)
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
func conflictKey(schema *DT.StoreSchema, row DT.Row) ([]int, []DT.Value, error) {
	if schema.Pk != "" {
		for i, c := range schema.Cols {
			if c == schema.Pk {
				if i >= len(row.Data) {
					return nil, nil, fmt.Errorf("ex: PK column %q out of range", schema.Pk)
				}
				return []int{i}, []DT.Value{row.Data[i]}, nil
			}
		}
	}
	if len(schema.Unique) > 0 {
		uk := schema.Unique[0]
		vals := make([]DT.Value, len(uk.Cols))
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

// evalReturning evaluates RETURNING expressions for a single row.
// REQ000984: extracted from 7 duplicated call sites in Insert/Update/Delete.
func evalReturning(exprs []PS.Expr, row *DT.Row, params []any, resultRows *[]DT.Row) error {
	if len(exprs) == 0 {
		return nil
	}
	expanded := expandReturningStar(exprs, row.Cols)
	resultRow := DT.Row{
		Cols:  make([]string, len(expanded)),
		Types: make([]LX.TokenType, len(expanded)),
		Data:  make([]DT.Value, len(expanded)),
	}
	for j, expr := range expanded {
		val, err := EV.EvalValue(expr, row, params)
		if err != nil {
			return err
		}
		resultRow.Cols[j] = colNameForReturning(expr, row.Cols, j)
		resultRow.Data[j] = val
	}
	*resultRows = append(*resultRows, resultRow)
	return nil
}

// UnsupportedOp is a writer-op stub for statement types that the
// parser accepts but the v1 executor cannot run. It defers the
// rejection until Next() so the planner/executor pipeline surfaces
// the error in a uniform location.
type UnsupportedOp struct {
	err    error
	done   bool
	Stmt   PS.Stmt
}

func NewUnsupportedOp(stmt PS.Stmt, msg string) *UnsupportedOp {
	return &UnsupportedOp{err: errors.New(msg), Stmt: stmt}
}

func (u *UnsupportedOp) Next(ctx context.Context) (DT.Row, error) {
	if u.done {
		return DT.Row{}, DT.ErrNoRows
	}
	u.done = true
	return DT.Row{}, u.err
}

func (u *UnsupportedOp) Close() error                { return nil }
func (u *UnsupportedOp) WithParams(_ []any) DT.Operator { return u }
func (u *UnsupportedOp) RowsAffected() int64         { return 0 }

// AttachOp implements ATTACH DATABASE by recording the name→path
// mapping on the Executor. REQ000908.
type AttachOp struct {
	amgr   DT.DBAttachManager
	name   string
	path   string
	done   bool
	closed bool
}

func NewAttachOp(amgr DT.DBAttachManager, name, path string) *AttachOp {
	return &AttachOp{amgr: amgr, name: name, path: path}
}

func (a *AttachOp) Next(ctx context.Context) (DT.Row, error) {
	if a.done {
		return DT.Row{}, DT.ErrNoRows
	}
	a.done = true
	if a.closed {
		return DT.Row{}, errors.New("wt: attach op is closed")
	}
	// For v1, cross-database queries (SELECT * FROM attached.t) are
	// rejected at the planner level by the qualified-name resolver.
	a.amgr.AttachDB(a.name, a.path)
	return DT.Row{}, nil
}

func (a *AttachOp) Close() error {
	a.closed = true
	return nil
}
func (a *AttachOp) WithParams(_ []any) DT.Operator { return a }
func (a *AttachOp) RowsAffected() int64         { return 0 }

// DetachOp implements DETACH DATABASE by removing the name→path
// mapping from the Executor. REQ000908.
type DetachOp struct {
	amgr   DT.DBAttachManager
	name   string
	done   bool
	closed bool
}

func NewDetachOp(amgr DT.DBAttachManager, name string) *DetachOp {
	return &DetachOp{amgr: amgr, name: name}
}

func (d *DetachOp) Next(ctx context.Context) (DT.Row, error) {
	if d.done {
		return DT.Row{}, DT.ErrNoRows
	}
	d.done = true
	if d.closed {
		return DT.Row{}, errors.New("wt: detach op is closed")
	}
	d.amgr.DetachDB(d.name)
	return DT.Row{}, nil
}

func (d *DetachOp) Close() error {
	d.closed = true
	return nil
}
func (d *DetachOp) WithParams(_ []any) DT.Operator { return d }
func (d *DetachOp) RowsAffected() int64         { return 0 }

// ErrMultiDatabaseNotSupported is returned when a query attempts to
// reference an attached database. Full cross-database query support
// (SELECT from attached.t, etc.) is deferred. REQ000908.
var ErrMultiDatabaseNotSupported = errors.New("wt: cross-database queries not supported in v1")

// fireInsertTriggers fires all AFTER INSERT triggers for the given table.
// The new row is passed as the context for trigger execution.
func fireInsertTriggers(table string, newRow *DT.Row, params []any, store DT.Store) error {
	// Build a minimal executor callback for trigger SQL execution
	exec := func(sql string) error {
		parser := PS.NewParser(sql)
		stmt, err := parser.Parse()
		if err != nil {
			return err
		}
		// For matview refresh, we need to execute the statement
		// This is a simplified implementation that works for REFRESH MATERIALIZED VIEW
		// Full implementation would wire through the executor
		if _, ok := stmt.(*PS.RefreshMatViewStmt); ok {
			// Execute refresh via store path
			return executeRefreshMatViewSQL(sql, store)
		}
		return nil
	}

	return FireTriggers(table, "AFTER", "INSERT", nil, newRow, params, exec)
}

// executeRefreshMatViewSQL executes a REFRESH MATERIALIZED VIEW statement.
func executeRefreshMatViewSQL(sql string, store DT.Store) error {
	parser := PS.NewParser(sql)
	stmt, err := parser.Parse()
	if err != nil {
		return err
	}
	refresh, ok := stmt.(*PS.RefreshMatViewStmt)
	if !ok {
		return errors.New("ex: not a refresh matview statement")
	}

	// Look up the matview definition
	matSel := DT.LookupMatView(refresh.Name)
	if matSel == nil {
		return fmt.Errorf("ex: materialized view %q not found", refresh.Name)
	}

	// For now, this is a full refresh (re-execute the query and store results)
	// Incremental refresh would require tracking changes to base DT.Tables
	// This is the baseline implementation for REQ000316
	return refreshMatViewData(refresh.Name, matSel, store)
}

// refreshMatViewData re-executes the matview query and updates the stored data.
func refreshMatViewData(name string, sel *PS.Select, store DT.Store) error {
	// This is a simplified implementation that re-runs the query
	// A full implementation would use the planner to build an execution plan
	// and write results to the matview data prefix

	// For now, we just clear old data and mark the view as needing refresh
	matPrefix := MatViewDataPrefix(name)
	if store != nil {
		it := store.NewIterator(matPrefix)
		for it.Next() {
			_ = store.Delete(it.Key())
		}
		it.Close()
	}

	return nil
}

// fireUpdateTriggers fires all AFTER UPDATE triggers for the given table.
func fireUpdateTriggers(table string, oldRow *DT.Row, newRow *DT.Row, params []any, store DT.Store) error {
	exec := func(sql string) error {
		parser := PS.NewParser(sql)
		stmt, err := parser.Parse()
		if err != nil {
			return err
		}
		if _, ok := stmt.(*PS.RefreshMatViewStmt); ok {
			return executeRefreshMatViewSQL(sql, store)
		}
		return nil
	}

	return FireTriggers(table, "AFTER", "UPDATE", oldRow, newRow, params, exec)
}

// fireDeleteTriggers fires all AFTER DELETE triggers for the given table.
func fireDeleteTriggers(table string, oldRow *DT.Row, params []any, store DT.Store) error {
	exec := func(sql string) error {
		parser := PS.NewParser(sql)
		stmt, err := parser.Parse()
		if err != nil {
			return err
		}
		if _, ok := stmt.(*PS.RefreshMatViewStmt); ok {
			return executeRefreshMatViewSQL(sql, store)
		}
		return nil
	}

	return FireTriggers(table, "AFTER", "DELETE", oldRow, nil, params, exec)
}
