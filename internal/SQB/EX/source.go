package EX

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// triggerMu guards the package-level trigger registry. REQ000435.
var (
	triggerMu     sync.RWMutex
	triggerReg    = map[string]*PS.TriggerStmt{}   // by trigger name
	tableTriggers = map[string][]*PS.TriggerStmt{} // by table name
)

func registerTrigger(t *PS.TriggerStmt) error {
	triggerMu.Lock()
	defer triggerMu.Unlock()
	if _, exists := triggerReg[t.Name]; exists {
		return fmt.Errorf("ex: trigger %q already exists", t.Name)
	}
	triggerReg[t.Name] = t
	tableTriggers[t.OnTable] = append(tableTriggers[t.OnTable], t)
	return nil
}

// unregisterTrigger removes a single trigger by name. Returns true if
// the trigger existed. REQ000496.
func unregisterTrigger(name string) bool {
	triggerMu.Lock()
	defer triggerMu.Unlock()
	t, ok := triggerReg[name]
	if !ok {
		return false
	}
	delete(triggerReg, name)
	if list, ok := tableTriggers[t.OnTable]; ok {
		filtered := list[:0]
		for _, x := range list {
			if x.Name != t.Name {
				filtered = append(filtered, x)
			}
		}
		if len(filtered) == 0 {
			delete(tableTriggers, t.OnTable)
		} else {
			tableTriggers[t.OnTable] = filtered
		}
	}
	return true
}

func triggersForTable(table string) []*PS.TriggerStmt {
	triggerMu.RLock()
	defer triggerMu.RUnlock()
	out := make([]*PS.TriggerStmt, len(tableTriggers[table]))
	copy(out, tableTriggers[table])
	return out
}

// UnregisterAll clears all registered state for test isolation.
func UnregisterAll() {
	DT.TablesMu.Lock()
	DT.Tables = map[string][]Row{}
	DT.Schemas = map[string][]string{}
	DT.StoreMu.Lock()
	DT.StoreSchemas = map[uint64]*DT.StoreSchema{}
	DT.TableIDs = map[string]uint64{}
	DT.InMemSchemas = map[string]*DT.StoreSchema{}
	DT.TableIDSeq = 0
	DT.CurrentCatalog.Store(nil)
	DT.RegisteredIndexes = map[string][]RegisteredIndex{}
	DT.ViewRegistry = map[string]*PS.Select{}
	DT.MatViewRegistry = map[string]*PS.Select{}
	DT.StoreMu.Unlock()
	triggerMu.Lock()
	triggerReg = map[string]*PS.TriggerStmt{}
	tableTriggers = map[string][]*PS.TriggerStmt{}
	triggerMu.Unlock()
	DT.TablesMu.Unlock()
	// Clear table schema cache for test isolation.
	OP.TableSchemaMu.Lock()
	OP.TableSchemaCache = map[string]*OP.TableSchemaEntry{}
	OP.TableSchemaMu.Unlock()
	// Clear subquery caches for test isolation.
	EV.ClearSubqueryCaches()
}

// buildInsertRow materializes an INSERT row from values. The
// params slice is forwarded to Eval for `?` placeholder
// resolution (R16-1..2). When params is nil the slice is a
// no-op and `?` placeholders resolve to nil (preserving the
// pre-iter-16 behavior for callers that do not bind args).
func buildInsertRow(schema []string, cols []string, colIdx []int, values []PS.Expr, params []any) (Row, error) {
	// REQ001029: share schema slice across all rows — it's read-only.
	out := Row{Cols: schema}
	if len(cols) == 0 {
		out.Data = make([]Value, len(values))
		for i, v := range values {
			val, err := EV.EvalValue(v, nil, params)
			if err != nil {
				return Row{}, err
			}
			out.Data[i] = val
		}
		return out, nil
	}
	// REQ000774: pre-compute column-name-to-schema-index mapping
	// to avoid per-row map allocation (case-insensitive match).
	// REQ001030: colIdx is pre-computed by caller and passed in.
	out.Data = make([]Value, len(schema))
	for i := range cols {
		val, err := EV.EvalValue(values[i], nil, params)
		if err != nil {
			return Row{}, err
		}
		if colIdx[i] >= 0 {
			// REQ001030: colIdx is pre-computed by caller and passed in.
			out.Data[colIdx[i]] = val
		}
	}
	return out, nil
}

// applyUpdate evaluates SET expressions against the current
// row, forwarding params for `?` placeholder resolution
// (R16-1..2).
func applyUpdate(row *Row, set []PS.Pair, params []any) error {
	for _, p := range set {
		val, err := EV.EvalValue(p.Val, row, params)
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
// REQ000741: evaluates the WHEN expression before executing the body.
func executeTrigger(trigger *PS.TriggerStmt, ctx *TriggerContext) error {
	// Build a synthetic row for NEW/OLD references in the WHEN expression.
	var whenRow *Row
	if trigger.When != nil {
		whenRow = buildTriggerWhenRow(ctx)
		ok, err := evalTriggerWhen(trigger.When, whenRow, ctx.Params)
		if err != nil {
			return fmt.Errorf("ex: trigger WHEN evaluation: %w", err)
		}
		if !ok {
			return nil // WHEN condition false, skip body
		}
	}

	for _, stmt := range trigger.Body {
		if err := executeTriggerStmt(stmt, ctx); err != nil {
			return err
		}
	}
	return nil
}

// buildTriggerWhenRow builds a synthetic Row that allows QualifiedName
// lookups for NEW.col and OLD.col references in trigger WHEN expressions.
func buildTriggerWhenRow(ctx *TriggerContext) *Row {
	var data []Value
	var cols []string
	if ctx.NewRow != nil {
		for i, c := range ctx.NewRow.Cols {
			data = append(data, ctx.NewRow.Data[i])
			cols = append(cols, "NEW."+c)
		}
	}
	if ctx.OldRow != nil {
		for i, c := range ctx.OldRow.Cols {
			// REQ001023: stack-friendly fast path for OLD row construction.
			data = append(data, ctx.OldRow.Data[i])
			cols = append(cols, "OLD."+c)
		}
	}
	return &Row{Data: data, Cols: cols}
}

// evalTriggerWhen evaluates a trigger WHEN expression against the
// synthetic NEW/OLD row. Returns true if the condition passes.
func evalTriggerWhen(expr PS.Expr, row *Row, params []any) (bool, error) {
	val, err := EV.EvalValue(expr, row, params)
	if err != nil {
		return false, err
	}
	if val.Kind == KindNull {
		return false, nil
	}
	if val.Kind == KindBool {
		return val.Bo, nil
	}
	return false, nil
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