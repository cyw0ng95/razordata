package WT

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	OP "github.com/cyw0ng95/razordata/internal/SQB/OP"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	VL "github.com/cyw0ng95/razordata/internal/TXN/VL"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// cellSizeVerifier is the optional interface for cell_size_check.
// The store adapter may implement this to verify SST block sizes.
// REQ001387.
type cellSizeVerifier interface {
	VerifyCellSizes() []string
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

func (t *Trigger) Close() error                   { return nil }
func (t *Trigger) WithParams(p []any) DT.Operator { return t }
func (t *Trigger) RowsAffected() int64            { return 0 }

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
	if p.Stmt.Name == "wal_checkpoint" {
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

	// Handle PRAGMA wal_autocheckpoint = N (REQ001300)
	if p.Stmt.Name == "wal_autocheckpoint" {
		if !p.done {
			p.done = true
			if p.Stmt.Value != "" {
				if n, err := strconv.ParseInt(p.Stmt.Value, 10, 64); err == nil {
					DT.SetWalAutocheckpoint(n)
				}
			}
			val := DT.GetWalAutocheckpoint()
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"wal_autocheckpoint"},
				Data: []DT.Value{DT.NewIntValue(val)},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA busy_timeout = N (REQ001301)
	if p.Stmt.Name == "busy_timeout" {
		if !p.done {
			p.done = true
			if p.Stmt.Value != "" {
				if n, err := strconv.ParseInt(p.Stmt.Value, 10, 64); err == nil {
					DT.SetBusyTimeout(n)
				}
			}
			val := DT.GetBusyTimeout()
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"busy_timeout"},
				Data: []DT.Value{DT.NewIntValue(val)},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA busy_handler = name (REQ001302)
	if p.Stmt.Name == "busy_handler" {
		if !p.done {
			p.done = true
			// Match the foreign_keys pattern: only write when Value is
			// non-empty. Reading the PRAGMA (no = clause) leaves state
			// untouched. To clear, the embedder can use the Go-level
			// DT.SetBusyHandler("", nil) hook — SQL cannot unambiguously
			// distinguish "clear" from "read".
			if p.Stmt.Value != "" {
				name := p.Stmt.Value
				// Register a callback under the given name that maps
				// the SQL PRAGMA setting onto the VL busyHandler hook.
				// The callback honors busy_timeout as its wait budget.
				DT.SetBusyHandler(name, busyHandlerAdapter(name))
				VL.SetBusyHandler(adaptBusyHandler(name))
			}
			cur, _ := DT.GetBusyHandler()
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"busy_handler"},
				Data: []DT.Value{DT.NewTextValue(cur)},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA batch_size [= N] (REQ001224)
	if p.Stmt.Name == "batch_size" {
		if !p.done {
			p.done = true
			if p.Stmt.Value != "" {
				// Write: set the batch size
				if n, err := strconv.Atoi(p.Stmt.Value); err == nil {
					OP.SetEngineBatchSize(n)
				}
			}
			// Read: return current value
			p.rows = append(p.rows, DT.Row{
				Cols: []string{"batch_size"},
				Data: []DT.Value{DT.NewTextValue(strconv.Itoa(OP.EngineBatchSize()))},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA foreign_keys [= ON|OFF] (REQ000905, REQ001307)
	if p.Stmt.Name == "foreign_keys" {
		if !p.done {
			p.done = true
			if p.Stmt.Value != "" {
				// REQ001307: PRAGMA foreign_keys is a no-op inside a transaction.
				// SQLite requires this to be set outside a transaction.
				if DT.CurrentTxWriter() == nil {
					val := strings.ToUpper(p.Stmt.Value)
					DT.SetForeignKeysEnabled(val == "ON" || val == "1" || val == "TRUE")
				}
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

	// REQ001271: route debug_* PRAGMAs to HandleDebugPragma.
	name := p.Stmt.Name
	if strings.HasPrefix(name, "debug_") {
		if !p.done {
			p.done = true
			var args []string
			if p.Stmt.Value != "" {
				args = []string{p.Stmt.Value}
			}
			result, err := UT.HandleDebugPragma(name, args)
			if err != nil {
				return DT.Row{}, err
			}
			p.rows = append(p.rows, DT.Row{
				Cols: []string{name},
				Data: []DT.Value{DT.NewTextValue(result)},
			})
		}
		if p.idx >= len(p.rows) {
			return DT.Row{}, DT.ErrNoRows
		}
		row := p.rows[p.idx]
		p.idx++
		return row, nil
	}

	// Handle PRAGMA cell_size_check (REQ001387)
	if p.Stmt.Name == "cell_size_check" {
		if !p.done {
			p.done = true
			if v, ok := p.store.(cellSizeVerifier); ok {
				errors := v.VerifyCellSizes()
				for _, errMsg := range errors {
					p.rows = append(p.rows, DT.Row{
						Cols: []string{"cell_size_check"},
						Data: []DT.Value{DT.NewTextValue(errMsg)},
					})
				}
				if len(p.rows) == 0 {
					p.rows = append(p.rows, DT.Row{
						Cols: []string{"cell_size_check"},
						Data: []DT.Value{DT.NewTextValue("ok")},
					})
				}
			} else {
				p.rows = append(p.rows, DT.Row{
					Cols: []string{"cell_size_check"},
					Data: []DT.Value{DT.NewTextValue("ok")},
				})
			}
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
func (p *Pragma) RowsAffected() int64            { return 0 }

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

func (e *Explain) Close() error                   { return nil }
func (e *Explain) WithParams(_ []any) DT.Operator { return e }
func (e *Explain) RowsAffected() int64            { return 0 }

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

func (t *Truncate) Close() error                   { return nil }
func (t *Truncate) WithParams(_ []any) DT.Operator { return t }
func (t *Truncate) RowsAffected() int64            { return t.rows }

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

func (r *Reindex) Close() error                   { return nil }
func (r *Reindex) WithParams(_ []any) DT.Operator { return r }
func (r *Reindex) RowsAffected() int64            { return 0 }

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

func (d *DropView) Close() error                   { return nil }
func (d *DropView) WithParams(_ []any) DT.Operator { return d }
func (d *DropView) RowsAffected() int64            { return 0 }

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

func (d *DropTrigger) Close() error                   { return nil }
func (d *DropTrigger) WithParams(_ []any) DT.Operator { return d }
func (d *DropTrigger) RowsAffected() int64            { return 0 }

type UnsupportedOp struct {
	err  error
	done bool
	Stmt PS.Stmt
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

func (u *UnsupportedOp) Close() error                   { return nil }
func (u *UnsupportedOp) WithParams(_ []any) DT.Operator { return u }
func (u *UnsupportedOp) RowsAffected() int64            { return 0 }

// InsteadOfInsert executes an INSTEAD OF INSERT trigger when
// inserting into a view. REQ001366.
type InsteadOfInsert struct {
	table   string
	trigger *PS.TriggerStmt
	done    bool
	rows    int64
}

func NewInsteadOfInsert(table string, trigger *PS.TriggerStmt) *InsteadOfInsert {
	return &InsteadOfInsert{table: table, trigger: trigger}
}

func (o *InsteadOfInsert) Next(ctx context.Context) (DT.Row, error) {
	if o.done {
		return DT.Row{}, DT.ErrNoRows
	}
	o.done = true
	o.rows = 1
	if o.trigger != nil {
		err := ExecuteTrigger(o.trigger, &TriggerContext{})
		if err != nil {
			return DT.Row{}, err
		}
	}
	return DT.Row{}, DT.ErrNoRows
}

func (o *InsteadOfInsert) Close() error                   { return nil }
func (o *InsteadOfInsert) WithParams(_ []any) DT.Operator { return o }
func (o *InsteadOfInsert) RowsAffected() int64            { return o.rows }

func NewInsteadOfUpdate(table string, trigger *PS.TriggerStmt) *InsteadOfInsert {
	return &InsteadOfInsert{table: table, trigger: trigger}
}

func NewInsteadOfDelete(table string, trigger *PS.TriggerStmt) *InsteadOfInsert {
	return &InsteadOfInsert{table: table, trigger: trigger}
}

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
func (a *AttachOp) RowsAffected() int64            { return 0 }

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
func (d *DetachOp) RowsAffected() int64            { return 0 }

// ErrMultiDatabaseNotSupported is returned when a query attempts to
// reference an attached database. Full cross-database query support
// (SELECT from attached.t, etc.) is deferred. REQ000908.
var ErrMultiDatabaseNotSupported = errors.New("wt: cross-database queries not supported in v1")

// busyHandlerAdapter returns a DT.BusyHandlerFunc that uses the
// active busy_timeout as its retry budget and returns false after
// the budget elapses. REQ001302.
func busyHandlerAdapter(_ string) DT.BusyHandlerFunc {
	return func(attempt int) bool {
		budget := time.Duration(DT.GetBusyTimeout()) * time.Millisecond
		if budget <= 0 {
			return false
		}
		// back-off: 1ms, 2ms, 4ms, ... capped at 50ms; return true
		// until the budget is exhausted.
		wait := time.Duration(1<<min(attempt-1, 6)) * time.Millisecond
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		time.Sleep(wait)
		return time.Duration(attempt)*wait <= budget
	}
}

// adaptBusyHandler returns a VL-shaped busy handler that honors the
// registered DT-level handler. We bridge by re-using the DT-level
// callback's retry decision and computing the wait duration locally.
// REQ001302.
func adaptBusyHandler(name string) func(attempt int) (time.Duration, bool) {
	return func(attempt int) (time.Duration, bool) {
		_, fn := DT.GetBusyHandler()
		if fn == nil {
			return 0, false
		}
		ok := fn(attempt)
		if !ok {
			return 0, false
		}
		wait := time.Duration(1<<min(attempt-1, 6)) * time.Millisecond
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		_ = name
		return wait, true
	}
}
