package DT

import (
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// memStore is a minimal in-memory Store used by TableHandle tests.
// The full Store interface (LSM-backed) lives in LS but these tests
// only exercise the TableHandle abstraction — they don't need
// crash-recovery semantics.
type memStore struct {
	rows map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{rows: map[string][]byte{}}
}

func (m *memStore) Insert(key, value []byte) error {
	cp := append([]byte(nil), key...)
	cp2 := append([]byte(nil), value...)
	m.rows[string(cp)] = cp2
	return nil
}

func (m *memStore) Delete(key []byte) error {
	delete(m.rows, string(key))
	return nil
}

func (m *memStore) Get(key []byte) ([]byte, bool, error) {
	v, ok := m.rows[string(key)]
	return v, ok, nil
}

func (m *memStore) NewIterator(prefix []byte) ls.RangeIter {
	return nil
}

func (m *memStore) ManualCompact() error { return nil }

// registerTestTable is a helper that registers a StoreSchema for the
// given name so OpenTable succeeds. Tests call this once per table
// before exercising TableHandle methods.
func registerTestTable(t *testing.T, name string, cols []string, pk string) *StoreSchema {
	t.Helper()
	id := RegisterStoreSchema(name, cols, pk)
	ss := StoreSchemas[id]
	if ss == nil {
		t.Fatalf("RegisterStoreSchema %q: schema not registered", name)
	}
	return ss
}

// TestTableHandle_InsertGetScan verifies the REQ000987 abstraction:
// InsertRow/GetRow/Exists round-trip through a TableHandle with the
// expected key/buf construction (prefix + pk).
func TestTableHandle_InsertGetScan(t *testing.T) {
	store := newMemStore()
	registerTestTable(t, "t1", []string{"id", "name"}, "id")
	defer UnregisterTable("t1")

	h, err := OpenTable(store, "t1")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	if h.Prefix() == nil {
		t.Fatalf("TableHandle.Prefix() = nil, want non-nil")
	}

	row := Row{
		Cols: []string{"id", "name"},
		Data: []Value{NewIntValue(42), NewTextValue("answer")},
	}
	key, buf, err := h.InsertRow(row)
	if err != nil {
		t.Fatalf("InsertRow: %v", err)
	}
	if len(key) == 0 {
		t.Errorf("InsertRow returned empty key")
	}
	if len(buf) == 0 {
		t.Errorf("InsertRow returned empty buf")
	}

	// Verify the key starts with the table prefix and the PK suffix.
	if !startsWith(key, h.Prefix()) {
		t.Errorf("InsertRow key %x does not start with prefix %x", key, h.Prefix())
	}

	// GetRow round-trip.
	got, found, err := h.GetRow(int64(42))
	if err != nil || !found {
		t.Fatalf("GetRow(42): found=%v err=%v", found, err)
	}
	if got.Data[0].I64 != 42 || got.Data[1].S != "answer" {
		t.Errorf("GetRow round-trip mismatch: got Data=%v", got.Data)
	}

	// Exists should match GetRow's found flag.
	ex, err := h.Exists(int64(42))
	if err != nil || !ex {
		t.Errorf("Exists(42) = %v, %v; want true, nil", ex, err)
	}
	ex, err = h.Exists(int64(99))
	if err != nil || ex {
		t.Errorf("Exists(99) = %v, %v; want false, nil", ex, err)
	}
}

// TestTableHandle_DeleteRow verifies DeleteRow removes the row and
// also the secondary index entries (none in this test, so no error).
func TestTableHandle_DeleteRow(t *testing.T) {
	store := newMemStore()
	registerTestTable(t, "t2", []string{"id", "val"}, "id")
	defer UnregisterTable("t2")

	h, err := OpenTable(store, "t2")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	row := Row{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(1), NewIntValue(100)}}
	if _, _, err := h.InsertRow(row); err != nil {
		t.Fatalf("InsertRow: %v", err)
	}
	if _, err := h.DeleteRow(row); err != nil {
		t.Fatalf("DeleteRow: %v", err)
	}
	ex, _ := h.Exists(int64(1))
	if ex {
		t.Errorf("row still exists after DeleteRow")
	}
}

// TestTableHandle_OpenTableUnknownErrors verifies OpenTable returns
// ErrTableNotRegisteredForStorage when the table is not registered.
func TestTableHandle_OpenTableUnknownErrors(t *testing.T) {
	store := newMemStore()
	_, err := OpenTable(store, "nonexistent_table")
	if err == nil {
		t.Fatalf("OpenTable(unknown) = nil error, want ErrTableNotRegisteredForStorage")
	}
}

// TestTableHandle_InsertRowAutoRowID covers REQ001128: when the PK
// column receives NULL, ExtractPK generates a synthetic rowid and
// InsertRow writes it back into row.Data so the persisted cell value
// matches the generated ID.
func TestTableHandle_InsertRowAutoRowID(t *testing.T) {
	store := newMemStore()
	ss := registerTestTable(t, "t3", []string{"id", "name"}, "id")
	ss.HiddenPK = false // exercise the explicit-PK NULL path
	defer UnregisterTable("t3")

	h, err := OpenTable(store, "t3")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	// Build a row where the PK (id) is NULL but the schema declares it.
	row := Row{
		Cols: []string{"id", "name"},
		Data: []Value{NullValue(), NewTextValue("auto")},
	}
	if _, _, err := h.InsertRow(row); err != nil {
		t.Fatalf("InsertRow with NULL PK: %v", err)
	}
	// After InsertRow, row.Data[0] should be filled with the
	// synthesized rowid (NOT remain NULL).
	if row.Data[0].Kind == KindNull {
		t.Errorf("InsertRow did not fill NULL PK: row.Data[0] still NULL")
	}
	if row.Data[0].Kind != KindInt {
		t.Errorf("InsertRow filled PK with non-int: %v", row.Data[0])
	}
}

// startsWith is a tiny helper to avoid importing bytes just for one
// equality check in tests.
func startsWith(s, prefix []byte) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}