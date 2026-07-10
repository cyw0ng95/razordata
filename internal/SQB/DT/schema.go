package DT

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	EC "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

var ErrTableExists = errors.New("ex: table already exists")

// UniqueKey is a UNIQUE constraint resolved to column indices. Cols
// references positions in the parent StoreSchema.Cols slice.
type UniqueKey struct {
	Cols []int
}

// StoreSchema describes a table's column layout for row encoding.
type StoreSchema struct {
	Cols     []string
	Pk       string
	Nullable []bool      // parallel to Cols; false means NOT NULL
	Defaults []PS.Expr   // parallel to Cols; nil means no DEFAULT
	Unique   []UniqueKey // each entry is 1+ columns
	Checks   []PS.Expr   // parallel to CHECK constraints
	// REQ000986: pre-compiled CHECK expressions evaluated per row.
	CompiledChecks []func(*Row) (bool, error)
	ColTypes       []LX.TokenType
	// REQ000568: DECIMAL(P,S) Precision and Scale per column.
	// Only meaningful when ColTypes[i] is T_DECIMAL or T_NUMERIC.
	Precision []int
	Scale     []int
	// REQ000248/249: parallel to Cols; non-nil means column is a
	// STORED Generated column. The expression is evaluated on
	// INSERT/UPDATE and the result is stored as the cell value.
	// VIRTUAL Generated columns are stored as nil here (deferred).
	Generated   []PS.Expr
	ForeignKeys []ForeignKeyConstraint // REQ000126: FK constraints
	// REQ000367: HiddenPK is set when the table was created
	// without a PRIMARY KEY declaration but is registered for
	// storage. The engine synthesizes an int64 rowid per insert
	// and uses it as the LSM key suffix; the user-visible schema
	// is unchanged (no rowid column appears in SELECT *).
	HiddenPK  bool
	// Strict indicates the table uses strict type enforcement.
	// When true, INSERT validates that each value's type matches
	// the declared column affinity. REQ001369.
	Strict    bool
	NextRowID atomic.Int64
	// ColIndex is a pre-built O(1) column name -> index map.
	// Built once at schema creation and shared across all rows.
	// Eliminates per-row map allocation in Row.buildColIndex().
	ColIndex map[string]int
}

// ForeignKeyConstraint describes a single FK constraint (REQ000126).
type ForeignKeyConstraint struct {
	Columns    []string // local column names
	RefTable   string   // referenced table
	RefColumns []string // referenced columns
	OnDelete   string   // CASCADE, RESTRICT, SET NULL, SET DEFAULT, NO ACTION
	OnUpdate   string   // same set
}

// RegisteredIndex is one entry in the EX-layer's index registry.
type RegisteredIndex struct {
	Name      string
	Columns   []string
	Unique    bool
	Predicate string // REQ001386: partial index WHERE clause text
}

// TriggerInfo stores metadata about a trigger for sqlite_master.
type TriggerInfo struct {
	Name    string
	OnTable string
	SQL     string
}

var (
	StoreMu      sync.Mutex
	TableIDSeq   uint64
	TableIDs     = map[string]uint64{}
	StoreSchemas = map[uint64]*StoreSchema{}
	InMemSchemas = map[string]*StoreSchema{} // in-memory fallback, keyed by table name

	// CurrentCatalog is the persistent system catalog wired in
	// by SYS at Open time. nil means the EX layer is in
	// in-memory mode (legacy behavior, used by unit tests that
	// do not have a backing directory).
	CurrentCatalog atomic.Pointer[ls.Catalog]

	// RegisteredIndexes is the EX-layer's view of secondary
	// indexes declared via CREATE INDEX. Keyed by table name.
	// iter-22 secondary indexes MVP.
	RegisteredIndexes = map[string][]RegisteredIndex{}

	// ViewRegistry stores view definitions (REQ000240). Keyed by
	// view name. Values are the parsed SELECT statements.
	ViewRegistry = map[string]*PS.Select{}

	// ViewMu protects ViewRegistry.
	ViewMu sync.RWMutex

	// MatViewRegistry stores materialized view definitions (REQ000316).
	// Keyed by view name. Values are the parsed SELECT statements.
	MatViewRegistry = map[string]*PS.Select{}

	// MatViewMu protects MatViewRegistry.
	MatViewMu sync.RWMutex

	// TriggerRegistry stores trigger definitions. Keyed by trigger
	// name. Used by sqlite_master introspection (REQ001388).
	TriggerRegistry = map[string]TriggerInfo{}
	TriggerMu sync.RWMutex
)

// In-memory table state (from source.go).
var (
	TablesMu sync.RWMutex
	Tables   = map[string][]Row{}
	Schemas  = map[string][]string{}
	TablePKs = map[string]string{} // in-memory table primary key column name

	// REQ001326: TempTables are session-scoped in-memory tables that
	// shadow main tables of the same name. Lookup via GetTableData /
	// GetTableSchema checks TempTables first.
	TempTables  = map[string][]Row{}
	TempSchemas = map[string][]string{}
	TempTableNames = map[string]bool{} // set of all temp table names for cleanup
)

// BuildColIndex (re)builds a column name -> index map from the schema's
// Cols slice. Always overwrites an existing map so callers can invoke it
// after ALTER TABLE adds/drops/renames columns; the result is shared
// across all rows.
func (s *StoreSchema) BuildColIndex() {
	s.ColIndex = make(map[string]int, len(s.Cols))
	for i, c := range s.Cols {
		s.ColIndex[c] = i
	}
}

// SchemaFor looks up a StoreSchema by table name, first in the store-backed
// registry (TableIDs → StoreSchemas), then in the in-memory fallback.
func SchemaFor(name string) (*StoreSchema, bool) {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	if id, ok := TableIDs[name]; ok {
		if ss, ok := StoreSchemas[id]; ok {
			return ss, ok
		}
	}
	if ss, ok := InMemSchemas[name]; ok {
		return ss, ok
	}
	return nil, false
}

// AllTableNames returns a sorted list of all registered table names.
// Used by PRAGMA table_list (REQ000732).
func AllTableNames() []string {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	seen := map[string]bool{}
	var names []string
	for name := range TableIDs {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for name := range InMemSchemas {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// RegisterView stores a view definition (REQ000240).
func RegisterView(name string, sel *PS.Select) {
	ViewMu.Lock()
	defer ViewMu.Unlock()
	ViewRegistry[name] = sel
}

// LookupView returns the SELECT statement for a view, or nil.
func LookupView(name string) *PS.Select {
	ViewMu.RLock()
	defer ViewMu.RUnlock()
	return ViewRegistry[name]
}

// UnregisterAllViews clears all views (for testing).
func UnregisterAllViews() {
	ViewMu.Lock()
	defer ViewMu.Unlock()
	ViewRegistry = map[string]*PS.Select{}
}

// UnregisterView removes a single view by name. Returns true if the
// view existed. REQ000494.
func UnregisterView(name string) bool {
	ViewMu.Lock()
	defer ViewMu.Unlock()
	_, existed := ViewRegistry[name]
	delete(ViewRegistry, name)
	return existed
}

// RegisterMatView stores a materialized view definition (REQ000316).
func RegisterMatView(name string, sel *PS.Select) {
	MatViewMu.Lock()
	defer MatViewMu.Unlock()
	MatViewRegistry[name] = sel
}

// LookupMatView returns the SELECT for a materialized view, or nil.
func LookupMatView(name string) *PS.Select {
	MatViewMu.RLock()
	defer MatViewMu.RUnlock()
	return MatViewRegistry[name]
}

// UnregisterMatView removes a single materialized view by name.
func UnregisterMatView(name string) {
	MatViewMu.Lock()
	defer MatViewMu.Unlock()
	delete(MatViewRegistry, name)
}

// UnregisterAllMatViews clears all materialized views (for testing).
func UnregisterAllMatViews() {
	MatViewMu.Lock()
	defer MatViewMu.Unlock()
	MatViewRegistry = map[string]*PS.Select{}
}

// RegisterIndexWithID registers a secondary index for the given
// table. Called from the CREATE INDEX executor path; for tests
// that don't go through the SQL surface, use RegisterIndex.
func RegisterIndexWithID(table string, idx RegisteredIndex) {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	RegisteredIndexes[table] = append(RegisteredIndexes[table], idx)
}

// GetRegisteredIndexes returns a copy of the index list for a table.
func GetRegisteredIndexes(table string) []RegisteredIndex {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	src := RegisteredIndexes[table]
	out := make([]RegisteredIndex, len(src))
	for i, idx := range src {
		out[i] = RegisteredIndex{
			Name:    idx.Name,
			Columns: append([]string(nil), idx.Columns...),
			Unique:  idx.Unique,
		}
	}
	return out
}

// AllTriggers returns a snapshot of the trigger registry.
func AllTriggers() []TriggerInfo {
	TriggerMu.RLock()
	defer TriggerMu.RUnlock()
	out := make([]TriggerInfo, 0, len(TriggerRegistry))
	for _, ti := range TriggerRegistry {
		out = append(out, ti)
	}
	return out
}

// RegisterTrigger adds a trigger to the registry.
func RegisterTrigger(ti TriggerInfo) {
	TriggerMu.Lock()
	defer TriggerMu.Unlock()
	TriggerRegistry[ti.Name] = ti
}

// UnregisterTrigger removes a trigger by name.
func UnregisterTrigger(name string) {
	TriggerMu.Lock()
	defer TriggerMu.Unlock()
	delete(TriggerRegistry, name)
}

// UnregisterAllTriggers clears the trigger registry.
func UnregisterAllTriggers() {
	TriggerMu.Lock()
	defer TriggerMu.Unlock()
	TriggerRegistry = map[string]TriggerInfo{}
}

// nextTableID allocates a new table ID. The id is stable for the lifetime
// of the process; restarting the process reassigns IDs and old data is
// unreachable (consistent with the existing in-memory catalog behavior).
// When a persistent catalog is wired in (iter-12), the table ID is
// sourced from the catalog's NextID counter so it survives Close/Open.
func NextTableID() uint64 {
	cat := CurrentCatalog.Load()
	if cat != nil {
		id, err := cat.NextID()
		if err == nil {
			return id
		}
		// Fall through to in-memory counter on error so the
		// process keeps serving (catalog errors are surfaced
		// separately on the operation that triggered them).
	}
	StoreMu.Lock()
	defer StoreMu.Unlock()
	TableIDSeq++
	return TableIDSeq
}

func TableIDFor(name string) (uint64, bool) {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	id, ok := TableIDs[name]
	return id, ok
}

// SetCatalog binds a persistent system catalog into the EX
// layer. Subsequent CREATE TABLE / DROP TABLE calls will go
// through the catalog for persistence. Pass nil to revert to
// in-memory mode (legacy behavior).
func SetCatalog(c *ls.Catalog) {
	CurrentCatalog.Store(c)
}

// Catalog returns the currently bound catalog, or nil if the EX
// layer is in in-memory mode.
func Catalog() *ls.Catalog {
	return CurrentCatalog.Load()
}

// RegisterStoreSchema assigns a table ID to a name and stores its schema.
// Safe to call multiple times for the same name (idempotent). All columns
// default to Nullable=true with no DEFAULT clause. Use
// RegisterStoreSchemaWithConstraints to set NOT NULL and DEFAULT.
func RegisterStoreSchema(name string, Cols []string, Pk string) uint64 {
	Nullable := make([]bool, len(Cols))
	for i := range Nullable {
		Nullable[i] = true
	}
	StoreMu.Lock()
	defer StoreMu.Unlock()
	if id, ok := TableIDs[name]; ok {
		if ss, ok := StoreSchemas[id]; ok {
			cp := make([]string, len(Cols))
			copy(cp, Cols)
			ss.Cols = cp
			ss.Pk = Pk
			ss.Nullable = Nullable
			ss.Defaults = nil
			ss.BuildColIndex()
			return id
		}
	}
	id := NextTableIDLocked()
	TableIDs[name] = id
	ss := &StoreSchema{Cols: append([]string(nil), Cols...), Pk: Pk, Nullable: Nullable}
	ss.BuildColIndex()
	StoreSchemas[id] = ss
	return id
}

// RegisterInMemorySchema registers a StoreSchema in the in-memory fallback
// registry, enabling constraint enforcement for tables without a Store.
func RegisterInMemorySchema(name string, Cols []string, Pk string) {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	Nullable := make([]bool, len(Cols))
	for i := range Nullable {
		Nullable[i] = true
	}
	ss := &StoreSchema{
		Cols:     append([]string(nil), Cols...),
		Pk:       Pk,
		Nullable: Nullable,
	}
	InMemSchemas[name] = ss
	ss.BuildColIndex()
}

// RegisterStoreSchemaWithConstraints stores schema with NOT NULL and DEFAULT
// info carried in the parallel slices. Safe to call multiple times for the
// same name (idempotent). Cols, Nullable, and Defaults must be the same
// length. No UNIQUE constraints are recorded; use
// RegisterStoreSchemaFull for that.
func RegisterStoreSchemaWithConstraints(name string, Cols []string, Nullable []bool, Defaults []PS.Expr, Pk string) uint64 {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	cpCols := append([]string(nil), Cols...)
	cpNullable := append([]bool(nil), Nullable...)
	var cpDefaults []PS.Expr
	if Defaults != nil {
		cpDefaults = append([]PS.Expr(nil), Defaults...)
	}
	if id, ok := TableIDs[name]; ok {
		if ss, ok := StoreSchemas[id]; ok {
			ss.Cols = cpCols
			ss.Pk = Pk
			ss.Nullable = cpNullable
			ss.Defaults = cpDefaults
			ss.Unique = nil
			ss.BuildColIndex()
			return id
		}
	}
	id := NextTableIDLocked()
	TableIDs[name] = id
	ss := &StoreSchema{Cols: cpCols, Pk: Pk, Nullable: cpNullable, Defaults: cpDefaults}
	ss.BuildColIndex()
	StoreSchemas[id] = ss
	return id
}

// RegisterStoreSchemaFull stores the full constraint set including
// UNIQUE. Unique may be nil. Safe to call multiple times for the
// same name (idempotent).
func RegisterStoreSchemaFull(name string, Cols []string, Nullable []bool, Defaults []PS.Expr, Unique []UniqueKey, Pk string) uint64 {
	return RegisterStoreSchemaWithFK(name, Cols, Nullable, Defaults, Unique, Pk, nil)
}

// RegisterStoreSchemaWithFK stores the full constraint set including
// UNIQUE and FOREIGN KEY constraints. REQ000126.
func RegisterStoreSchemaWithFK(name string, Cols []string, Nullable []bool, Defaults []PS.Expr, Unique []UniqueKey, Pk string, fks []ForeignKeyConstraint) uint64 {
	StoreMu.Lock()
	defer StoreMu.Unlock()
	return RegisterStoreSchemaWithFKLocked(name, Cols, Nullable, Defaults, Unique, Pk, fks)
}

// RegisterStoreSchemaWithFKLocked is the locked variant of
// RegisterStoreSchemaWithFK. The caller MUST already hold StoreMu.
func RegisterStoreSchemaWithFKLocked(name string, Cols []string, Nullable []bool, Defaults []PS.Expr, Unique []UniqueKey, Pk string, fks []ForeignKeyConstraint) uint64 {
	cpCols := append([]string(nil), Cols...)
	cpNullable := append([]bool(nil), Nullable...)
	var cpDefaults []PS.Expr
	if Defaults != nil {
		cpDefaults = append([]PS.Expr(nil), Defaults...)
	}
	var cpUnique []UniqueKey
	if Unique != nil {
		cpUnique = make([]UniqueKey, len(Unique))
		for i, u := range Unique {
			cpUnique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
		}
	}
	if id, ok := TableIDs[name]; ok {
		if ss, ok := StoreSchemas[id]; ok {
			ss.Cols = cpCols
			ss.Pk = Pk
			ss.Nullable = cpNullable
			ss.Defaults = cpDefaults
			ss.Unique = cpUnique
			if fks != nil {
				ss.ForeignKeys = fks
			}
			ss.BuildColIndex()
			return id
		}
	}
	id := NextTableIDLocked()
	TableIDs[name] = id
	ss := &StoreSchema{Cols: cpCols, Pk: Pk, Nullable: cpNullable, Defaults: cpDefaults, Unique: cpUnique, ForeignKeys: fks}
	ss.BuildColIndex()
	StoreSchemas[id] = ss
	return id
}

// NextTableIDLocked is the locked variant of nextTableID. The
// caller MUST already hold StoreMu.
func NextTableIDLocked() uint64 {
	cat := CurrentCatalog.Load()
	if cat != nil {
		id, err := cat.NextID()
		if err == nil {
			return id
		}
	}
	TableIDSeq++
	return TableIDSeq
}

// RegisterFromCatalog rehydrates the in-memory StoreSchemas /
// TableIDs maps from a catalog entry. Used by SYS.Open to
// repopulate runtime state on every restart. Does NOT call
// NextID — the entry already carries its tableID. Subsequent
// unregistration of this table uses the same tableID, which
// must match what other code paths expect.
//
// Lock ordering: TablesMu → StoreMu (REQ000974).
func RegisterFromCatalog(entry *ls.CatalogEntry) error {
	if entry == nil {
		return errors.New("ex: nil catalog entry")
	}
	// Acquire TablesMu first, then StoreMu, to match the
	// established TablesMu → StoreMu ordering (REQ000974).
	TablesMu.Lock()
	StoreMu.Lock()
	if _, exists := TableIDs[entry.Name]; exists {
		// Already registered (e.g. test setup called RegisterTable
		// manually before SYS bound the catalog). Leave the
		// existing entry alone.
		StoreMu.Unlock()
		TablesMu.Unlock()
		return nil
	}
	Cols := make([]string, len(entry.Columns))
	Nullable := make([]bool, len(entry.Columns))
	ColTypes := make([]LX.TokenType, len(entry.Columns))
	ColIndex := make(map[string]int, len(entry.Columns))
	for i, c := range entry.Columns {
		Cols[i] = c.Name
		Nullable[i] = c.Nullable
		ColTypes[i] = LX.TokenType(c.Type)
		ColIndex[c.Name] = i
	}
	// PRIMARY KEY implies NOT NULL.
	if entry.PrimaryKey != "" {
		if idx, ok := ColIndex[entry.PrimaryKey]; ok {
			Nullable[idx] = false
		}
	}
	Unique := make([]UniqueKey, len(entry.Unique))
	for i, u := range entry.Unique {
		Unique[i] = UniqueKey{Cols: append([]int(nil), u.Cols...)}
	}
	// Defaults are not persisted across restart in v0.9.0 — the
	// parser AST cannot be safely serialized. Callers that need
	// the Defaults back must re-issue the CREATE TABLE statement.
	var Defaults []PS.Expr
	TableIDs[entry.Name] = entry.TableID
	ss := &StoreSchema{
		Cols:     Cols,
		Pk:       entry.PrimaryKey,
		Nullable: Nullable,
		Defaults: Defaults,
		Unique:   Unique,
		ColTypes: ColTypes,
	}
	ss.BuildColIndex()
	StoreSchemas[entry.TableID] = ss
	// Also publish to the in-memory `Tables` / `Schemas` map that
	// the executor scans.
	if _, exists := Tables[entry.Name]; !exists {
		Tables[entry.Name] = []Row{}
	}
	Schemas[entry.Name] = Cols
	// Track the in-memory counter so subsequent NextID calls
	// (in the absence of a catalog) do not reuse this ID.
	if entry.TableID >= TableIDSeq {
		TableIDSeq = entry.TableID
	}
	StoreMu.Unlock()
	TablesMu.Unlock()
	return nil
}

// RegisterTable registers a table with rows in the in-memory registry.
func RegisterTable(name string, rows []Row) {
	TablesMu.Lock()
	defer TablesMu.Unlock()
	cp := make([]Row, len(rows))
	for i, r := range rows {
		cp[i] = CloneRow(r)
	}
	Tables[name] = cp
	if len(rows) > 0 {
		Schemas[name] = append([]string(nil), rows[0].Cols...)
	}
}

// RegisterTableSchema registers a table schema without rows.
func RegisterTableSchema(name string, Cols []string) {
	TablesMu.Lock()
	defer TablesMu.Unlock()
	if _, ok := Tables[name]; !ok {
		Tables[name] = []Row{}
	}
	Schemas[name] = append([]string(nil), Cols...)
}

// Schema returns the column names for a registered table.
func Schema(name string) []string {
	TablesMu.RLock()
	defer TablesMu.RUnlock()
	if s, ok := Schemas[name]; ok {
		return append([]string(nil), s...)
	}
	return nil
}

// SnapshotInMemoryTable returns a deep copy of the in-memory table's
// current rows. The caller must hold TablesMu (read or write).
// REQ000641.
func SnapshotInMemoryTable(table string) []Row {
	src := Tables[table]
	if tempRows, ok := TempTables[table]; ok {
		src = tempRows
	}
	if src == nil {
		return nil
	}
	cp := make([]Row, len(src))
	for i, r := range src {
		cp[i] = CloneRow(r)
	}
	return cp
}

// RestoreInMemoryTables replaces in-memory tables with the given
// snapshots. Used by TX.Transaction.Rollback to undo in-memory
// writes. REQ000641.
func RestoreInMemoryTables(snapshots map[string][]Row) error {
	TablesMu.Lock()
	defer TablesMu.Unlock()
	for table, snap := range snapshots {
		Tables[table] = snap
	}
	return nil
}

// UnregisterTable removes a single table from the in-memory
// Tables and Schemas maps. Used by CTE cleanup to remove
// temporary CTE tables after query execution.
func UnregisterTable(name string) {
	TablesMu.Lock()
	defer TablesMu.Unlock()
	delete(Tables, name)
	delete(Schemas, name)
}

// CloneRow creates a deep copy of a Row.
func CloneRow(r Row) Row {
	EC.BUG_ON(len(r.Data) != len(r.Types), "CloneRow row integrity: Data len %d != Types len %d", len(r.Data), len(r.Types))
	out := Row{Cols: append([]string(nil), r.Cols...), Types: append([]LX.TokenType(nil), r.Types...), Outer: r.Outer, Planner: r.Planner, StoreKey: r.StoreKey, TableName: r.TableName}
	if r.Data != nil {
		out.Data = append([]Value(nil), r.Data...)
	}
	return out
}

// RowIndex finds the index of a row in a table.
func RowIndex(table string, row Row) (int, bool) {
	TablesMu.RLock()
	defer TablesMu.RUnlock()
	return RowIndexLocked(Tables[table], row)
}

// RowIndexLocked finds the index of a row in a slice of rows.
func RowIndexLocked(rows []Row, row Row) (int, bool) {
	for i, r := range rows {
		if RowEqual(r, row) {
			return i, true
		}
	}
	return -1, false
}

// RowEqual compares two rows for equality.
func RowEqual(a, b Row) bool {
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

// ReplaceBySnapshot replaces a row in a table by matching a snapshot.
func ReplaceBySnapshot(table string, snapshot, updated Row) error {
	TablesMu.Lock()
	defer TablesMu.Unlock()
	existing := Tables[table]
	idx, ok := RowIndexLocked(existing, snapshot)
	if !ok {
		return errors.New("ex: row not found for update")
	}
	existing[idx] = updated
	Tables[table] = existing
	return nil
}

// equalValue compares two Values for equality (NULL == NULL is true).
func equalValue(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindNull:
		return true
	case KindInt:
		return a.I64 == b.I64
	case KindFloat:
		return a.F64 == b.F64
	case KindText:
		return a.S == b.S
	}
	return false
}

// RegisterTempTable registers a temporary table in the in-memory schema.
// Temp tables shadow main tables of the same name. REQ001326.
func RegisterTempTable(name string, cols []string) {
	TablesMu.Lock()
	defer TablesMu.Unlock()
	TempTables[name] = []Row{}
	TempSchemas[name] = append([]string(nil), cols...)
	TempTableNames[name] = true
}

// GetTableData returns rows for the given table name, checking temp
// tables first. REQ001326.
func GetTableData(name string) []Row {
	TablesMu.RLock()
	defer TablesMu.RUnlock()
	if rows, ok := TempTables[name]; ok {
		return rows
	}
	return Tables[name]
}

// GetTableSchema returns column names for the given table, checking
// temp tables first. REQ001326.
func GetTableSchema(name string) []string {
	TablesMu.RLock()
	defer TablesMu.RUnlock()
	if s, ok := TempSchemas[name]; ok {
		return s
	}
	return Schemas[name]
}

// IsTempTable reports whether the given table name is a temp table.
func IsTempTable(name string) bool {
	TablesMu.RLock()
	defer TablesMu.RUnlock()
	return TempTableNames[name]
}

// ClearTempTables removes all temp tables from the in-memory schema.
// Called at session end. REQ001326.
func ClearTempTables() {
	TablesMu.Lock()
	defer TablesMu.Unlock()
	for name := range TempTableNames {
		delete(TempTables, name)
		delete(TempSchemas, name)
	}
	TempTableNames = map[string]bool{}
}

// tempStoreMode controls where temp data is stored.
// 0=DEFAULT, 1=FILE, 2=MEMORY. Default MEMORY per REQ001327.
var tempStoreMode int32 = 2

// TempStoreMode returns the current temp_store mode.
func TempStoreMode() int { return int(atomic.LoadInt32(&tempStoreMode)) }

// SetTempStoreMode sets the temp_store mode (0, 1, or 2). REQ001327.
func SetTempStoreMode(mode int) { atomic.StoreInt32(&tempStoreMode, int32(mode)) }

// REQ001392: journal_mode state.
var (
	journalModeMu sync.RWMutex
	journalMode   = "delete"
)

func JournalMode() string {
	journalModeMu.RLock()
	defer journalModeMu.RUnlock()
	return journalMode
}

func SetJournalMode(mode string) {
	journalModeMu.Lock()
	defer journalModeMu.Unlock()
	journalMode = mode
}
