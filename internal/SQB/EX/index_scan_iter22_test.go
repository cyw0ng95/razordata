package EX

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// TestIndexScan_WithIndexSeek exercises the iter-22 real index
// seek path. We insert rows into a table, build a secondary
// index, then run an OP.IndexScan that uses the index to seek.
func TestIndexScan_WithIndexSeek(t *testing.T) {
	dir := t.TempDir()
	eng, err := ls.Open(dir)
	if err != nil {
		t.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()

	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)

	// Register table and a real secondary index
	ex.RegisterTableWithPK("users", []string{"id", "email", "name"}, "id")
	id, ok := DT.TableIDFor("users")
	if !ok {
		t.Fatal("users not registered")
	}
	_ = id

	// Insert rows: write through the regular table path
	ctx := context.Background()
	// Register the index in the LS layer
	idxStore := ls.NewIndexStore(eng, id, "idx_email")
	for _, r := range []struct {
		id    int64
		email string
		name  string
	}{
		{1, "alice@x.com", "Alice"},
		{2, "bob@x.com", "Bob"},
		{3, "carol@x.com", "Carol"},
	} {
		row := fmt.Sprintf(`INSERT INTO users VALUES (%d, '%s', '%s')`, r.id, r.email, r.name)
		if _, err := ex.Exec(ctx, row); err != nil {
			t.Fatalf("insert %d: %v", r.id, err)
		}
		// Manually populate the index (Block F will automate this)
		if err := idxStore.Insert([]byte(r.email), int64ToBytes(r.id)); err != nil {
			t.Fatalf("idxStore.Insert: %v", err)
		}
	}

	// Build an OP.IndexScan that seeks for "bob@x.com"
	scan, err := OP.NewIndexScanWithIndex(store, id, "users", "idx_email", []byte("bob@x.com"), nil)
	if err != nil {
		t.Fatalf("NewIndexScanWithIndex: %v", err)
	}
	defer scan.Close()

	row, err := scan.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// row.Data[0] is id, row.Data[1] is email
	if !row.Data[0].Equal(NewIntValue(int64(2))) {
		t.Errorf("row id = %v, want 2", row.Data[0])
	}
	if !row.Data[1].Equal(NewTextValue("bob@x.com")) {
		t.Errorf("row email = %v, want bob@x.com", row.Data[1])
	}

	// Next call should return ErrNoRows
	_, err = scan.Next(ctx)
	if err != DT.ErrNoRows {
		t.Errorf("second Next: got %v, want ErrNoRows", err)
	}
}

// int64ToBytes encodes an int64 as a big-endian byte slice.
func int64ToBytes(n int64) []byte {
	b := make([]byte, 8)
	u := uint64(n)
	b[7] = byte(u)
	b[6] = byte(u >> 8)
	b[5] = byte(u >> 16)
	b[4] = byte(u >> 24)
	b[3] = byte(u >> 32)
	b[2] = byte(u >> 40)
	b[1] = byte(u >> 48)
	b[0] = byte(u >> 56)
	return b
}

// TestIndexScan_BuildIndexKey verifies the key encoding format.
func TestIndexScan_BuildIndexKey(t *testing.T) {
	key := OP.BuildIndexKey(7, "idx_email", []byte("alice"))
	want := []byte("__idx__:")
	want = append(want, 0, 0, 0, 0, 0, 0, 0, 7)
	want = append(want, ':')
	want = append(want, "idx_email"...)
	want = append(want, ':')
	want = append(want, "alice"...)
	if !bytes.Equal(key, want) {
		t.Errorf("buildIndexKey = %q, want %q", key, want)
	}
}

// TestIndexScan_WithStore_Fallback is a regression test for the
// existing (non-iter-22) prefix-scan path. We keep both paths
// because the iter-22 path requires manual index population
// (Block F will automate it via DML hooks).
func TestIndexScan_WithStore_Fallback(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t_fallback", []string{"id", "a"}, "id")
	ex.RegisterIndex("t_fallback", "idx_a", []string{"a"})

	ctx := context.Background()
	for _, s := range []string{
		"INSERT INTO t_fallback VALUES (1, 'x')",
		"INSERT INTO t_fallback VALUES (2, 'y')",
		"INSERT INTO t_fallback VALUES (3, 'x')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT id FROM t_fallback WHERE a = 'x'")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}

// TestIndexScan_CloseWithIndex verifies Close releases the
// index iterator.
func TestIndexScan_CloseWithIndex(t *testing.T) {
	dir := t.TempDir()
	eng, _ := ls.Open(dir)
	defer eng.Close()
	store := &engineStore{eng: eng}
	ex := NewExecutorWithEngine(store)
	ex.RegisterTableWithPK("t", []string{"id", "a"}, "id")
	id, _ := DT.TableIDFor("t")
	_ = id

	ctx := context.Background()
	_, _ = ex.Exec(ctx, "INSERT INTO t VALUES (1, 'x')")

	scan, _ := OP.NewIndexScanWithIndex(store, id, "t", "idx_a", []byte("x"), nil)
	if err := scan.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Second close should be idempotent
	if err := scan.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// guard against unused import
var _ = PS.TypeInfo{}
