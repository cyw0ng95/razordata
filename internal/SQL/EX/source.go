package EX

import (
	"errors"
	"strings"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

var errTableExists = errors.New("ex: table already exists")

var (
	tablesMu sync.RWMutex
	tables   = map[string][]Row{}
	schemas  = map[string][]string{}
	tablePKs = map[string]string{} // in-memory table primary key column name
)

// triggerMu guards the package-level trigger registry. REQ000435.
var (
	triggerMu     sync.RWMutex
	triggerReg    = map[string]*PS.TriggerStmt{}   // by trigger name
	tableTriggers = map[string][]*PS.TriggerStmt{} // by table name
)

func registerTrigger(t *PS.TriggerStmt) {
	triggerMu.Lock()
	defer triggerMu.Unlock()
	triggerReg[t.Name] = t
	tableTriggers[t.OnTable] = append(tableTriggers[t.OnTable], t)
}

// unregisterTrigger removes a single trigger by name. REQ000496.
func unregisterTrigger(name string) {
	triggerMu.Lock()
	defer triggerMu.Unlock()
	t, ok := triggerReg[name]
	if !ok {
		return
	}
	delete(triggerReg, name)
	if list, ok := tableTriggers[t.OnTable]; ok {
		filtered := list[:0]
		for _, x := range list {
			if x.Name != name {
				filtered = append(filtered, x)
			}
		}
		if len(filtered) == 0 {
			delete(tableTriggers, t.OnTable)
		} else {
			tableTriggers[t.OnTable] = filtered
		}
	}
}

func triggersForTable(table string) []*PS.TriggerStmt {
	triggerMu.RLock()
	defer triggerMu.RUnlock()
	out := make([]*PS.TriggerStmt, len(tableTriggers[table]))
	copy(out, tableTriggers[table])
	return out
}

func RegisterTable(name string, rows []Row) {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	cp := make([]Row, len(rows))
	for i, r := range rows {
		cp[i] = cloneRow(r)
	}
	tables[name] = cp
	if len(rows) > 0 {
		schemas[name] = append([]string(nil), rows[0].Cols...)
	}
}

func RegisterTableSchema(name string, cols []string) {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	if _, ok := tables[name]; !ok {
		tables[name] = []Row{}
	}
	schemas[name] = append([]string(nil), cols...)
}

func Schema(name string) []string {
	tablesMu.RLock()
	defer tablesMu.RUnlock()
	if s, ok := schemas[name]; ok {
		return append([]string(nil), s...)
	}
	return nil
}

// SnapshotInMemoryTable returns a deep copy of the in-memory table's
// current rows. The caller must hold tablesMu (read or write).
// REQ000641.
func SnapshotInMemoryTable(table string) []Row {
	src := tables[table]
	if src == nil {
		return nil
	}
	cp := make([]Row, len(src))
	for i, r := range src {
		cp[i] = cloneRow(r)
	}
	return cp
}

// RestoreInMemoryTables replaces in-memory tables with the given
// snapshots. Used by TX.Transaction.Rollback to undo in-memory
// writes. REQ000641.
func RestoreInMemoryTables(snapshots map[string][]Row) error {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	for table, snap := range snapshots {
		tables[table] = snap
	}
	return nil
}

func UnregisterAll() {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	tables = map[string][]Row{}
	schemas = map[string][]string{}
	storeMu.Lock()
	storeSchemas = map[uint64]*storeSchema{}
	tableIDs = map[string]uint64{}
	inMemSchemas = map[string]*storeSchema{}
	tableIDSeq = 0
	currentCatalog.Store(nil)
	registeredIndexes = map[string][]RegisteredIndex{}
	viewRegistry = map[string]*PS.Select{}
	matViewRegistry = map[string]*PS.Select{}
	storeMu.Unlock()
	triggerMu.Lock()
	triggerReg = map[string]*PS.TriggerStmt{}
	tableTriggers = map[string][]*PS.TriggerStmt{}
	triggerMu.Unlock()
	// Clear table schema cache for test isolation.
	tableSchemaMu.Lock()
	tableSchemaCache = map[string]*tableSchemaEntry{}
	tableSchemaMu.Unlock()
	// Clear subquery caches for test isolation.
	ClearSubqueryCaches()
}

// UnregisterTable removes a single table from the in-memory
// tables and schemas maps. Used by CTE cleanup to remove
// temporary CTE tables after query execution.
func UnregisterTable(name string) {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	delete(tables, name)
	delete(schemas, name)
	// Clear table schema cache entry.
	tableSchemaMu.Lock()
	delete(tableSchemaCache, name)
	tableSchemaMu.Unlock()
}

func cloneRow(r Row) Row {
	out := Row{Cols: append([]string(nil), r.Cols...), Types: append([]int(nil), r.Types...), Outer: r.Outer, planner: r.planner, storeKey: r.storeKey, tableName: r.tableName}
	if r.Data != nil {
		out.Data = append([]any(nil), r.Data...)
	}
	return out
}

// buildInsertRow materializes an INSERT row from values. The
// params slice is forwarded to Eval for `?` placeholder
// resolution (R16-1..2). When params is nil the slice is a
// no-op and `?` placeholders resolve to nil (preserving the
// pre-iter-16 behavior for callers that do not bind args).
func buildInsertRow(schema []string, cols []string, values []PS.Expr, params []any) (Row, error) {
	out := Row{Cols: append([]string(nil), schema...)}
	if len(cols) == 0 {
		out.Data = make([]any, len(values))
		for i, v := range values {
			val, err := Eval(v, nil, params)
			if err != nil {
				return Row{}, err
			}
			out.Data[i] = val
		}
		return out, nil
	}
	byName := make(map[string]any, len(cols))
	for i, name := range cols {
		val, err := Eval(values[i], nil, params)
		if err != nil {
			return Row{}, err
		}
		byName[name] = val
	}
	out.Data = make([]any, len(schema))
	for i, name := range schema {
		if v, ok := byName[name]; ok {
			out.Data[i] = v
			continue
		}
		out.Data[i] = nil
	}
	return out, nil
}

// applyUpdate evaluates SET expressions against the current
// row, forwarding params for `?` placeholder resolution
// (R16-1..2).
func applyUpdate(row *Row, set []PS.Pair, params []any) error {
	for _, p := range set {
		val, err := Eval(p.Val, row, params)
		if err != nil {
			return err
		}
		idx := -1
		for i, c := range row.Cols {
			if c == p.Col {
				idx = i
				break
			}
		}
		if idx < 0 {
			return errors.New("ex: update column not found: " + p.Col)
		}
		row.Data[idx] = val
	}
	return nil
}

func replaceBySnapshot(table string, snapshot, updated Row) error {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	existing := tables[table]
	idx, ok := rowIndexLocked(existing, snapshot)
	if !ok {
		return errors.New("ex: row not found for update")
	}
	existing[idx] = updated
	tables[table] = existing
	return nil
}

func rowIndex(table string, row Row) (int, bool) {
	tablesMu.RLock()
	defer tablesMu.RUnlock()
	return rowIndexLocked(tables[table], row)
}

func rowIndexLocked(rows []Row, row Row) (int, bool) {
	for i, r := range rows {
		if rowEqual(r, row) {
			return i, true
		}
	}
	return -1, false
}

func rowEqual(a, b Row) bool {
	if len(a.Cols) != len(b.Cols) {
		return false
	}
	for i := range a.Cols {
		if !equalValue(a.Data[i], b.Data[i]) {
			return false
		}
	}
	return true
}

// TriggerContext provides the runtime context for trigger execution.
// It holds the old/new row values and the executor for running trigger body statements.
type TriggerContext struct {
	OldRow *Row // nil for INSERT
	NewRow *Row // nil for DELETE
	Params []any
	Exec   func(sql string) error // callback to execute SQL (for matview refresh)
}

// fireTriggers executes all triggers for the given table, time, and event.
// Returns an error if any trigger fails. For AFTER triggers, oldRow and newRow
// represent the state before and after the DML operation.
// REQ000316: AFTER triggers are used for incremental matview refresh.
func fireTriggers(table string, time string, event string, oldRow *Row, newRow *Row, params []any, exec func(sql string) error) error {
	triggerMu.RLock()
	triggers := make([]*PS.TriggerStmt, 0, len(tableTriggers[table]))
	for _, t := range tableTriggers[table] {
		if strings.EqualFold(t.OnTable, table) && strings.EqualFold(t.Event, event) && strings.EqualFold(t.Time, time) {
			triggers = append(triggers, t)
		}
	}
	triggerMu.RUnlock()

	if len(triggers) == 0 {
		return nil
	}

	ctx := &TriggerContext{
		OldRow: oldRow,
		NewRow: newRow,
		Params: params,
		Exec:   exec,
	}

	for _, trigger := range triggers {
		if err := executeTrigger(trigger, ctx); err != nil {
			return err
		}
	}
	return nil
}

// executeTrigger runs a single trigger's body statements.
func executeTrigger(trigger *PS.TriggerStmt, ctx *TriggerContext) error {
	for _, stmt := range trigger.Body {
		if err := executeTriggerStmt(stmt, ctx); err != nil {
			return err
		}
	}
	return nil
}

// executeTriggerStmt executes a single statement within a trigger body.
// For incremental matviews (REQ000316), the trigger body typically contains
// REFRESH MATERIALIZED VIEW to refresh the view on base table changes.
// Other trigger statements are not yet fully supported (existing no-op behavior).
func executeTriggerStmt(stmt PS.Stmt, ctx *TriggerContext) error {
	if ctx.Exec == nil {
		return errors.New("ex: trigger execution requires an executor")
	}

	// Only REFRESH MATERIALIZED VIEW is supported in trigger bodies for now
	if refresh, ok := stmt.(*PS.RefreshMatViewStmt); ok {
		return executeRefreshMatViewStmt(refresh, ctx)
	}

	// Other trigger body statements (INSERT/UPDATE/DELETE/SELECT) are no-ops
	// This maintains existing behavior; full trigger execution is deferred.
	return nil
}

// executeRefreshMatViewStmt executes a REFRESH MATERIALIZED VIEW statement.
func executeRefreshMatViewStmt(s *PS.RefreshMatViewStmt, ctx *TriggerContext) error {
	sql := "REFRESH MATERIALIZED VIEW " + s.Name
	return ctx.Exec(sql)
}
