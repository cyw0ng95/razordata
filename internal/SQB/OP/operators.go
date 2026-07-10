package OP

import (
	"strings"
	"sync"

	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// TableSchemaCache caches shared column metadata per table to avoid
// rebuilding Cols, Types, and colIndex on every SeqScan.snapshot() call.
type tableSchemaEntry struct {
	cols     []string
	types    []LX.TokenType
	colIndex map[string]int
}

type TableSchemaEntry = tableSchemaEntry

var (
	TableSchemaMu    sync.RWMutex
	TableSchemaCache = map[string]*TableSchemaEntry{}
)

func getTableSchema(table string, src []Row) *tableSchemaEntry {
	if len(src) == 0 {
		return nil
	}
	// Fast path: read lock.
	TableSchemaMu.RLock()
	entry, ok := TableSchemaCache[table]
	TableSchemaMu.RUnlock()
	if ok {
		return entry
	}
	// Slow path: write lock and build.
	TableSchemaMu.Lock()
	defer TableSchemaMu.Unlock()
	// Double-check after acquiring write lock.
	if entry, ok = TableSchemaCache[table]; ok {
		return entry
	}
	cols := append([]string(nil), src[0].Cols...)
	var types []LX.TokenType
	if len(src[0].Types) > 0 {
		types = append([]LX.TokenType(nil), src[0].Types...)
	}
	colIndex := make(map[string]int, len(cols))
	for i, c := range cols {
		colIndex[strings.ToLower(c)] = i
	}
	entry = &tableSchemaEntry{cols: cols, types: types, colIndex: colIndex}
	TableSchemaCache[table] = entry
	return entry
}