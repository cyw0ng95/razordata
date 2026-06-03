package EX

import (
	"sync"
)

var (
	tablesMu sync.RWMutex
	tables   = map[string][]Row{}
)

func RegisterTable(name string, rows []Row) {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	cp := make([]Row, len(rows))
	for i, r := range rows {
		cp[i] = cloneRow(r)
	}
	tables[name] = cp
}

func UnregisterAll() {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	tables = map[string][]Row{}
}

func cloneRow(r Row) Row {
	out := Row{Cols: append([]string(nil), r.Cols...), Types: append([]int(nil), r.Types...)}
	if r.Data != nil {
		out.Data = append([]interface{}(nil), r.Data...)
	}
	return out
}
