package EX

import (
	"errors"
	"sync"

	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

var errTableExists = errors.New("ex: table already exists")

var (
	tablesMu sync.RWMutex
	tables   = map[string][]Row{}
	schemas  = map[string][]string{}
)

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

func UnregisterAll() {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	tables = map[string][]Row{}
	schemas = map[string][]string{}
}

func cloneRow(r Row) Row {
	out := Row{Cols: append([]string(nil), r.Cols...), Types: append([]int(nil), r.Types...)}
	if r.Data != nil {
		out.Data = append([]interface{}(nil), r.Data...)
	}
	return out
}

func tableSchema(name string, existing []Row) []string {
	if len(existing) > 0 {
		return existing[0].Cols
	}
	return nil
}

func buildInsertRow(schema []string, cols []string, values []PS.Expr) (Row, error) {
	out := Row{Cols: append([]string(nil), schema...)}
	if len(cols) == 0 {
		out.Data = make([]interface{}, len(values))
		for i, v := range values {
			val, err := Eval(v, nil, nil)
			if err != nil {
				return Row{}, err
			}
			out.Data[i] = val
		}
		return out, nil
	}
	byName := make(map[string]interface{}, len(cols))
	for i, name := range cols {
		val, err := Eval(values[i], nil, nil)
		if err != nil {
			return Row{}, err
		}
		byName[name] = val
	}
	out.Data = make([]interface{}, len(schema))
	for i, name := range schema {
		if v, ok := byName[name]; ok {
			out.Data[i] = v
			continue
		}
		out.Data[i] = nil
	}
	return out, nil
}

func applyUpdate(row *Row, set []PS.Pair) error {
	for _, p := range set {
		val, err := Eval(p.Val, row, nil)
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

func replaceRow(table string, updated Row) error {
	snapshot := cloneRow(updated)
	return replaceBySnapshot(table, snapshot, updated)
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
