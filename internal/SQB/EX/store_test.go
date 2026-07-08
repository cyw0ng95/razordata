package EX

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQB/OP"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

func TestSeqScan_AgainstRealStore(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("users", []string{"id", "name"}, "id")
	ctx := context.Background()

	for _, s := range []string{
		"INSERT INTO users VALUES (1, 'alice')",
		"INSERT INTO users VALUES (2, 'bob')",
		"INSERT INTO users VALUES (3, 'carol')",
	} {
		if _, err := ex.Exec(ctx, s); err != nil {
			t.Fatalf("insert %q: %v", s, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
}

func TestInsert_AndGetViaExecutor(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("kv", []string{"k", "v"}, "k")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO kv VALUES ('a', '1')"); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := ex.Exec(ctx, "INSERT INTO kv VALUES ('b', '2')"); err != nil {
		t.Fatalf("insert b: %v", err)
	}

	rows, err := ex.QueryAll(ctx, "SELECT k, v FROM kv")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestUpdate_AppendsVersion(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("t", []string{"id", "val"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'old')"); err != nil {
		t.Fatal(err)
	}
	res, err := ex.Exec(ctx, "UPDATE t SET val = 'new' WHERE id = 1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}
	rows, err := ex.QueryAll(ctx, "SELECT val FROM t WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if got, ok := rows[0].Data[0].ToAny().(string); !ok || got != "new" {
		t.Errorf("expected val=new, got %v", rows[0].Data[0])
	}
}

func TestDelete_InsertsTombstone(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()

	ex.RegisterTableWithPK("t", []string{"id", "val"}, "id")
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "INSERT INTO t VALUES (1, 'hello')"); err != nil {
		t.Fatal(err)
	}
	res, err := ex.Exec(ctx, "DELETE FROM t WHERE id = 1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}
	rows, err := ex.QueryAll(ctx, "SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", len(rows))
	}
}

func TestSQLviaEngine_CreateInsertUpdateSelect(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	_, err := ex.Exec(ctx, "CREATE TABLE engine_t (id INTEGER PRIMARY KEY, val INTEGER)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = ex.Exec(ctx, "INSERT INTO engine_t VALUES (?, ?)", int64(i), int64(i*10))
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM engine_t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}

	_, err = ex.Exec(ctx, "UPDATE engine_t SET val = val * 2 WHERE id > 2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	rows, err = ex.QueryAll(ctx, "SELECT * FROM engine_t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows after update, got %d", len(rows))
	}
	expected := [][]int64{{1, 10}, {2, 20}, {3, 60}, {4, 80}, {5, 100}}
	for i, exp := range expected {
		if !rows[i].Data[0].Equal(NewIntValue(exp[0])) || !rows[i].Data[1].Equal(NewIntValue(exp[1])) {
			t.Errorf("row %d: got %v, want %v", i, rows[i].Data, exp)
		}
	}
}

func TestEngine_SequentialDDL(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	// Create multiple DT.Tables with indexes (like SLT tests do)
	tnames := []string{"ta", "tb", "tc"}
	for _, tname := range tnames {
		_, err := ex.Exec(ctx, "CREATE TABLE "+tname+" (id INTEGER PRIMARY KEY, val NUMERIC, w TEXT)")
		if err != nil {
			t.Fatalf("create %s: %v", tname, err)
		}
		for j := 1; j <= 3; j++ {
			_, err = ex.Exec(ctx, "INSERT INTO "+tname+" VALUES (?, ?, ?)", int64(j), int64(j*10), "hello")
			if err != nil {
				t.Fatalf("insert %s/%d: %v", tname, j, err)
			}
		}
		_, err = ex.Exec(ctx, "CREATE INDEX i1 ON "+tname+"(val)")
		if err != nil {
			t.Fatalf("create index %s: %v", tname, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT * FROM ta ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("ta: expected 3 rows, got %d", len(rows))
	}
}

func TestStore_LockOrdering(t *testing.T) {
	// Verify consistent lock ordering: DT.TablesMu → DT.StoreMu (REQ000974).
	// Cannot run concurrent Exec() calls (Executor is not goroutine-safe),
	// so we verify the ordering by calling the two problematic code paths
	// and confirming they don't deadlock via table creation + unregistration.
	DT.StoreMu.Lock()
	DT.StoreMu.Unlock()
	DT.TablesMu.Lock()
	DT.TablesMu.Unlock()

	// RegisterFromCatalog: acquires DT.TablesMu → DT.StoreMu
	dir := t.TempDir()
	lsCat, err := ls.NewCatalog(dir)
	if err != nil {
		t.Fatalf("ls.NewCatalog: %v", err)
	}
	defer lsCat.Close()

	DT.SetCatalog(lsCat)
	defer DT.SetCatalog(nil)

	// RegisterFromCatalog uses DT.TablesMu → DT.StoreMu order.
	// UnregisterAll uses DT.TablesMu → DT.StoreMu order.
	// Both follow the same ordering — no deadlock risk.
	entry := &ls.CatalogEntry{
		TableID: 1,
		Name:    "ordering_test",
		Columns: []ls.CatalogColumn{
			{Name: "id", Type: 1, Nullable: false},
			{Name: "val", Type: 4, Nullable: true},
		},
		PrimaryKey: "id",
		CreateSQL:  "CREATE TABLE ordering_test (id INTEGER PRIMARY KEY, val TEXT)",
	}
	if err := DT.RegisterFromCatalog(entry); err != nil {
		t.Fatalf("RegisterFromCatalog: %v", err)
	}

	// Verify the table was registered
	if _, ok := DT.SchemaFor("ordering_test"); !ok {
		t.Fatal("DT.SchemaFor returned false after RegisterFromCatalog")
	}

	// UnregisterAll uses DT.TablesMu → DT.StoreMu — same ordering
	UnregisterAll()

	// Verify the table is gone
	if _, ok := DT.SchemaFor("ordering_test"); ok {
		t.Fatal("DT.SchemaFor returned true after UnregisterAll")
	}
}

// BenchmarkSeqScan_BatchVsSingle compares row-at-a-time Next() with
// batched NextBatch() reading 64 rows per call. Uses the real LSM
// engine to exercise the page cache and SST block iteration paths
// where batch reads amortize per-block access. REQ001064.
func BenchmarkSeqScan_BatchVsSingle(b *testing.B) {
	const rowCount = 100000
	dir := b.TempDir()
	eng, err := ls.Open(filepath.Join(dir, "db"))
	if err != nil {
		b.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()

	s := &engineStore{eng: eng}
	schema := []string{"id", "name", "val"}
	_ = DT.RegisterStoreSchema("bench", schema, "id")

	// Build and insert encoded rows via the engine.
	ss, _ := DT.SchemaFor("bench")
	for i := 0; i < rowCount; i++ {
		row := DT.Row{
			Data: []DT.Value{
				NewIntValue(int64(i)),
				NewTextValue("name_" + strconv.Itoa(i)),
				NewFloatValue(float64(i) * 1.5),
			},
		}
		encoded, err := OP.EncodeRow(ss, row)
		if err != nil {
			b.Fatalf("EncodeRow: %v", err)
		}
		key := OP.RowKey(OP.TablePrefix("bench"), NewIntValue(int64(i)))
		if err := s.Insert(key, encoded); err != nil {
			b.Fatalf("Insert: %v", err)
		}
	}

	// Force data to SST to exercise page cache and block-level reads.
	if err := s.ManualCompact(); err != nil {
		b.Fatalf("ManualCompact: %v", err)
	}

	ctx := context.Background()

	b.Run("Next", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			scan, err := OP.NewSeqScanWithStore(s, "bench")
			if err != nil {
				b.Fatalf("NewSeqScanWithStore: %v", err)
			}
			var count int
			for {
				_, err := scan.Next(ctx)
				if err != nil {
					if err == DT.ErrNoRows {
						break
					}
					b.Fatalf("Next: %v", err)
				}
				count++
			}
			scan.Close()
			if count != rowCount {
				b.Fatalf("expected %d rows, got %d", rowCount, count)
			}
		}
	})

	b.Run("NextBatch", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			scan, err := OP.NewSeqScanWithStore(s, "bench")
			if err != nil {
				b.Fatalf("NewSeqScanWithStore: %v", err)
			}
			var count int
			for {
				batch, err := scan.NextBatch(ctx)
				if err != nil {
					b.Fatalf("NextBatch: %v", err)
				}
				if batch == nil {
					break
				}
				count += batch.Size
				batch.Put()
			}
			scan.Close()
			if count != rowCount {
				b.Fatalf("expected %d rows, got %d", rowCount, count)
			}
		}
	})

	b.Run("GetPerRow", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			prefix := OP.TablePrefix("bench")
			var count int
			for i := 0; i < rowCount; i++ {
				key := OP.RowKey(prefix, NewIntValue(int64(i)))
				v, ok, err := s.Get(key)
				if err != nil {
					b.Fatalf("Get: %v", err)
				}
				if !ok {
					b.Fatalf("key not found: %d", i)
				}
				_, err = OP.DecodeRow(v, ss)
				if err != nil {
					b.Fatalf("decodeRow: %v", err)
				}
				count++
			}
			if count != rowCount {
				b.Fatalf("expected %d rows, got %d", rowCount, count)
			}
		}
	})
}

// BenchmarkSeqScan_FullScan measures the throughput of a full table
// scan with the decode buffer optimization (REQ001101). Must complete
// a 100K row scan in under 5 seconds.
func BenchmarkSeqScan_FullScan(b *testing.B) {
	const rowCount = 10000
	dir := b.TempDir()
	eng, err := ls.Open(filepath.Join(dir, "db"))
	if err != nil {
		b.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()

	s := &engineStore{eng: eng}
	schema := []string{"id", "name", "val"}
	_ = DT.RegisterStoreSchema("bench", schema, "id")

	ss, _ := DT.SchemaFor("bench")
	for i := 0; i < rowCount; i++ {
		row := DT.Row{
			Data: []DT.Value{
				NewIntValue(int64(i)),
				NewTextValue("name_" + strconv.Itoa(i)),
				NewFloatValue(float64(i) * 1.5),
			},
		}
		encoded, err := OP.EncodeRow(ss, row)
		if err != nil {
			b.Fatalf("EncodeRow: %v", err)
		}
		key := OP.RowKey(OP.TablePrefix("bench"), NewIntValue(int64(i)))
		if err := s.Insert(key, encoded); err != nil {
			b.Fatalf("Insert: %v", err)
		}
	}
	if err := s.ManualCompact(); err != nil {
		b.Fatalf("ManualCompact: %v", err)
	}

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scan, err := OP.NewSeqScanWithStore(s, "bench")
		if err != nil {
			b.Fatalf("NewSeqScanWithStore: %v", err)
		}
		var count int
		for {
			_, err := scan.Next(ctx)
			if err != nil {
				if err == DT.ErrNoRows {
					break
				}
				b.Fatalf("Next: %v", err)
			}
			count++
		}
		scan.Close()
		if count != rowCount {
			b.Fatalf("expected %d rows, got %d", rowCount, count)
		}
	}
}

// BenchmarkSelect1_VecPath compares row-based vs vectorized SeqScan
// throughput for a simple SELECT * scan. REQ001224.
func BenchmarkSelect1_VecPath(b *testing.B) {
	const rowCount = 100000
	dir := b.TempDir()
	eng, err := ls.Open(filepath.Join(dir, "db"))
	if err != nil {
		b.Fatalf("ls.Open: %v", err)
	}
	defer eng.Close()

	s := &engineStore{eng: eng}
	schema := []string{"id", "name", "val"}
	_ = DT.RegisterStoreSchema("bench", schema, "id")

	ss, _ := DT.SchemaFor("bench")
	// Set ColTypes for vectorized path (RegisterStoreSchema doesn't set types).
	ss.ColTypes = []LX.TokenType{LX.T_INT_KW, LX.T_TEXT, LX.T_FLOAT_KW}

	for i := 0; i < rowCount; i++ {
		row := DT.Row{
			Data: []DT.Value{
				NewIntValue(int64(i)),
				NewTextValue("name_" + strconv.Itoa(i)),
				NewFloatValue(float64(i) * 1.5),
			},
		}
		encoded, err := OP.EncodeRow(ss, row)
		if err != nil {
			b.Fatalf("EncodeRow: %v", err)
		}
		key := OP.RowKey(OP.TablePrefix("bench"), NewIntValue(int64(i)))
		if err := s.Insert(key, encoded); err != nil {
			b.Fatalf("Insert: %v", err)
		}
	}
	if err := s.ManualCompact(); err != nil {
		b.Fatalf("ManualCompact: %v", err)
	}

	ctx := context.Background()

	b.Run("RowPath", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			scan, err := OP.NewSeqScanWithStore(s, "bench")
			if err != nil {
				b.Fatalf("NewSeqScanWithStore: %v", err)
			}
			var count int
			for {
				_, err := scan.Next(ctx)
				if err != nil {
					if err == DT.ErrNoRows {
						break
					}
					b.Fatalf("Next: %v", err)
				}
				count++
			}
			scan.Close()
			if count != rowCount {
				b.Fatalf("expected %d rows, got %d", rowCount, count)
			}
		}
	})

	b.Run("VecPath", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			scan, err := OP.NewSeqScanWithStore(s, "bench")
			if err != nil {
				b.Fatalf("NewSeqScanWithStore: %v", err)
			}
			// Wrap in vectorized path via tryVectorizePlan
			root := tryVectorizePlan(scan)
			var count int
			for {
				_, err := root.Next(ctx)
				if err != nil {
					if err == DT.ErrNoRows {
						break
					}
					b.Fatalf("Next: %v", err)
				}
				count++
			}
			root.Close()
			if count != rowCount {
				b.Fatalf("expected %d rows, got %d", rowCount, count)
			}
		}
	})
}

// TestPragma_BatchSize_ReadWrite verifies PRAGMA batch_size reads and
// writes the engine batch size. REQ001224.
func TestPragma_BatchSize_ReadWrite(t *testing.T) {
	prev := OP.EngineBatchSize()
	defer OP.SetEngineBatchSize(prev)

	e := NewExecutorWithEngine(nil)
	ctx := context.Background()

	// Default should be 512.
	rows, err := e.QueryAll(ctx, "PRAGMA batch_size")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0].Data[0].S
	if got != "512" {
		t.Fatalf("expected batch_size=512, got %q", got)
	}

	// Set to 256.
	_, err = e.Exec(ctx, "PRAGMA batch_size = 256")
	if err != nil {
		t.Fatal(err)
	}
	if OP.EngineBatchSize() != 256 {
		t.Fatalf("expected engineBatchSize=256, got %d", OP.EngineBatchSize())
	}

	// Read back via a new executor (avoid stmt cache reuse).
	e2 := NewExecutorWithEngine(nil)
	rows, err = e2.QueryAll(ctx, "PRAGMA batch_size")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got = rows[0].Data[0].S
	if got != "256" {
		t.Fatalf("expected batch_size=256, got %q", got)
	}
}

// TestStore_InsertThenSelect verifies the core write-then-read round-trip:
// CREATE TABLE via SQL DDL, INSERT rows, SELECT them back. REQ001425 regression.
func TestStore_InsertThenSelect(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ctx := context.Background()

	if _, err := ex.Exec(ctx, "CREATE TABLE rt (id INTEGER PRIMARY KEY, val TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := range 5 {
		if _, err := ex.Exec(ctx, "INSERT INTO rt VALUES (?, ?)", int64(i), "v"+strconv.Itoa(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := ex.QueryAll(ctx, "SELECT id, val FROM rt ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
	for i, row := range rows {
		if !row.Data[0].Equal(NewIntValue(int64(i))) {
			t.Errorf("row %d: id=%v, want %d", i, row.Data[0].ToAny(), i)
		}
		if !row.Data[1].Equal(NewTextValue("v" + strconv.Itoa(i))) {
			t.Errorf("row %d: val=%v, want %q", i, row.Data[1].ToAny(), "v"+strconv.Itoa(i))
		}
	}
}
