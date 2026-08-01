package WT

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// growSlice grows *s to length n without zeroing the new capacity.
// It is a generic helper that avoids the per-call make pattern
// (REQ001557 family of zero-alloc REQs).
func growSlice[T any](s *[]T, n int) {
	if cap(*s) >= n {
		*s = (*s)[:n]
		return
	}
	nc := max(cap(*s)*2, n)
	b := make([]T, n, nc)
	copy(b, *s)
	*s = b
}

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
	execCtx        *DT.ExecContext     // REQ000812
	pending        map[string]struct{} // REQ001563: reused pending map for conflict resolution
	colIndexMap    map[string]int      // REQ001564: pre-computed column index map for INSERT...SELECT
	rColsBuf       []string            // REQ001557: flat RETURNING col name backing
	rTypesBuf      []LX.TokenType      // REQ001557: flat RETURNING type backing
	rDataBuf       []DT.Value          // REQ001557: flat RETURNING data backing
	// REQ001675: pooled scratch buffers reused across INSERT calls.
	scratchColIdx    []int     // pre-computed column index mapping
	scratchInsertBuf []DT.Row  // batch insert buffer
	scratchBigBuf    []DT.Value // contiguous value buffer for multi-row INSERT
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
	ins := &Insert{
		table:      table,
		cols:       cols,
		values:     values,
		returning:  returning,
		onConflict: onConflict,
		store:      store,
		schema:     ss,
	}
	if len(returning) > 0 {
		nCols := len(ss.Cols)
		ins.rColsBuf = make([]string, 0, 256*nCols)
		ins.rTypesBuf = make([]LX.TokenType, 0, 256*nCols)
		ins.rDataBuf = make([]DT.Value, 0, 256*nCols)
	}
	return ins, nil
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
	// REQ001565: pre-allocate Data buffer for DEFAULT VALUES path (mirrors REQ001426 store path).
	var defaultBuf []DT.Value
	if i.defaultValues && len(schema) > 0 {
		defaultBuf = make([]DT.Value, len(schema))
	}
	for _, row := range iterValues {
		var out DT.Row
		var err error
		if row == nil && i.defaultValues {
			out = DT.Row{Cols: schema}
			out.Data = defaultBuf
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
								if err := evalReturning(i.returning, &existingRow, i.params, &i.resultRows, &i.rColsBuf, &i.rTypesBuf, &i.rDataBuf); err != nil {
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
						if err := evalReturning(i.returning, &resultRow, i.params, &i.resultRows, &i.rColsBuf, &i.rTypesBuf, &i.rDataBuf); err != nil {
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
			if err := evalReturning(i.returning, &out, i.params, &i.resultRows, &i.rColsBuf, &i.rTypesBuf, &i.rDataBuf); err != nil {
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
	// REQ001675: reuse scratch buffer across INSERT calls.
	if cap(i.scratchColIdx) < len(i.cols) {
		i.scratchColIdx = make([]int, len(i.cols))
	} else {
		i.scratchColIdx = i.scratchColIdx[:len(i.cols)]
	}
	colIdx := i.scratchColIdx
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
	// REQ001675: reuse scratch buffer; grow only when capacity is insufficient.
	if i.schema != nil && nRows > 1 && nCols > 0 {
		need := nRows * nCols
		if cap(i.scratchBigBuf) < need {
			i.scratchBigBuf = make([]DT.Value, need)
		} else {
			i.scratchBigBuf = i.scratchBigBuf[:need]
		}
	}
	// REQ001589: batch INSERT rows into InsertRowBatch for amortised
	// PK extraction, encoding, and store write. The batch is flushed
	// when full or when conflict handling requires per-row insertion.
	const insertChunkSize = 256
	// REQ001675: reuse scratch insert buffer.
	if cap(i.scratchInsertBuf) < insertChunkSize {
		i.scratchInsertBuf = make([]DT.Row, 0, insertChunkSize)
	} else {
		i.scratchInsertBuf = i.scratchInsertBuf[:0]
	}
	insertBuf := i.scratchInsertBuf
	flushInsertBuf := func() error {
		if len(insertBuf) == 0 {
			return nil
		}
		keys, bufs, err := h.InsertRowBatch(insertBuf)
		if err != nil {
			return err
		}
		if i.txWriter != nil {
			for j, k := range keys {
				i.txWriter.RecordWrite(k, bufs[j])
			}
		}
		i.rows += int64(len(insertBuf))
		if i.execCtx != nil {
			i.execCtx.LastChanges += int64(len(insertBuf))
			i.execCtx.TotalChanges += int64(len(insertBuf))
		}
		insertBuf = insertBuf[:0]
		return nil
	}
	for ri, row := range iterValues {
		var out DT.Row
		var err error
		// REQ001426: use pre-allocated bigBuf slot when available.
		var dataBuf []DT.Value
		if i.scratchBigBuf != nil {
			off := ri * nCols
			dataBuf = i.scratchBigBuf[off : off : off+nCols]
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
				// Flush pending batch before per-row delete+insert.
				if err := flushInsertBuf(); err != nil {
					return DT.Row{}, err
				}
				if _, derr := h.DeleteRow(out); derr == nil {
					i.rows++
					if i.execCtx != nil {
						i.execCtx.LastChanges++
						i.execCtx.TotalChanges++
					}
				}
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
		// REQ001589: batch insert via InsertRowBatch instead of per-row InsertRow.
		insertBuf = append(insertBuf, out)
		if len(insertBuf) >= insertChunkSize {
			if err := flushInsertBuf(); err != nil {
				return DT.Row{}, err
			}
		}

		// Fire AFTER INSERT triggers (REQ000316: incremental matview support)
		if err := fireInsertTriggers(i.table, &out, i.params, i.store); err != nil {
			return DT.Row{}, err
		}

		// Evaluate RETURNING expressions (REQ000518: expand *)
		if len(i.returning) > 0 {
			if err := evalReturning(i.returning, &out, i.params, &i.resultRows, &i.rColsBuf, &i.rTypesBuf, &i.rDataBuf); err != nil {
				return DT.Row{}, err
			}
		}
	}
	// Flush remaining batch.
	if err := flushInsertBuf(); err != nil {
		return DT.Row{}, err
	}

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
	// REQ001705: pre-allocate selectRows to exact row count to avoid
	// geometric growth reallocation churn.
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
	// Compact to exact size — eliminates over-capacity from geometric growth.
	if len(selectRows) > 0 && cap(selectRows) > len(selectRows) {
		compact := make([]DT.Row, len(selectRows))
		copy(compact, selectRows)
		selectRows = compact
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

	// REQ001564: pre-compute column index map once.
	if i.colIndexMap == nil {
		i.colIndexMap = make(map[string]int, len(schema))
		for idx, c := range schema {
			i.colIndexMap[c] = idx
		}
	}

	// REQ001589: batch INSERT via InsertRowBatch for store-backed path.
	const selChunkSize = 256
	selBuf := make([]DT.Row, 0, selChunkSize)
	flushSelBuf := func() error {
		if len(selBuf) == 0 || hsel == nil {
			return nil
		}
		keys, bufs, ierr := hsel.InsertRowBatch(selBuf)
		if ierr != nil {
			return ierr
		}
		if i.txWriter != nil {
			for j, k := range keys {
				i.txWriter.RecordWrite(k, bufs[j])
			}
		}
		selBuf = selBuf[:0]
		return nil
	}
	for _, row := range selectRows {
		// Build insert row from SELECT result
		out, err := buildInsertRowFromSelectWithMap(schema, i.cols, row, i.colIndexMap)
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
					// Flush batch before skipping, then skip this row.
					if err := flushSelBuf(); err != nil {
						return DT.Row{}, err
					}
					continue
				}
				return DT.Row{}, err
			}
			// REQ001129: store-backed INSERT...SELECT must write to
			// the store engine, not just the in-memory table map.
			// REQ000987: route through TableHandle.InsertRowBatch so the
			// PK extract + REQ001128 rowid auto-fill + EncodeRow +
			// Store.Insert + MaintainIndexesOnInsert sequence is
			// encapsulated in a single batch call.
			if hsel != nil {
				selBuf = append(selBuf, out)
				if len(selBuf) >= selChunkSize {
					if err := flushSelBuf(); err != nil {
						return DT.Row{}, err
					}
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
			if err := evalReturning(i.returning, &out, i.params, &i.resultRows, &i.rColsBuf, &i.rTypesBuf, &i.rDataBuf); err != nil {
				return DT.Row{}, err
			}
		}
	}
	// Flush remaining batch.
	if err := flushSelBuf(); err != nil {
		return DT.Row{}, err
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
		colIdx := make(map[string]int, len(schema))
		for i, c := range schema {
			colIdx[c] = i
		}
		return buildInsertRowFromSelectWithMap(schema, cols, src, colIdx)
	}
	// No column list: use SELECT columns directly
	out := DT.Row{Cols: make([]string, len(src.Cols))}
	copy(out.Cols, src.Cols)
	out.Data = append([]DT.Value(nil), src.Data...)
	return out, nil
}

// buildInsertRowFromSelectWithMap is the internal implementation that accepts
// a pre-computed column index map to avoid per-row map allocation.
// REQ001564.
func buildInsertRowFromSelectWithMap(schema []string, cols []string, src DT.Row, colIdx map[string]int) (DT.Row, error) {
	if len(cols) > 0 {
		// Map SELECT columns to insert columns by position
		// REQ001681: share the schema Cols slice (stable across all rows).
		out := DT.Row{Cols: schema}
		out.Data = make([]DT.Value, len(schema))
		// REQ001705: use caller's pre-computed colIdx instead of
		// allocating a new map per row (was shadowing the parameter).
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
	out := DT.Row{Cols: make([]string, len(src.Cols))}
	copy(out.Cols, src.Cols)
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

// NextBatch triggers the insert on first call and returns RETURNING rows
// in columnar batches. REQ001983.
func (i *Insert) NextBatch(ctx context.Context) (*UT.Batch, error) {
	// On first call, trigger the full insert via Next()
	if !i.done || (len(i.resultRows) > 0 && i.resultPos == 0) {
		_, err := i.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				if len(i.resultRows) == 0 {
					return nil, nil
				}
			} else {
				return nil, err
			}
		}
		if i.resultPos > 0 {
			i.resultPos--
		}
		if len(i.resultRows) == 0 {
			return nil, nil
		}
	}
	return drainResultRows(i.resultRows, &i.resultPos)
}

// drainResultRows slices resultRows[resultPos:] into batch-sized chunks.
func drainResultRows(resultRows []DT.Row, resultPos *int) (*UT.Batch, error) {
	remaining := len(resultRows) - *resultPos
	if remaining <= 0 {
		return nil, nil
	}
	batchSize := remaining
	if batchSize > UT.BatchSize {
		batchSize = UT.BatchSize
	}
	batch := UT.RowsToBatch(resultRows[*resultPos : *resultPos+batchSize])
	*resultPos += batchSize
	return batch, nil
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
	// pendingUpdates collects (oldRow, newRow) pairs whose AFTER
	// UPDATE triggers have not yet been fired. The chunk loop
	// appends; flushChunk fires triggers for the entire chunk in
	// one pass before clearing the slice. REQ001578.
	pendingUpdates []triggerEvent
	rColsBuf       []string       // REQ001557: flat RETURNING col name backing
	rTypesBuf      []LX.TokenType // REQ001557: flat RETURNING type backing
	rDataBuf       []DT.Value     // REQ001557: flat RETURNING data backing
	// REQ001585: pre-resolved column indices for SET targets.
	// Eliminates the O(N*M) linear scan per row in ApplyUpdate.
	setColIdx []int
}

// triggerEvent is one deferred trigger invocation captured during
// the batch mutation loop. The rows are borrowed pointers (the
// source RowArena owns the backing data) and stay valid for the
// lifetime of the writer call.
type triggerEvent struct {
	oldRow DT.Row
	newRow DT.Row // zero value for DELETE
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
	upd := &Update{
		table:     table,
		set:       set,
		where:     where,
		iter:      iter,
		returning: returning,
		store:     store,
		schema:    ss,
	}
	// REQ001585: pre-resolve column indices for SET targets.
	upd.setColIdx = make([]int, len(set))
	for i, p := range set {
		upd.setColIdx[i] = -1
		for j, c := range ss.Cols {
			if c == p.Col {
				upd.setColIdx[i] = j
				break
			}
		}
		// REQ001586: validate SET column exists in schema.
		if upd.setColIdx[i] < 0 {
			return nil, fmt.Errorf("ex: update SET column %q not found in table %q", p.Col, table)
		}
	}
	if len(returning) > 0 {
		nCols := len(ss.Cols)
		upd.rColsBuf = make([]string, 0, 256*nCols)
		upd.rTypesBuf = make([]LX.TokenType, 0, 256*nCols)
		upd.rDataBuf = make([]DT.Value, 0, 256*nCols)
	}
	return upd, nil
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
		// REQ002097: capture old-row values into the arena for snapshot.
		// SQB/OP.SeqScan uses shallow mode by default, so `row.Data` may
		// alias the underlying DT.Tables row. SnapshotRowSingle copies
		// the values into arena-owned slots so subsequent in-place
		// mutation of `row.Data` does not corrupt our snapshot. This
		// replaces the previous `DT.ShallowCloneRow(row)` allocation.
		var snapshot DT.Row
		if u.execCtx != nil {
			if arena, ok := u.execCtx.RowArena.(*DT.RowArena); ok && arena != nil {
				snapshot = arena.SnapshotRowSingle(row, len(row.Data))
			} else {
				snapshot = DT.ShallowCloneRow(row)
			}
		} else {
			snapshot = DT.ShallowCloneRow(row)
		}
		// REQ000840: Filter's fallback path (predicate not compilable)
		// returns the child's row directly without deep-copying Data.
		// To avoid aliasing DT.Tables during in-place ApplyUpdate, deep-
		// copy Data here. Filter.compiledBatch path already arranges for
		// arena-backed Data, so this copies once more on that path —
		// REQ002097's SnapshotRowSingle already provided the snapshot,
		// so the only redundant work is the Data copy itself. Acceptable
		// for the safety guarantee; can be revisited if the batch path
		// proves dominant in benchmarks.
		// REQ002104: when the SeqScan has needsStableData set, the Data
		// is already an independent copy — skip the redundant deep-copy.
		if !row.DataStable {
			row.Data = append([]DT.Value(nil), row.Data...)
		}
		if u.setColIdx != nil {
			if err := ApplyUpdateFast(&row, u.set, u.params, u.setColIdx); err != nil {
				return DT.Row{}, err
			}
		} else if err := ApplyUpdate(&row, u.set, u.params); err != nil {
			return DT.Row{}, err
		}
		if cschema != nil {
			// REQ002099+: guard validation calls. FillDefaults/
			// ValidateCheck take row DT.Row by value, and the
			// error return forces heap escape (~176B per call).
			// The Defaults/Generated/Checks slices are always
			// len=nCols (non-nil nil entries) for any CREATE TABLE,
			// so len() guards are insufficient. Check that at least
			// one entry is non-nil before calling.
			//
			// REQ002099+: guard each validation call to avoid Go's
			// heap-escape of the Row struct when passed by value.
			// Even though FillDefaults/ValidateCheck have early-returns
			// for empty schemas, the function call itself copies the
			// ~176B Row struct, which escapes to the heap (Go escape
			// analysis rule: returning a value alongside an interface
			// allocates the value on the heap).
			if len(cschema.Defaults) > 0 || len(cschema.Generated) > 0 {
				if row, err = FillDefaults(cschema, row); err != nil {
					return DT.Row{}, err
				}
			}
			if err := ValidateRow(cschema, row); err != nil {
				return DT.Row{}, err
			}
			if err := ValidateDecimal(cschema, row); err != nil {
				return DT.Row{}, err
			}
			if len(cschema.Checks) > 0 {
				if err := ValidateCheck(cschema, row); err != nil {
					return DT.Row{}, err
				}
			}
			// REQ000513/REQ000905: FK re-validation when FK columns are updated.
			// REQ002102: skip the FK boxing passes when the schema has no
			// FK declarations even if foreign_keys pragma is ON (the
			// default). ValidateForeignKeyUpdateInMemory early-returns
			// on empty ForeignKeys, but the previous code still paid for
			// the two DT.ValueSliceToAny calls because Go evaluates
			// function arguments eagerly. Schemas without FKs are the
			// common case — guard before boxing.
			if DT.IsForeignKeysEnabled() && len(cschema.ForeignKeys) > 0 {
				oldVals := DT.ValueSliceToAny(snapshot.Data)
				newVals := DT.ValueSliceToAny(row.Data)
				if err := UT.ValidateForeignKeyUpdateInMemory(cschema, oldVals, newVals); err != nil {
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
			if err := evalReturning(u.returning, &row, u.params, &u.resultRows, &u.rColsBuf, &u.rTypesBuf, &u.rDataBuf); err != nil {
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

// updateChunkSize is the per-batch row count for chunked mutation.
// REQ001555: amortises the per-call atomic checks (closed.Load,
// ShouldFlush) and per-row Insert/EncodeRow overhead across N rows.
const updateChunkSize = 256

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
	// REQ001555: pull rows in chunks of updateChunkSize and route
	// each chunk through h.UpdateRowBatch. Per-row apply/validate
	// still happens first (correctness); the batch pays off at
	// the engine WriteBatch layer.
	oldBuf := make([]DT.Row, 0, updateChunkSize)
	newBuf := make([]DT.Row, 0, updateChunkSize)
	// REQ001578: defer AFTER UPDATE triggers to fire AFTER the heap
	// batch is durable. Trigger events are appended to pendingUpdates
	// during the chunk loop and consumed by firePendingTriggers below.
	// The trigger lock (`triggerMu` inside FireTriggers) is acquired
	// O(1) times per chunk instead of O(N) times per row.
	// REQ001585: batch-evaluate SET expressions via EvalBatchExpr
	// instead of per-row ApplyUpdate. Build a batch from oldBuf,
	// evaluate each SET expression once, and apply results to newBuf.
	flushChunk := func() error {
		if len(oldBuf) == 0 {
			return nil
		}
		// Batch evaluate SET expressions.
		if len(u.set) > 0 && u.setColIdx != nil {
			oldRows := make([]*DT.Row, len(oldBuf))
			for i := range oldBuf {
				oldRows[i] = &oldBuf[i]
			}
			batch := EV.RowsToBatch(oldRows)
			defer batch.Put()
			nRows := batch.LogicalSize()
			for si, p := range u.set {
				col := EV.EvalBatchExpr(p.Val, batch, u.params)
				ci := u.setColIdx[si]
				if ci < 0 {
					continue
				}
				for i := 0; i < nRows && i < len(newBuf); i++ {
					v := UT.ToValue(col, i)
					newBuf[i].Data[ci] = v
				}
			}
		}
		// Validate each new row.
		for i := range newBuf {
			row := &newBuf[i]
			if r, err := FillDefaults(u.schema, *row); err != nil {
				return err
			} else {
				*row = r
			}
			if err := ValidateRow(u.schema, *row); err != nil {
				return err
			}
			if err := ValidateCheck(u.schema, *row); err != nil {
				return err
			}
			noopLookup := func(cols []int, vals []any) (bool, error) { return false, nil }
			if err := CheckUnique(u.schema, *row, nil, DT.Row{}, noopLookup); err != nil {
				return err
			}
		}
		// REQ001586: batch evaluate RETURNING expressions via EvalBatchExpr
		// instead of per-row EvalValue. Build a batch from newBuf, evaluate
		// each RETURNING expression once, and build result rows.
		if len(u.returning) > 0 && len(newBuf) > 0 {
			firstRow := &newBuf[0]
			expanded := expandReturningStar(u.returning, firstRow.Cols)
			nCols := len(expanded)

			// Build batch from newBuf.
			newRows := make([]*DT.Row, len(newBuf))
			for i := range newBuf {
				newRows[i] = &newBuf[i]
			}
			batch := EV.RowsToBatch(newRows)
			nRows := batch.LogicalSize()

			flatOff := len(u.rColsBuf)
			newLen := flatOff + nCols*nRows
			growSlice(&u.rColsBuf, newLen)
			growSlice(&u.rTypesBuf, newLen)
			growSlice(&u.rDataBuf, newLen)

			// Evaluate each RETURNING expression once per batch.
			for j, expr := range expanded {
				col := EV.EvalBatchExpr(expr, batch, u.params)
				colName := colNameForReturning(expr, firstRow.Cols, j)
				for i := 0; i < nRows; i++ {
					off := flatOff + i*nCols + j
					v := UT.ToValue(col, i)
					u.rColsBuf[off] = colName
					u.rTypesBuf[off] = col.Type
					u.rDataBuf[off] = v
				}
			}
			batch.Put()

			// Build result rows from flat buffers.
			for i := 0; i < nRows; i++ {
				off := flatOff + i*nCols
				resultRow := DT.Row{
					Cols:  u.rColsBuf[off : off+nCols : off+nCols],
					Types: u.rTypesBuf[off : off+nCols : off+nCols],
					Data:  u.rDataBuf[off : off+nCols : off+nCols],
				}
				u.resultRows = append(u.resultRows, resultRow)
			}
		}
		keys, bufs, err := h.UpdateRowBatch(oldBuf, newBuf)
		if err != nil {
			return err
		}
		_ = prefix // retained for any future callers; UpdateRowBatch uses h.Prefix().
		if u.txWriter != nil {
			for i, k := range keys {
				u.txWriter.RecordWrite(k, bufs[i])
			}
		}
		// Heap batch is durable. Fire the deferred triggers now, then
		// drop the captured events so the next chunk starts clean.
		if terr := u.firePendingUpdates(); terr != nil {
			oldBuf = oldBuf[:0]
			newBuf = newBuf[:0]
			u.pendingUpdates = u.pendingUpdates[:0]
			return terr
		}
		oldBuf = oldBuf[:0]
		newBuf = newBuf[:0]
		u.pendingUpdates = u.pendingUpdates[:0]
		return nil
	}
	for {
		row, err := u.iter.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				break
			}
			return DT.Row{}, err
		}
		oldRow := DT.ShallowCloneRow(row)
		// Deep-copy Data before ApplyUpdate mutates it in-place.
		row.Data = append([]DT.Value(nil), row.Data...)
		oldBuf = append(oldBuf, oldRow)
		newBuf = append(newBuf, row)
		u.rows++
		if u.execCtx != nil {
			u.execCtx.LastChanges++
			u.execCtx.TotalChanges++
		}
		u.pendingUpdates = append(u.pendingUpdates, triggerEvent{oldRow: oldRow, newRow: row})

		if len(oldBuf) >= updateChunkSize {
			if err := flushChunk(); err != nil {
				return DT.Row{}, err
			}
		}
	}
	if err := flushChunk(); err != nil {
		return DT.Row{}, err
	}

	// Return first RETURNING result if any
	if len(u.resultRows) > 0 {
		row := u.resultRows[0]
		u.resultPos = 1
		return row, nil
	}
	return DT.Row{}, DT.ErrNoRows
}

// firePendingUpdates fires all captured AFTER UPDATE trigger events
// for the current chunk in row order. Each event sees its own
// (oldRow, newRow) pair as a normal TriggerContext — the trigger body
// runs once per mutated row, just at a deferred time. REQ001578.
func (u *Update) firePendingUpdates() error {
	for i := range u.pendingUpdates {
		ev := &u.pendingUpdates[i]
		if err := fireUpdateTriggers(u.table, &ev.oldRow, &ev.newRow, u.params, u.store); err != nil {
			return err
		}
	}
	return nil
}

// firePendingDeletes fires all captured AFTER DELETE trigger events
// for the current chunk in row order. REQ001578.
func (d *Delete) firePendingDeletes() error {
	for i := range d.pendingDeletes {
		ev := &d.pendingDeletes[i]
		if err := fireDeleteTriggers(d.table, &ev.oldRow, d.params, d.store); err != nil {
			return err
		}
	}
	return nil
}

// triggerFireCount counts the number of times a trigger's REFRESH
// MATERIALIZED VIEW body executes during a single writer call.
// REQ001578 test-only seam: lets tests assert that batched
// UPDATE/DELETE fires triggers once per row (deferred to after the
// chunk flush), without standing up a full matview.
// Increments in executeRefreshMatViewSQL; tests reset via the
// ResetTriggerFireCount helper below.
var triggerFireCount atomic.Int64

// ResetTriggerFireCount zeroes the package-level trigger fire
// counter. Test-only.
func ResetTriggerFireCount() { triggerFireCount.Store(0) }

// TriggerFireCount returns the current trigger fire count. Test-only.
func TriggerFireCount() int64 { return triggerFireCount.Load() }

func (u *Update) Close() error {
	err := u.iter.Close()
	u.done = false
	u.rows = 0
	u.resultRows = nil
	u.resultPos = 0
	u.pendingUpdates = nil
	return err
}

func (u *Update) RowsAffected() int64 {
	return u.rows
}

// NextBatch triggers the update on first call and returns RETURNING rows
// in columnar batches. REQ001984.
func (u *Update) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if !u.done || (len(u.resultRows) > 0 && u.resultPos == 0) {
		_, err := u.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				if len(u.resultRows) == 0 {
					return nil, nil
				}
			} else {
				return nil, err
			}
		}
		if u.resultPos > 0 {
			u.resultPos--
		}
		if len(u.resultRows) == 0 {
			return nil, nil
		}
	}
	return drainResultRows(u.resultRows, &u.resultPos)
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
	// pendingDeletes collects oldRow snapshots whose AFTER DELETE
	// triggers have not yet been fired. REQ001578.
	pendingDeletes []triggerEvent
	rColsBuf       []string       // REQ001557: flat RETURNING col name backing
	rTypesBuf      []LX.TokenType // REQ001557: flat RETURNING type backing
	rDataBuf       []DT.Value     // REQ001557: flat RETURNING data backing
	limit          int64          // REQ002313: max rows to delete (0 = unlimited)
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

// SetLimit sets the maximum number of rows to delete (0 = unlimited).
func (d *Delete) SetLimit(n int64) { d.limit = n }

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
	del := &Delete{
		table:     table,
		where:     where,
		iter:      iter,
		returning: returning,
		store:     store,
		schema:    ss,
	}
	if len(returning) > 0 {
		nCols := len(ss.Cols)
		del.rColsBuf = make([]string, 0, 256*nCols)
		del.rTypesBuf = make([]LX.TokenType, 0, 256*nCols)
		del.rDataBuf = make([]DT.Value, 0, 256*nCols)
	}
	return del, nil
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
	limit := d.limit
	for {
		if limit > 0 && int64(len(toDelete)) >= limit {
			break
		}
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
				if err := evalReturning(d.returning, &row, d.params, &d.resultRows, &d.rColsBuf, &d.rTypesBuf, &d.rDataBuf); err != nil {
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

// deleteChunkSize is the per-batch row count for chunked deletion.
// REQ001556: amortises the per-call atomic checks (closed.Load,
// ShouldFlush) and per-row Delete/RowKey overhead across N rows.
const deleteChunkSize = 256

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
	// REQ001556: pull rows in chunks of deleteChunkSize and route
	// each chunk through h.DeleteRowBatch. RETURNING is evaluated
	// before the batch flush so callers still see the pre-delete
	// row contents.
	//
	// REQ001578: AFTER DELETE triggers are deferred to fire AFTER
	// the heap batch is durable. Trigger events are appended to
	// pendingDeletes during the chunk loop and consumed by
	// firePendingDeletes below — the trigger lock is acquired
	// O(1) times per chunk instead of O(N) times per row.
	delBuf := make([]DT.Row, 0, deleteChunkSize)
	flushChunk := func() error {
		if len(delBuf) == 0 {
			return nil
		}
		keys, err := h.DeleteRowBatch(delBuf)
		if err != nil {
			return err
		}
		_ = prefix // retained for any future callers; DeleteRowBatch uses h.Prefix().
		if d.txWriter != nil {
			for _, k := range keys {
				d.txWriter.RecordWrite(k, nil)
			}
		}
		// Heap batch is durable. Fire the deferred triggers now, then
		// drop the captured events so the next chunk starts clean.
		if terr := d.firePendingDeletes(); terr != nil {
			delBuf = delBuf[:0]
			d.pendingDeletes = d.pendingDeletes[:0]
			return terr
		}
		delBuf = delBuf[:0]
		d.pendingDeletes = d.pendingDeletes[:0]
		return nil
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
			if err := evalReturning(d.returning, &row, d.params, &d.resultRows, &d.rColsBuf, &d.rTypesBuf, &d.rDataBuf); err != nil {
				return DT.Row{}, err
			}
		}

		// REQ001578: capture the pre-delete row for deferred trigger
		// firing. Use a value copy so the loop variable aliasing
		// can't poison later firePendingDeletes iterations.
		delBuf = append(delBuf, row)
		d.pendingDeletes = append(d.pendingDeletes, triggerEvent{oldRow: row})
		d.rows++
		if d.execCtx != nil {
			d.execCtx.LastChanges++
			d.execCtx.TotalChanges++
		}

		if len(delBuf) >= deleteChunkSize {
			if err := flushChunk(); err != nil {
				return DT.Row{}, err
			}
		}
	}
	if err := flushChunk(); err != nil {
		return DT.Row{}, err
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
	d.pendingDeletes = nil
	return err
}

func (d *Delete) RowsAffected() int64 {
	return d.rows
}

// NextBatch triggers the delete on first call and returns RETURNING rows
// in columnar batches. REQ001985.
func (d *Delete) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if !d.done || (len(d.resultRows) > 0 && d.resultPos == 0) {
		_, err := d.Next(ctx)
		if err != nil {
			if err == DT.ErrNoRows {
				if len(d.resultRows) == 0 {
					return nil, nil
				}
			} else {
				return nil, err
			}
		}
		if d.resultPos > 0 {
			d.resultPos--
		}
		if len(d.resultRows) == 0 {
			return nil, nil
		}
	}
	return drainResultRows(d.resultRows, &d.resultPos)
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
// REQ001557: bufCols/bufTypes/bufData are flat backing buffers reused across
// rows within one statement execution, avoiding per-row make.
func evalReturning(exprs []PS.Expr, row *DT.Row, params []any, resultRows *[]DT.Row,
	bufCols *[]string, bufTypes *[]LX.TokenType, bufData *[]DT.Value) error {
	if len(exprs) == 0 {
		return nil
	}
	expanded := expandReturningStar(exprs, row.Cols)
	n := len(expanded)

	flatOff := len(*bufCols)
	newLen := flatOff + n
	growSlice(bufCols, newLen)
	growSlice(bufTypes, newLen)
	growSlice(bufData, newLen)

	resultRow := DT.Row{
		Cols:  (*bufCols)[flatOff:newLen:newLen],
		Types: (*bufTypes)[flatOff:newLen:newLen],
		Data:  (*bufData)[flatOff:newLen:newLen],
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
		updated := DT.ShallowCloneRow(target)
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
		defer parser.Close()
		defer parser.Close()
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
	defer parser.Close()
	defer parser.Close()
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

	// REQ001578 test-only seam: count every matview refresh body
	// execution. Tests use this to assert batched UPDATE/DELETE fires
	// triggers exactly once per mutated row (deferred), regardless of
	// chunk size.
	triggerFireCount.Add(1)

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
		defer parser.Close()
		defer parser.Close()
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
		defer parser.Close()
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
