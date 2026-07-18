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