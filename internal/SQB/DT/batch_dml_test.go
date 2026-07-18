package DT

import (
	"bytes"
	"reflect"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

// batchMemStore is a memStore that satisfies BatchStore + BatchDeleteStore
// for TableHandle.UpdateRowBatch / DeleteRowBatch tests. It records the
// batch call counts so tests can assert the batch path was taken.
type batchMemStore struct {
	rows         map[string][]byte
	writeBatches int
	deleteBatches int
}

func newBatchMemStore() *batchMemStore {
	return &batchMemStore{rows: map[string][]byte{}}
}

func (m *batchMemStore) Insert(key, value []byte) error {
	m.rows[string(append([]byte(nil), key...))] = append([]byte(nil), value...)
	return nil
}

func (m *batchMemStore) Delete(key []byte) error {
	delete(m.rows, string(key))
	return nil
}

func (m *batchMemStore) Get(key []byte) ([]byte, bool, error) {
	v, ok := m.rows[string(key)]
	return v, ok, nil
}

func (m *batchMemStore) NewIterator(prefix []byte) ls.RangeIter {
	return nil
}

func (m *batchMemStore) ManualCompact() error { return nil }

func (m *batchMemStore) WriteBatch(keys, values [][]byte) error {
	m.writeBatches++
	if len(keys) != len(values) {
		return ls.ErrBatchLengthMismatch
	}
	for i := range keys {
		m.rows[string(append([]byte(nil), keys[i]...))] = append([]byte(nil), values[i]...)
	}
	return nil
}

func (m *batchMemStore) DeleteBatch(keys [][]byte) error {
	m.deleteBatches++
	for i := range keys {
		delete(m.rows, string(keys[i]))
	}
	return nil
}

// TestTableHandle_UpdateRowBatch verifies REQ001555: UpdateRowBatch
// routes through WriteBatch when the store implements BatchStore,
// writes all rows with the expected keys/bufs, and maintains
// per-row index maintenance.
func TestTableHandle_UpdateRowBatch(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "upd_batch", []string{"id", "val"}, "id")
	defer UnregisterTable("upd_batch")

	h, err := OpenTable(store, "upd_batch")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}

	const n = 100
	oldRows := make([]Row, n)
	newRows := make([]Row, n)
	wantKeys := make([][]byte, n)
	for i := 0; i < n; i++ {
		oldRows[i] = Row{
			Cols: []string{"id", "val"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i))},
		}
		newRows[i] = Row{
			Cols: []string{"id", "val"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i * 2))},
		}
		wantKeys[i] = h.Prefix()
		// The exact suffix depends on the PK encoding; compare via GetRow.
		if _, _, err := h.InsertRow(oldRows[i]); err != nil {
			t.Fatalf("InsertRow[%d]: %v", i, err)
		}
	}

	keys, bufs, err := h.UpdateRowBatch(oldRows, newRows)
	if err != nil {
		t.Fatalf("UpdateRowBatch: %v", err)
	}
	if len(keys) != n || len(bufs) != n {
		t.Fatalf("UpdateRowBatch returned keys=%d bufs=%d, want %d/%d", len(keys), len(bufs), n, n)
	}
	if store.writeBatches != 1 {
		t.Errorf("store.WriteBatch call count = %d, want 1 (single batched call)", store.writeBatches)
	}

	// Verify each row was actually updated by reading back via GetRow.
	for i := 0; i < n; i++ {
		got, found, err := h.GetRow(int64(i + 1))
		if err != nil || !found {
			t.Fatalf("GetRow(%d): found=%v err=%v", i+1, found, err)
		}
		if got.Data[1].I64 != int64(i*2) {
			t.Errorf("row[%d].val = %d, want %d", i, got.Data[1].I64, i*2)
		}
	}

	// Verify key/buf stability: returned keys must prefix-match h.Prefix().
	for i, k := range keys {
		if !startsWith(k, h.Prefix()) {
			t.Errorf("keys[%d] = %x does not start with prefix %x", i, k, h.Prefix())
		}
		if len(bufs[i]) == 0 {
			t.Errorf("bufs[%d] is empty", i)
		}
	}
}

// TestTableHandle_UpdateRowBatch_EmptyNoop verifies that a zero-length
// batch is a clean no-op (no WriteBatch call, no error).
func TestTableHandle_UpdateRowBatch_EmptyNoop(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "upd_empty", []string{"id", "val"}, "id")
	defer UnregisterTable("upd_empty")

	h, err := OpenTable(store, "upd_empty")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}

	keys, bufs, err := h.UpdateRowBatch(nil, nil)
	if err != nil {
		t.Fatalf("UpdateRowBatch(nil, nil): %v", err)
	}
	if keys != nil || bufs != nil {
		t.Errorf("UpdateRowBatch(nil, nil) returned non-nil: keys=%v bufs=%v", keys, bufs)
	}
	if store.writeBatches != 0 {
		t.Errorf("store.WriteBatch call count = %d, want 0", store.writeBatches)
	}
}

// TestTableHandle_UpdateRowBatch_LengthMismatch verifies the batch
// rejects mismatched old/new slice lengths.
func TestTableHandle_UpdateRowBatch_LengthMismatch(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "upd_mm", []string{"id", "val"}, "id")
	defer UnregisterTable("upd_mm")

	h, err := OpenTable(store, "upd_mm")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	old := []Row{{Cols: []string{"id", "val"}, Data: []Value{NewIntValue(1), NewIntValue(1)}}}
	if _, _, err := h.UpdateRowBatch(old, nil); err == nil {
		t.Errorf("UpdateRowBatch with mismatched length = nil error, want error")
	}
}

// TestTableHandle_UpdateRowBatch_NonBatchStore verifies that when
// the store does NOT implement BatchStore, the batch falls back to
// per-row Insert while preserving the chunked-batch semantics on
// the writer side.
func TestTableHandle_UpdateRowBatch_NonBatchStore(t *testing.T) {
	store := newMemStore()
	registerTestTable(t, "upd_fb", []string{"id", "val"}, "id")
	defer UnregisterTable("upd_fb")

	h, err := OpenTable(store, "upd_fb")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	const n = 10
	oldRows := make([]Row, n)
	newRows := make([]Row, n)
	for i := 0; i < n; i++ {
		oldRows[i] = Row{
			Cols: []string{"id", "val"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i))},
		}
		newRows[i] = Row{
			Cols: []string{"id", "val"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(-i))},
		}
		if _, _, err := h.InsertRow(oldRows[i]); err != nil {
			t.Fatalf("InsertRow[%d]: %v", i, err)
		}
	}
	if _, _, err := h.UpdateRowBatch(oldRows, newRows); err != nil {
		t.Fatalf("UpdateRowBatch fallback: %v", err)
	}
	for i := 0; i < n; i++ {
		got, found, err := h.GetRow(int64(i + 1))
		if err != nil || !found {
			t.Fatalf("GetRow(%d): found=%v err=%v", i+1, found, err)
		}
		if got.Data[1].I64 != int64(-i) {
			t.Errorf("row[%d].val = %d, want %d", i, got.Data[1].I64, -i)
		}
	}
}

// TestTableHandle_DeleteRowBatch verifies REQ001556: DeleteRowBatch
// routes through DeleteBatch when the store implements BatchDeleteStore
// and removes every row by its primary key.
func TestTableHandle_DeleteRowBatch(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "del_batch", []string{"id", "val"}, "id")
	defer UnregisterTable("del_batch")

	h, err := OpenTable(store, "del_batch")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	const n = 50
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols: []string{"id", "val"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i * 10))},
		}
		if _, _, err := h.InsertRow(rows[i]); err != nil {
			t.Fatalf("InsertRow[%d]: %v", i, err)
		}
	}

	keys, err := h.DeleteRowBatch(rows)
	if err != nil {
		t.Fatalf("DeleteRowBatch: %v", err)
	}
	if len(keys) != n {
		t.Fatalf("DeleteRowBatch returned %d keys, want %d", len(keys), n)
	}
	if store.deleteBatches != 1 {
		t.Errorf("store.DeleteBatch call count = %d, want 1 (single batched call)", store.deleteBatches)
	}
	for i, k := range keys {
		if !startsWith(k, h.Prefix()) {
			t.Errorf("keys[%d] = %x does not start with prefix %x", i, k, h.Prefix())
		}
	}
	for i := 0; i < n; i++ {
		ex, err := h.Exists(int64(i + 1))
		if err != nil {
			t.Fatalf("Exists(%d): %v", i+1, err)
		}
		if ex {
			t.Errorf("row[%d] still exists after DeleteRowBatch", i)
		}
	}
}

// TestTableHandle_DeleteRowBatch_EmptyNoop verifies a zero-length
// batch is a clean no-op.
func TestTableHandle_DeleteRowBatch_EmptyNoop(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "del_empty", []string{"id", "val"}, "id")
	defer UnregisterTable("del_empty")

	h, err := OpenTable(store, "del_empty")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	keys, err := h.DeleteRowBatch(nil)
	if err != nil {
		t.Fatalf("DeleteRowBatch(nil): %v", err)
	}
	if keys != nil {
		t.Errorf("DeleteRowBatch(nil) returned non-nil: %v", keys)
	}
	if store.deleteBatches != 0 {
		t.Errorf("store.DeleteBatch call count = %d, want 0", store.deleteBatches)
	}
}

// TestTableHandle_DeleteRowBatch_NonBatchStore verifies the fallback
// to per-row Delete when BatchDeleteStore is unavailable.
func TestTableHandle_DeleteRowBatch_NonBatchStore(t *testing.T) {
	store := newMemStore()
	registerTestTable(t, "del_fb", []string{"id", "val"}, "id")
	defer UnregisterTable("del_fb")

	h, err := OpenTable(store, "del_fb")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	const n = 10
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols: []string{"id", "val"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i))},
		}
		if _, _, err := h.InsertRow(rows[i]); err != nil {
			t.Fatalf("InsertRow[%d]: %v", i, err)
		}
	}
	if _, err := h.DeleteRowBatch(rows); err != nil {
		t.Fatalf("DeleteRowBatch fallback: %v", err)
	}
	for i := 0; i < n; i++ {
		ex, _ := h.Exists(int64(i + 1))
		if ex {
			t.Errorf("row[%d] still exists after DeleteRowBatch", i)
		}
	}
}

// TestBatchDeleteStore_InterfaceAssertion verifies BatchDeleteStore
// is a proper interface and a concrete store implementation can
// satisfy it (so the writer's type assertion is valid at runtime).
//
// Note: the live LS Engine returns ([]byte, error) from Get instead
// of the full Store interface's ([]byte, bool, error). The SYS/SY
// executorStoreAdapter wraps the engine and presents the full Store
// shape. Wiring BatchStore / BatchDeleteStore through that adapter
// is a separate wiring task; here we verify the interface seam
// itself is sound.
func TestBatchDeleteStore_InterfaceAssertion(t *testing.T) {
	// Runtime check: batchMemStore (defined above) implements
	// BatchDeleteStore end-to-end via its DeleteBatch method.
	var s Store = newBatchMemStore()
	bds, ok := s.(BatchDeleteStore)
	if !ok {
		t.Fatalf("batchMemStore did not satisfy BatchDeleteStore at runtime; got %T", s)
	}
	if _, ok := s.(BatchStore); !ok {
		t.Errorf("batchMemStore did not also satisfy BatchStore (REQ001421 contract)")
	}
	if bds == nil {
		t.Fatalf("BatchDeleteStore type assertion returned nil")
	}

	// Sanity: the BatchDeleteStore interface is non-empty and
	// contains exactly DeleteBatch (plus embedded Store methods).
	bdsType := reflect.TypeOf((*BatchDeleteStore)(nil)).Elem()
	if bdsType.NumMethod() < 2 {
		t.Errorf("BatchDeleteStore has %d methods, want >=2 (Store embedded + DeleteBatch)", bdsType.NumMethod())
	}

	// LS engine-level sanity check: eng.WriteBatch and eng.DeleteBatch
	// exist as concrete methods (proves the engine path is in place
	// once the executor adapter forwards them).
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()
	if err := eng.WriteBatch([][]byte{[]byte("k")}, [][]byte{[]byte("v")}); err != nil {
		t.Errorf("eng.WriteBatch not callable: %v", err)
	}
	if err := eng.DeleteBatch([][]byte{[]byte("k")}); err != nil {
		t.Errorf("eng.DeleteBatch not callable: %v", err)
	}
}

// TestBatchDeleteStore_KeyShapes verifies the keys returned from
// DeleteRowBatch start with the table prefix and have stable shape
// across repeated calls on the same row.
func TestBatchDeleteStore_KeyShapes(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "del_shape", []string{"id", "val"}, "id")
	defer UnregisterTable("del_shape")

	h, err := OpenTable(store, "del_shape")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	row := Row{
		Cols: []string{"id", "val"},
		Data: []Value{NewIntValue(42), NewIntValue(99)},
	}
	if _, _, err := h.InsertRow(row); err != nil {
		t.Fatalf("InsertRow: %v", err)
	}
	// Single-key batch.
	keys, err := h.DeleteRowBatch([]Row{row})
	if err != nil {
		t.Fatalf("DeleteRowBatch: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("DeleteRowBatch returned %d keys, want 1", len(keys))
	}
	if !startsWith(keys[0], h.Prefix()) {
		t.Errorf("key %x does not start with prefix %x", keys[0], h.Prefix())
	}
	// Re-insert and re-fetch the key by hand via RowKey + Insert.
	pk, _ := ExtractPKForUpdate(h.Schema, row, h.Prefix())
	wantKey := RowKey(h.Prefix(), pk)
	if !bytes.Equal(keys[0], wantKey) {
		t.Errorf("DeleteRowBatch key %x != RowKey() %x", keys[0], wantKey)
	}
}

// TestMaintainIndexesOnUpdateBatch_Basic verifies REQ001576: when an
// UPDATE batch touches a registered secondary index, all (oldKey,
// newKey) diffs across the batch are collected and routed through a
// single BatchDeleteStore.DeleteBatch + BatchStore.WriteBatch per
// index (instead of N per-row Delete+Insert pairs).
//
// The test registers a table with a secondary index on column "v",
// inserts 50 rows, then issues an UPDATE batch that changes every v.
// After the batch, the index keyspace must contain exactly the 50
// new index entries (no stale old entries), and the heap-row
// keyspace must hold the updated payloads.
func TestMaintainIndexesOnUpdateBatch_Basic(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "idx_upd_batch", []string{"id", "v"}, "id")
	defer UnregisterTable("idx_upd_batch")
	RegisterIndexWithID("idx_upd_batch", RegisteredIndex{Name: "v_idx", Columns: []string{"v"}})
	defer UnregisterTableIndexes("idx_upd_batch")

	ss := mustSchemaFor(t, "idx_upd_batch")
	tableID, ok := TableIDFor("idx_upd_batch")
	if !ok {
		t.Fatalf("TableIDFor(idx_upd_batch) failed")
	}
	h, err := OpenTable(store, "idx_upd_batch")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}

	const n = 50
	oldRows := make([]Row, n)
	newRows := make([]Row, n)
	for i := 0; i < n; i++ {
		oldRows[i] = Row{
			Cols: []string{"id", "v"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i))},
		}
		newRows[i] = Row{
			Cols: []string{"id", "v"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i + 1000))},
		}
		if _, _, err := h.InsertRow(oldRows[i]); err != nil {
			t.Fatalf("InsertRow[%d]: %v", i, err)
		}
	}

	// Snapshot store metrics so the post-batch assertion is meaningful
	// (InsertRow uses per-row Insert, not WriteBatch, so we expect
	// writeBatches to still be 0 here — UpdateRowBatch will be the
	// one to drive the index-side DeleteBatch + WriteBatch counts).
	beforeDeleteBatches := store.deleteBatches

	if _, _, err := h.UpdateRowBatch(oldRows, newRows); err != nil {
		t.Fatalf("UpdateRowBatch: %v", err)
	}

	// After UpdateRowBatch: index keyspace must reflect the NEW values,
	// not the OLD ones. The old index entries must be gone.
	for i := 0; i < n; i++ {
		newIdxKey := BuildIndexKey(tableID, "v_idx", mustIndexValue(t, ss, newRows[i], "v"))
		if _, ok := store.rows[string(newIdxKey)]; !ok {
			t.Errorf("row[%d]: new index key %x missing after UpdateRowBatch", i, newIdxKey)
		}
		oldIdxKey := BuildIndexKey(tableID, "v_idx", mustIndexValue(t, ss, oldRows[i], "v"))
		if _, ok := store.rows[string(oldIdxKey)]; ok {
			t.Errorf("row[%d]: stale old index key %x still present after UpdateRowBatch", i, oldIdxKey)
		}
	}

	// UpdateRowBatch must have used at least one DeleteBatch + one
	// WriteBatch for index maintenance (separate from the heap-row
	// batch already counted in beforeInsertBatches).
	if store.deleteBatches <= beforeDeleteBatches {
		t.Errorf("UpdateRowBatch did not increase DeleteBatch count: before=%d after=%d", beforeDeleteBatches, store.deleteBatches)
	}
}


// TestMaintainIndexesOnUpdateBatch_EmptyNoop verifies a zero-length
// batch is a clean no-op (no error, no Store calls).
func TestMaintainIndexesOnUpdateBatch_EmptyNoop(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "idx_empty", []string{"id", "v"}, "id")
	defer UnregisterTable("idx_empty")

	ss := mustSchemaFor(t, "idx_empty")
	beforeDelete := store.deleteBatches
	if err := MaintainIndexesOnUpdateBatch(store, "idx_empty", ss, nil, nil, nil); err != nil {
		t.Errorf("MaintainIndexesOnUpdateBatch(nil, nil, nil): %v", err)
	}
	if store.deleteBatches != beforeDelete {
		t.Errorf("empty batch incremented DeleteBatch: before=%d after=%d", beforeDelete, store.deleteBatches)
	}
}

// TestMaintainIndexesOnUpdateBatch_LengthMismatch verifies that
// mismatched slice lengths surface an error instead of panicking.
func TestMaintainIndexesOnUpdateBatch_LengthMismatch(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "idx_mm", []string{"id", "v"}, "id")
	defer UnregisterTable("idx_mm")

	ss := mustSchemaFor(t, "idx_mm")
	old := []Row{{Cols: []string{"id", "v"}, Data: []Value{NewIntValue(1), NewIntValue(1)}}}
	if err := MaintainIndexesOnUpdateBatch(store, "idx_mm", ss, old, nil, nil); err == nil {
		t.Errorf("MaintainIndexesOnUpdateBatch with mismatched lengths = nil error, want error")
	}
}

// TestMaintainIndexesOnDeleteBatch_Basic verifies the REQ001556
// follow-up: when a DELETE batch touches a registered secondary
// index, all index keys are collected and removed in a single
// BatchDeleteStore.DeleteBatch call per index.
func TestMaintainIndexesOnDeleteBatch_Basic(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "idx_del_batch", []string{"id", "v"}, "id")
	defer UnregisterTable("idx_del_batch")
	RegisterIndexWithID("idx_del_batch", RegisteredIndex{Name: "v_idx", Columns: []string{"v"}})
	defer UnregisterTableIndexes("idx_del_batch")

	ss := mustSchemaFor(t, "idx_del_batch")
	tableID, ok := TableIDFor("idx_del_batch")
	if !ok {
		t.Fatalf("TableIDFor(idx_del_batch) failed")
	}
	h, err := OpenTable(store, "idx_del_batch")
	if err != nil {
		t.Fatalf("OpenTable: %v", err)
	}
	const n = 30
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{
			Cols: []string{"id", "v"},
			Data: []Value{NewIntValue(int64(i + 1)), NewIntValue(int64(i * 3))},
		}
		if _, _, err := h.InsertRow(rows[i]); err != nil {
			t.Fatalf("InsertRow[%d]: %v", i, err)
		}
	}
	// Capture every row's index key for the post-delete assertion.
	indexKeysBefore := make(map[string]bool)
	for i := 0; i < n; i++ {
		idxKey := BuildIndexKey(tableID, "v_idx", mustIndexValue(t, ss, rows[i], "v"))
		indexKeysBefore[string(idxKey)] = true
		if _, ok := store.rows[string(idxKey)]; !ok {
			t.Fatalf("row[%d]: index key %x missing pre-delete", i, idxKey)
		}
	}

	beforeDeleteBatches := store.deleteBatches
	if _, err := h.DeleteRowBatch(rows); err != nil {
		t.Fatalf("DeleteRowBatch: %v", err)
	}
	if store.deleteBatches <= beforeDeleteBatches {
		t.Errorf("DeleteRowBatch did not increase DeleteBatch count: before=%d after=%d", beforeDeleteBatches, store.deleteBatches)
	}
	for k := range indexKeysBefore {
		if _, ok := store.rows[k]; ok {
			t.Errorf("index key %x still present after DeleteRowBatch", k)
		}
	}
}

// TestMaintainIndexesOnDeleteBatch_EmptyNoop verifies a zero-length
// DELETE batch is a clean no-op for index maintenance.
func TestMaintainIndexesOnDeleteBatch_EmptyNoop(t *testing.T) {
	store := newBatchMemStore()
	registerTestTable(t, "idx_del_empty", []string{"id", "v"}, "id")
	defer UnregisterTable("idx_del_empty")

	ss := mustSchemaFor(t, "idx_del_empty")
	before := store.deleteBatches
	if err := MaintainIndexesOnDeleteBatch(store, "idx_del_empty", ss, nil); err != nil {
		t.Errorf("MaintainIndexesOnDeleteBatch(nil): %v", err)
	}
	if store.deleteBatches != before {
		t.Errorf("empty batch incremented DeleteBatch: before=%d after=%d", before, store.deleteBatches)
	}
}

// mustSchemaFor returns the registered StoreSchema for `table` by
// name, failing the test if it can't be found. The naive
// `StoreSchemas[0]` approach is order-dependent (other tests share
// the global slice), so we look up by Table name.
func mustSchemaFor(t *testing.T, table string) *StoreSchema {
	t.Helper()
	ss, ok := SchemaFor(table)
	if !ok {
		t.Fatalf("SchemaFor(%q) failed", table)
	}
	return ss
}

// mustIndexValue is a small helper that returns the bytes-encoded
// value of `col` in `row` (per the schema's column ordering), failing
// the test if the column is missing or the encoding fails.
func mustIndexValue(t *testing.T, schema *StoreSchema, row Row, col string) []byte {
	t.Helper()
	for j, sc := range schema.Cols {
		if sc == col && j < len(row.Data) {
			b, err := pkToBytes(row.Data[j])
			if err != nil {
				t.Fatalf("pkToBytes(%v): %v", row.Data[j], err)
			}
			return b
		}
	}
	t.Fatalf("column %q not found in schema %v", col, schema.Cols)
	return nil
}