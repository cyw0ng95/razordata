package WT

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// triggerMu guards the package-level trigger registry. REQ000435.
var (
	triggerMu     sync.RWMutex
	triggerReg    = map[string]*PS.TriggerStmt{}   // by trigger name
	tableTriggers = map[string][]*PS.TriggerStmt{} // by table name

	// insertDataPool holds reusable []Value slices for BuildInsertRow.
	// Each slice is pre-sized for the row width. REQ001426: eliminates
	// per-row make([]Value, N) in the hot path for multi-row INSERT.
	insertDataPool = sync.Pool{
		New: func() any { return make([]DT.Value, 0, 64) },
	}
)

// RegisterTrigger registers a trigger. Returns error if name already exists.
func RegisterTrigger(t *PS.TriggerStmt) error {
	triggerMu.Lock()
	defer triggerMu.Unlock()
	if _, exists := triggerReg[t.Name]; exists {
		return fmt.Errorf("ex: trigger %q already exists", t.Name)
	}
	triggerReg[t.Name] = t
	tableTriggers[t.OnTable] = append(tableTriggers[t.OnTable], t)
	// REQ001388: expose to sqlite_master via DT trigger registry.
	DT.RegisterTrigger(DT.TriggerInfo{
		Name:    t.Name,
		OnTable: t.OnTable,
	})
	return nil
}

// UnregisterTrigger removes a single trigger by name. Returns true if
// the trigger existed. REQ000496.
func UnregisterTrigger(name string) bool {
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
	DT.UnregisterTrigger(name)
	return true
}

// TriggersForTable returns all triggers registered for the given table.
func TriggersForTable(table string) []*PS.TriggerStmt {
	triggerMu.RLock()
	defer triggerMu.RUnlock()
	out := make([]*PS.TriggerStmt, len(tableTriggers[table]))
	copy(out, tableTriggers[table])
	return out
}

// ClearTriggerState clears all registered state for test isolation.
func ClearTriggerState() {
	triggerMu.Lock()
	triggerReg = map[string]*PS.TriggerStmt{}
	tableTriggers = map[string][]*PS.TriggerStmt{}
	triggerMu.Unlock()
}

// DropTriggersForTable removes all triggers associated with a table.
func DropTriggersForTable(tableName string) {
	triggerMu.Lock()
	defer triggerMu.Unlock()
	if triggers, ok := tableTriggers[tableName]; ok {
		for _, t := range triggers {
			delete(triggerReg, t.Name)
		}
		delete(tableTriggers, tableName)
	}
}

// IsTriggerRegistered checks if a trigger with the given name is registered.
func IsTriggerRegistered(name string) bool {
	triggerMu.RLock()
	defer triggerMu.RUnlock()
	_, ok := triggerReg[name]
	return ok
}

// BuildInsertRow materializes an INSERT row from values. The
// params slice is forwarded to Eval for `?` placeholder
// resolution (R16-1..2).
// BuildInsertRow constructs a Row from INSERT value expressions. When
// dataBuf is non-nil and has capacity >= len(schema), it is used as the
// Data slice backing array instead of allocating a new one. REQ001426.
func BuildInsertRow(schema []string, cols []string, colIdx []int, values []PS.Expr, params []any, dataBuf []DT.Value) (DT.Row, error) {
	out := DT.Row{Cols: schema}
	if len(cols) == 0 {
		if cap(dataBuf) >= len(values) {
			out.Data = dataBuf[:len(values)]
		} else {
			out.Data = make([]DT.Value, len(values))
		}
		for i, v := range values {
			val, err := EV.EvalValue(v, nil, params)
			if err != nil {
				return DT.Row{}, err
			}
			out.Data[i] = val
		}
		return out, nil
	}
	if cap(dataBuf) >= len(schema) {
		out.Data = dataBuf[:len(schema)]
	} else {
		out.Data = make([]DT.Value, len(schema))
	}
	for i := range cols {
		val, err := EV.EvalValue(values[i], nil, params)
		if err != nil {
			return DT.Row{}, err
		}
		if colIdx[i] >= 0 {
			out.Data[colIdx[i]] = val
		}
	}
	return out, nil
}

// ApplyUpdate evaluates SET expressions against the current
// row, forwarding params for `?` placeholder resolution.
func ApplyUpdate(row *DT.Row, set []PS.Pair, params []any) error {
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
type TriggerContext struct {
	OldRow *DT.Row // nil for INSERT
	NewRow *DT.Row // nil for DELETE
	Params []any
	Exec   func(sql string) error // callback to execute SQL (for matview refresh)
}

// FireTriggers executes all triggers for the given table, time, and event.
func FireTriggers(table string, time string, event string, oldRow *DT.Row, newRow *DT.Row, params []any, exec func(sql string) error) error {
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
		if err := ExecuteTrigger(trigger, ctx); err != nil {
			return err
		}
	}
	return nil
}

// ExecuteTrigger runs a single trigger's body statements.
func ExecuteTrigger(trigger *PS.TriggerStmt, ctx *TriggerContext) error {
	var whenRow *DT.Row
	if trigger.When != nil {
		whenRow = BuildTriggerWhenRow(ctx)
		ok, err := EvalTriggerWhen(trigger.When, whenRow, ctx.Params)
		if err != nil {
			return fmt.Errorf("ex: trigger WHEN evaluation: %w", err)
		}
		if !ok {
			return nil
		}
	}

	for _, stmt := range trigger.Body {
		if err := ExecuteTriggerStmt(stmt, ctx); err != nil {
			return err
		}
	}
	return nil
}

// BuildTriggerWhenRow builds a synthetic DT.Row for NEW/OLD references.
func BuildTriggerWhenRow(ctx *TriggerContext) *DT.Row {
	var data []DT.Value
	var cols []string
	if ctx.NewRow != nil {
		for i, c := range ctx.NewRow.Cols {
			data = append(data, ctx.NewRow.Data[i])
			cols = append(cols, "NEW."+c)
		}
	}
	if ctx.OldRow != nil {
		for i, c := range ctx.OldRow.Cols {
			data = append(data, ctx.OldRow.Data[i])
			cols = append(cols, "OLD."+c)
		}
	}
	return &DT.Row{Data: data, Cols: cols}
}

// EvalTriggerWhen evaluates a trigger WHEN expression.
func EvalTriggerWhen(expr PS.Expr, row *DT.Row, params []any) (bool, error) {
	val, err := EV.EvalValue(expr, row, params)
	if err != nil {
		return false, err
	}
	if val.Kind == DT.KindNull {
		return false, nil
	}
	if val.Kind == DT.KindBool {
		return val.Bo, nil
	}
	return false, nil
}

// ExecuteTriggerStmt executes a single statement within a trigger body.
func ExecuteTriggerStmt(stmt PS.Stmt, ctx *TriggerContext) error {
	if ctx.Exec == nil {
		return errors.New("ex: trigger execution requires an executor")
	}
	if refresh, ok := stmt.(*PS.RefreshMatViewStmt); ok {
		sql := "REFRESH MATERIALIZED VIEW " + refresh.Name
		return ctx.Exec(sql)
	}
	return nil
}
