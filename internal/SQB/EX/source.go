package EX

import (
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	WT "github.com/cyw0ng95/razordata/internal/SQB/WT"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// UnregisterAll clears all registered state for test isolation.
func UnregisterAll() {
	DT.TablesMu.Lock()
	DT.Tables = map[string][]DT.Row{}
	DT.Schemas = map[string][]string{}
	DT.StoreMu.Lock()
	DT.StoreSchemas = map[uint64]*DT.StoreSchema{}
	DT.TableIDs = map[string]uint64{}
	DT.InMemSchemas = map[string]*DT.StoreSchema{}
	DT.TableIDSeq = 0
	DT.CurrentCatalog.Store(nil)
	DT.RegisteredIndexes = map[string][]DT.RegisteredIndex{}
	DT.ViewRegistry = map[string]*PS.Select{}
	DT.MatViewRegistry = map[string]*PS.Select{}
	DT.StoreMu.Unlock()
	WT.ClearTriggerState()
	DT.TablesMu.Unlock()
	OP.TableSchemaMu.Lock()
	OP.TableSchemaCache = map[string]*OP.TableSchemaEntry{}
	OP.TableSchemaMu.Unlock()
	EV.ClearSubqueryCaches()
}
