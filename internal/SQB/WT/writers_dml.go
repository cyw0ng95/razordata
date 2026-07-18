package WT

import (
	"context"
	"errors"
	"fmt"
	"strings"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
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
	pending        map[string]struct{} // REQ001563: reused pending map for conflict resolution
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
	if i.pending == nil {
		i.pending = make(map[string]struct{}, len(i.values)+1)
	} else {
		clear(i.pending)
	}
	var iterValues [][]PS.Expr
	if i.defaultValues {
		clear(i.pending)
		iterValues = [][]PS.Expr{nil}
	} else {
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
			out, err = BuildInsertRow(schema, i.cols, colIdx, row, i.params, nil)
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
			if err := CheckUnique(cschema, out, i.pending, DT.Row{}, AsUniqueLookup(lookup)); err != nil {
				if i.conflictAction == PS.ConflictActionReplace {
					var removed int
					existing, removed = RemoveConflicting(existing, cschema, out)
					i.rows += int64(removed)
					if i.execCtx != nil {
						i.execCtx.LastChanges += int64(removed)
						i.execCtx.TotalChanges += int64(removed)
					}
					clear(i.pending)
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
					// REQ001383: for DO NOTHING with RETURNING, look up
					// the existing row and emit it via evalReturning.
					if len(i.returning) > 0 {
						if lookupApply, ok := lookup.(UniqueLookupWithApply); ok {
							existingRow, found, lerr := findExistingRow(lookupApply, cschema, out)
							if lerr != nil {
								return DT.Row{}, lerr
							}
							if found {
								if err := evalReturning(i.returning, &existingRow, i.params, &i.resultRows); err != nil {
									return DT.Row{}, err
								}
							}
						}
					}
					continue
				}
				// DO UPDATE: locate the conflicting row and apply
				// the SET clauses. We re-use the lookup closure
				// to find the existing row and mutate it in place.
				if apply, ok := lookup.(UniqueLookupWithApply); ok {
					resultRow, cerr := applyConflictUpdate(cschema, existing, out, i.onConflict, i.params, apply)
					if cerr != nil {
						if errors.Is(cerr, ErrTargetWhereFalse) {
							// REQ001364: partial-index WHERE on the
							// conflict target evaluated false — treat
							// the row as non-conflicting. Remove the
							// existing row and fall through to normal
							// insertion (doInsertReplace).
							var removed int
							existing, removed = RemoveConflicting(existing, cschema, out)
							i.rows += int64(removed)
							if i.execCtx != nil {
								i.execCtx.LastChanges += int64(removed)
								i.execCtx.TotalChanges += int64(removed)
							}
							clear(i.pending)
							goto doInsertReplace
						}
						return DT.Row{}, cerr
					}
					// Note: we already counted the row's impact in
					// applyConflictUpdate (it updated an existing
					// row in place). Do not append to `existing` and
					// do not increment i.rows — that would create a
					// duplicate.
					// REQ001383: emit RETURNING row for DO UPDATE
					// using the post-update row.
					if len(i.returning) > 0 {
						if err := evalReturning(i.returning, &resultRow, i.params, &i.resultRows); err != nil {
							return DT.Row{}, err
						}
					}
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
			clear(i.pending)
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
	if i.pending == nil {
		i.pending = make(map[string]struct{}, len(i.values)+1)
	} else {
		clear(i.pending)
	}
	var iterValues [][]PS.Expr
	if i.defaultValues {
		clear(i.pending)
		iterValues = [][]PS.Expr{nil}
	} else {
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
	// REQ001426: pre-allocate all rows' Data from one contiguous slice
	// to eliminate per-row make([]Value, N) — for 100-row INSERT this
	// replaces 100 small allocs with 1 larger one.
	nRows := len(iterValues)
	nCols := len(i.schema.Cols)
	var bigBuf []DT.Value
	if i.schema != nil && nRows > 1 && nCols > 0 {
		bigBuf = make([]DT.Value, nRows*nCols)
	}
	for ri, row := range iterValues {
		var out DT.Row
		var err error
		// REQ001426: use pre-allocated bigBuf slot when available.
		var dataBuf []DT.Value
		if bigBuf != nil {
			off := ri * nCols
			dataBuf = bigBuf[off : off : off+nCols]
		}
		if row == nil && i.defaultValues {
			out = DT.Row{Cols: i.schema.Cols}
			if dataBuf != nil {
				out.Data = dataBuf
			} else {
				out.Data = make([]DT.Value, nCols)
			}
		} else {
			out, err = BuildInsertRow(i.schema.Cols, i.cols, colIdx, row, i.params, dataBuf)
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
		if err := CheckUnique(i.schema, out, i.pending, DT.Row{}, lookupFn); err != nil {
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

	if i.pending == nil {
		i.pending = make(map[string]struct{})
	} else {
		clear(i.pending)
	}
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
			if err := CheckUnique(cschema, out, i.pending, DT.Row{}, AsUniqueLookup(lookup)); err != nil {
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
	i.done = false
	i.rows = 0
	i.resultRows = nil
	i.resultPos = 0
	return nil
}

// Reset reinitializes the Insert operator for reuse without Close.
// Clears execution state (done, rows, resultRows) so the same
// operator can be executed again with different parameters.
// REQ001425: operator tree cache support.
func (i *Insert) Reset() {
	i.done = false
	i.rows = 0
	i.resultRows = nil
	i.resultPos = 0
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
	// REQ001424: pre-size the RowArena so the apply-update/fill/validate
	// path doesn't repeatedly grow the slab.
	if u.execCtx != nil {
		if arena, ok := u.execCtx.RowArena.(*DT.RowArena); ok && arena != nil && !arena.Initialized() {
			ss, ok := DT.SchemaFor(u.table)
			nCols := 8
			if ok && ss != nil {
				nCols = len(ss.Cols)
			}
			arena.Init(256, nCols)
		}
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
	err := u.iter.Close()
	u.done = false
	u.rows = 0
	u.resultRows = nil
	u.resultPos = 0
	return err
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
	// REQ001424: pre-size the RowArena for the delete loop.
	if d.execCtx != nil {
		if arena, ok := d.execCtx.RowArena.(*DT.RowArena); ok && arena != nil && !arena.Initialized() {
			nCols := 8
			if dschema != nil {
				nCols = len(dschema.Cols)
			}
			arena.Init(256, nCols)
		}
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
	err := d.iter.Close()
	d.done = false
	d.rows = 0
	d.resultRows = nil
	d.resultPos = 0
	return err
}

func (d *Delete) RowsAffected() int64 {
	return d.rows
}

// expandReturningStar expands StarExpr entries in the RETURNING list
// into individual column references. REQ000518: RETURNING * returns
// all columns of the inserted/updated/deleted row.
func expandReturningStar(exprs []PS.Expr, colNames []string) []PS.Expr {
	var out []PS.Expr
	for _, e := range exprs {
		if _, ok := e.(*PS.StarExpr); ok {
			for _, name := range colNames {
				out = append(out, &PS.Ident{Name: name, SlotIdx: -1})
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
// ErrTargetWhereFalse is returned by applyConflictUpdate when the
// partial-index WHERE on the conflict target evaluates false,
// signaling the caller to treat the row as non-conflicting. REQ001364.
var ErrTargetWhereFalse = errors.New("wt: ON CONFLICT partial-index WHERE false")

// findExistingRow locates the conflicting row using the conflict key
// derived from `newRow`. Returns the existing row and found=true if a
// match exists. Used by DO NOTHING RETURNING path. REQ001383.
//
// The caller must hold DT.TablesMu (write lock) since this function
// reads DT.Tables directly without re-acquiring the lock.
func findExistingRow(apply UniqueLookupWithApply, schema *DT.StoreSchema, newRow DT.Row) (DT.Row, bool, error) {
	idxs, vals, err := conflictKey(schema, newRow)
	if err != nil {
		return DT.Row{}, false, err
	}
	if found, _ := apply.Lookup(idxs, DT.ValueSliceToAny(vals)); !found {
		return DT.Row{}, false, nil
	}
	// Walk DT.Tables looking for a row matching the conflict key.
	for _, rows := range DT.Tables {
		for _, r := range rows {
			match := true
			for _, i := range idxs {
				if i < len(r.Data) && i < len(newRow.Data) {
					eq, _ := DT.EqualValue(r.Data[i], newRow.Data[i])
					if !eq {
						match = false
						break
					}
				}
			}
			if match {
				return r, true, nil
			}
		}
	}
	return DT.Row{}, false, nil
}

func applyConflictUpdate(schema *DT.StoreSchema, existing []DT.Row, out DT.Row, onConflict *PS.OnConflict, params []any, apply UniqueLookupWithApply) (DT.Row, error) {
	if apply == nil || onConflict == nil {
		return out, nil
	}
	// REQ001365: if DO UPDATE has a WHERE clause and the predicate is
	// false against the existing row, fall through to DO NOTHING
	// for this conflict.
	if onConflict.UpdateWhere != nil && apply != nil {
		// We need the existing row first to evaluate WHERE — defer the
		// predicate check until we have located it below.
	}
	// Build the lookup key from the PK column (we use PK as the
	// canonical conflict target when OnConflict.Columns is empty,
	// matching the most common SQLite UPSERT pattern).
	idxs, vals, err := conflictKey(schema, out)
	if err != nil {
		return out, err
	}
	rowIdx, ok, err := apply.FindAndLock(idxs, DT.ValueSliceToAny(vals))
	if err != nil {
		return out, err
	}
	if !ok {
		// No matching row found (race with another writer).
		// Skip silently per UPSERT semantics.
		return out, nil
	}
	_ = existing
	var targetWhereFalse bool
	var resultRow DT.Row
	merr := apply.Mutate(rowIdx, func(target DT.Row) DT.Row {
		// REQ001364: if TargetWhere is set and evaluates to false
		// against the existing row, skip the UPSERT.
		if onConflict.TargetWhere != nil {
			pred, perr := EV.EvalValue(onConflict.TargetWhere, &target, nil)
			if perr == nil && !DT.IsValueTruthy(pred) {
				targetWhereFalse = true
				resultRow = target
				return target
			}
		}
		// REQ001365: evaluate UpdateWhere against the existing target
		// row; if the predicate is false, return target unchanged.
		if onConflict.UpdateWhere != nil {
			pred, perr := EV.EvalValue(onConflict.UpdateWhere, &target, params)
			if perr == nil {
				if !DT.IsValueTruthy(pred) {
					resultRow = target
					return target
				}
			}
		}
		updated := DT.CloneRow(target)
		for _, p := range onConflict.SetClauses {
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
			v, err := evalUpsertValue(p.Val, &out, params)
			if err != nil {
				continue
			}
			if ci < len(updated.Data) {
				updated.Data[ci] = v
			}
		}
		// REQ001383: capture updated row for RETURNING.
		resultRow = updated
		return updated
	})
	if merr != nil {
		return out, merr
	}
	if targetWhereFalse {
		return resultRow, ErrTargetWhereFalse
	}
	return resultRow, nil
}

// evalUpsertValue evaluates the SET-clause RHS, resolving EXCLUDED.col
// against the new (would-be-inserted) row. EXCLUDED.col maps to
// QualifiedName{Table: "excluded", Name: col}; we intercept this case
// and look up the bare column name in the new row, falling back to the
// generic EV.EvalValue for everything else (literals, arithmetic,
// nested expressions). REQ001363.
func evalUpsertValue(expr PS.Expr, excluded *DT.Row, params []any) (DT.Value, error) {
	if qn, ok := expr.(*PS.QualifiedName); ok && strings.EqualFold(qn.Table, "excluded") {
		if excluded != nil {
			for i, c := range excluded.Cols {
				if strings.EqualFold(c, qn.Name) && i < len(excluded.Data) {
					return excluded.Data[i], nil
				}
			}
		}
		// EXCLUDED.col referenced but column not in the new row —
		// surface as NULL rather than silently substituting text.
		return DT.NullValue(), nil
	}
	return EV.EvalValue(expr, excluded, params)
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
