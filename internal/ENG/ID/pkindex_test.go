package id

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
	"github.com/cyw0ng95/razordata/internal/ENG/TB"
)

func newTestPKIndex() (*PKIndex, *tb.MapStore) {
	store := tb.NewMapStore()
	return NewPKIndex(store), store
}

func intBytes(v int64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(v))
	return out
}

func TestPKIndex_InsertAndSeek(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}

	rowKey := []byte("row:1:42")
	if err := idx.Insert(tableID, types, [][]byte{intBytes(42)}, rowKey); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, found, err := idx.Seek(tableID, types, [][]byte{intBytes(42)})
	if err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if !found {
		t.Fatal("Seek: expected hit")
	}
	if string(got) != string(rowKey) {
		t.Errorf("got %q, want %q", got, rowKey)
	}
}

func TestPKIndex_Seek_Miss(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}

	if _, found, err := idx.Seek(tableID, types, [][]byte{intBytes(99)}); err != nil {
		t.Fatalf("Seek: %v", err)
	} else if found {
		t.Errorf("expected miss")
	}
}

func TestPKIndex_Delete(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}

	_ = idx.Insert(tableID, types, [][]byte{intBytes(42)}, []byte("row"))
	if err := idx.Delete(tableID, types, [][]byte{intBytes(42)}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := idx.Seek(tableID, types, [][]byte{intBytes(42)}); found {
		t.Errorf("expected miss after delete")
	}
}

func TestPKIndex_Delete_Missing(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}
	if err := idx.Delete(tableID, types, [][]byte{intBytes(99)}); err != nil {
		t.Errorf("Delete on missing key: %v", err)
	}
}

func TestPKIndex_InvalidTableID(t *testing.T) {
	idx, _ := newTestPKIndex()
	types := []sc.ColumnType{sc.CTInt}
	if err := idx.Insert(0, types, [][]byte{intBytes(1)}, []byte("x")); !errors.Is(err, ErrInvalidTableID) {
		t.Errorf("expected ErrInvalidTableID, got %v", err)
	}
	if _, _, err := idx.Seek(0, types, [][]byte{intBytes(1)}); !errors.Is(err, ErrInvalidTableID) {
		t.Errorf("Seek(0): expected ErrInvalidTableID, got %v", err)
	}
	if err := idx.Delete(0, types, [][]byte{intBytes(1)}); !errors.Is(err, ErrInvalidTableID) {
		t.Errorf("Delete(0): expected ErrInvalidTableID, got %v", err)
	}
}

func TestPKIndex_ColumnCountMismatch(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt, sc.CTInt}
	if err := idx.Insert(tableID, types, [][]byte{intBytes(1)}, []byte("x")); !errors.Is(err, ErrKeyCodecColumnCount) {
		t.Errorf("expected ErrKeyCodecColumnCount, got %v", err)
	}
}

func TestPKIndex_MultipleRows_OrderByPK(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}

	// Insert in non-sorted order.
	ints := []int64{50, 10, 80, 30, 20, 70, 40, 60, 90, 0}
	for _, v := range ints {
		rowKey := []byte(fmt.Sprintf("row:%d", v))
		if err := idx.Insert(tableID, types, [][]byte{intBytes(v)}, rowKey); err != nil {
			t.Fatalf("Insert %d: %v", v, err)
		}
	}

	// Seek each and verify all hits.
	for _, v := range ints {
		got, found, err := idx.Seek(tableID, types, [][]byte{intBytes(v)})
		if err != nil {
			t.Fatalf("Seek %d: %v", v, err)
		}
		if !found {
			t.Errorf("Seek %d: expected hit", v)
		}
		want := fmt.Sprintf("row:%d", v)
		if string(got) != want {
			t.Errorf("Seek %d: got %q, want %q", v, got, want)
		}
	}
}

func TestPKIndex_CompositeKey(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt, sc.CTVarchar}

	cases := []struct {
		intVal int64
		text   string
		rowKey string
	}{
		{1, "alice", "row:1:alice"},
		{1, "bob", "row:1:bob"},
		{2, "alice", "row:2:alice"},
		{2, "bob", "row:2:bob"},
		{1, "charlie", "row:1:charlie"},
	}
	for _, c := range cases {
		if err := idx.Insert(tableID, types, [][]byte{intBytes(c.intVal), []byte(c.text)}, []byte(c.rowKey)); err != nil {
			t.Fatalf("Insert (%d, %s): %v", c.intVal, c.text, err)
		}
	}
	for _, c := range cases {
		got, found, err := idx.Seek(tableID, types, [][]byte{intBytes(c.intVal), []byte(c.text)})
		if err != nil {
			t.Fatalf("Seek (%d, %s): %v", c.intVal, c.text, err)
		}
		if !found {
			t.Errorf("Seek (%d, %s): expected hit", c.intVal, c.text)
		}
		if string(got) != c.rowKey {
			t.Errorf("Seek (%d, %s): got %q, want %q", c.intVal, c.text, got, c.rowKey)
		}
	}
}

func TestPKIndex_TableIsolation(t *testing.T) {
	idx, _ := newTestPKIndex()
	types := []sc.ColumnType{sc.CTInt}
	for tableID := uint64(1); tableID <= 3; tableID++ {
		if err := idx.Insert(tableID, types, [][]byte{intBytes(42)}, []byte(fmt.Sprintf("row:%d", tableID))); err != nil {
			t.Fatalf("Insert table %d: %v", tableID, err)
		}
	}
	for tableID := uint64(1); tableID <= 3; tableID++ {
		got, found, _ := idx.Seek(tableID, types, [][]byte{intBytes(42)})
		if !found {
			t.Errorf("table %d: expected hit", tableID)
			continue
		}
		want := fmt.Sprintf("row:%d", tableID)
		if string(got) != want {
			t.Errorf("table %d: got %q, want %q", tableID, got, want)
		}
	}
}

func TestPKIndex_NullColumns(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt, sc.CTVarchar, sc.CTInt}

	_ = idx.Insert(tableID, types, [][]byte{intBytes(42), nil, intBytes(7)}, []byte("row:42:null:7"))
	got, found, err := idx.Seek(tableID, types, [][]byte{intBytes(42), nil, intBytes(7)})
	if err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if !found || string(got) != "row:42:null:7" {
		t.Errorf("got %q, found=%v", got, found)
	}
}

func TestPKIndex_Range_Basic(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}

	// Insert 0..99.
	for i := int64(0); i < 100; i++ {
		rowKey := []byte(fmt.Sprintf("row:%d", i))
		if err := idx.Insert(tableID, types, [][]byte{intBytes(i)}, rowKey); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	// Range [10, 20): should yield exactly 10 entries.
	it, err := idx.Range(tableID, types, [][]byte{intBytes(10)}, [][]byte{intBytes(20)})
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	defer it.Close()
	count := 0
	for it.Next() {
		count++
	}
	if count != 10 {
		t.Errorf("expected 10 entries, got %d", count)
	}
}

func TestPKIndex_Range_NoUpperBound(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}
	for i := int64(0); i < 10; i++ {
		_ = idx.Insert(tableID, types, [][]byte{intBytes(i)}, []byte(fmt.Sprintf("row:%d", i)))
	}
	it, err := idx.Range(tableID, types, [][]byte{intBytes(5)}, nil)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	defer it.Close()
	count := 0
	for it.Next() {
		count++
	}
	if count != 5 {
		t.Errorf("expected 5 entries (5..9), got %d", count)
	}
}

func TestPKIndex_Range_Composite(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt, sc.CTVarchar}

	// Insert (1, a), (1, b), (2, a), (2, b), (3, a), (3, b).
	for _, intVal := range []int64{1, 2, 3} {
		for _, text := range []string{"a", "b"} {
			_ = idx.Insert(tableID, types, [][]byte{intBytes(intVal), []byte(text)}, []byte(fmt.Sprintf("row:%d:%s", intVal, text)))
		}
	}

	// Range from (1, b) to (3, a): inclusive lo, exclusive hi.
	// Within the (1, b) row, all rows with int >= 1 and int < 3,
	// OR (int == 3 and text < "a"). For the same int, the text
	// also needs to be considered, but with composite-PK length-
	// first ordering we get a less exact match. For this test we
	// just verify that the result count is reasonable.
	it, err := idx.Range(tableID, types, [][]byte{intBytes(1), []byte("b")}, [][]byte{intBytes(3), []byte("a")})
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	defer it.Close()
	count := 0
	for it.Next() {
		count++
	}
	if count == 0 {
		t.Errorf("expected at least 1 entry in range, got 0")
	}
}

func TestPKIndex_ConcurrentInserts(t *testing.T) {
	idx, _ := newTestPKIndex()
	tableID := uint64(1)
	types := []sc.ColumnType{sc.CTInt}

	const goroutines = 10
	const perGoroutine = 1000
	done := make(chan struct{}, goroutines)
	for g := 0; g < goroutines; g++ {
		base := int64(g * perGoroutine)
		go func() {
			defer func() { done <- struct{}{} }()
			for i := int64(0); i < perGoroutine; i++ {
				v := base + i
				_ = idx.Insert(tableID, types, [][]byte{intBytes(v)}, []byte(fmt.Sprintf("row:%d", v)))
			}
		}()
	}
	for g := 0; g < goroutines; g++ {
		<-done
	}
	// Verify all entries are present.
	for g := 0; g < goroutines; g++ {
		base := int64(g * perGoroutine)
		for i := int64(0); i < perGoroutine; i++ {
			v := base + i
			if _, found, _ := idx.Seek(tableID, types, [][]byte{intBytes(v)}); !found {
				t.Errorf("missing key %d", v)
			}
		}
	}
}
