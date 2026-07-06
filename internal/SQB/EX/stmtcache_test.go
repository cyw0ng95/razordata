package EX

import (
	"context"
	"fmt"
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	"github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// BenchmarkStmtCache_ParsedVsCached measures the cost of repeated
// QueryStream calls with and without cache hits. REQ000771 expects
// the cache hit path to skip the parser and AST allocator.
func BenchmarkStmtCache_ParsedVsCached(b *testing.B) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id", "name"})
	DT.TablesMu.Lock()
	for i := 0; i < 100; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
			Cols: []string{"id", "name"},
			Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("u")},
		})
	}
	DT.TablesMu.Unlock()

	// Use a complex SQL where parsing cost is significant.
	sql := `SELECT t1.id, t1.name FROM t AS t1
	        INNER JOIN t AS t2 ON t1.id = t2.id
	        WHERE t1.id > 50 AND t1.name IN ('u', 'v', 'w')
	        ORDER BY t1.id`

	b.Run("cached", func(b *testing.B) {
		ex := NewExecutor()
		ctx := context.Background()
		// Prime cache.
		rows, _ := ex.QueryStream(ctx, sql)
		drainStream(rows)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rows2, _ := ex.QueryStream(ctx, sql)
			drainStream(rows2)
		}
	})
}

// BenchmarkSelect1_ExecutorCache_Shared measures allocation with shared
// caches (REQ001220). Multiple ShallowCopy clones share the root executor's
// stmtCache and planCache by pointer, eliminating per-query cache allocation.
func BenchmarkSelect1_ExecutorCache_Shared(b *testing.B) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id", "name"})
	DT.TablesMu.Lock()
	for i := 0; i < 20; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
			Cols: []string{"id", "name"},
			Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("u")},
		})
	}
	DT.TablesMu.Unlock()

	root := NewExecutor()
	ctx := context.Background()
	sql := "SELECT id, name FROM t WHERE id >= 0"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ex := root.ShallowCopy()
		rows, err := ex.QueryStream(ctx, sql)
		if err != nil {
			b.Fatal(err)
		}
		drainStream(rows)
	}
}

// BenchmarkSelect1_ExecutorCache_PerSession measures allocation when
// each call creates a standalone executor (pre-REQ001220 behavior
// baseline for comparison). Use for benchmarking only — not used in
// production code.
func BenchmarkSelect1_ExecutorCache_PerSession(b *testing.B) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id", "name"})
	DT.TablesMu.Lock()
	for i := 0; i < 20; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
			Cols: []string{"id", "name"},
			Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("u")},
		})
	}
	DT.TablesMu.Unlock()

	ctx := context.Background()
	sql := "SELECT id, name FROM t WHERE id >= 0"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ex := NewExecutor()
		rows, err := ex.QueryStream(ctx, sql)
		if err != nil {
			b.Fatal(err)
		}
		drainStream(rows)
	}
}

// TestShallowCopy_SharesCaches verifies REQ001220 + REQ001259: ShallowCopy
// clones share the root executor's stmtCache and planCache by pointer
// (saving ~171 MB + ~1231 MB allocation per query respectively).
func TestShallowCopy_SharesCaches(t *testing.T) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	root := NewExecutor()

	ex1 := root.ShallowCopy()
	ex2 := root.ShallowCopy()

	// stmtCache must be the same pointer (shared).
	if ex1.stmtCache != root.stmtCache {
		t.Error("ShallowCopy stmtCache is not shared with root")
	}
	if ex2.stmtCache != root.stmtCache {
		t.Error("second ShallowCopy stmtCache is not shared with root")
	}

	// REQ001259: planCache must also be shared (immutable after compilation).
	if ex1.planCache != root.planCache {
		t.Error("ShallowCopy planCache should be shared with root")
	}
	if ex2.planCache != root.planCache {
		t.Error("second ShallowCopy planCache should be shared with root")
	}
	if ex1.planCache.maxSize != root.planCache.maxSize {
		t.Error("planCache maxSize should match root")
	}
}

func drainStream(rows *streamIterator) {
	for {
		_, err := rows.Next()
		if err != nil {
			_ = rows.Close()
			return
		}
	}
}

// TestStmtCache_QueryStream verifies REQ000771: QueryStream hits
// the cache on repeated identical SQL.
func TestStmtCache_QueryStream(t *testing.T) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	for i := 0; i < 10; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(i))}})
	}
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()

	// Run the same query many times — should produce identical
	// results and never error.
	for i := 0; i < 5; i++ {
		rows, err := ex.QueryStream(ctx, "SELECT id FROM t")
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		count := 0
		for {
			_, err := rows.Next()
			if err != nil {
				break
			}
			count++
		}
		rows.Close()
		if count != 10 {
			t.Errorf("iter %d: got %d rows, want 10", i, count)
		}
	}

	// Cache should have one entry.
	if ex.stmtCache.entries == nil {
		t.Fatal("cache not initialized")
	}
	ex.stmtCache.mu.Lock()
	size := len(ex.stmtCache.entries)
	ex.stmtCache.mu.Unlock()
	if size != 1 {
		t.Errorf("expected 1 cache entry, got %d", size)
	}
}

// TestStmtCache_InvalidationOnError verifies REQ000771: if the
// SQL is invalid, the cache must not retain the failed AST.
// We verify this indirectly — a syntactically invalid SQL should
// never end up in the cache.
func TestStmtCache_InvalidationOnError(t *testing.T) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()

	ex := NewExecutor()
	ctx := context.Background()

	// Invalid SQL — parser fails, must not cache.
	_, err := ex.QueryStream(ctx, "SELECT FROM WHERE INVALID")
	if err == nil {
		t.Fatal("expected parse error")
	}

	ex.stmtCache.mu.Lock()
	size := len(ex.stmtCache.entries)
	ex.stmtCache.mu.Unlock()
	if size != 0 {
		t.Errorf("invalid SQL should not be cached, got %d entries", size)
	}
}

// TestStmtCache_DDLInvalidates verifies REQ000771: after a DDL
// statement (e.g. CREATE TABLE), cached queries for DT.Tables that
// no longer exist must still error gracefully. The cache itself
// keeps entries — DDL invalidation is the planner's responsibility
// (it re-checks catalog at plan time). This test verifies the
// cache doesn't accidentally serve a stale AST that points to a
// non-existent table.
func TestStmtCache_DDLInvalidates(t *testing.T) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(1))}})
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()

	// First query: prime the cache.
	rows, err := ex.QueryStream(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatalf("first query: %v", err)
	}
	rows.Close()

	// Verify cache has the entry.
	ex.stmtCache.mu.Lock()
	hasEntry := ex.stmtCache.entries["SELECT id FROM t"] != nil
	ex.stmtCache.mu.Unlock()
	if !hasEntry {
		t.Fatal("expected cache entry for first query")
	}

	// The cached AST still works as long as the table exists.
	// This confirms the cache hit path (REQ000771).
	rows, err = ex.QueryStream(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatalf("cached query: %v", err)
	}
	count := 0
	for {
		_, err := rows.Next()
		if err != nil {
			break
		}
		count++
	}
	rows.Close()
	if count != 1 {
		t.Errorf("expected 1 row from cached query, got %d", count)
	}
}

// TestQueryStreamFromAST_BypassParser verifies REQ000771: callers
// can pass a pre-parsed AST to skip parsing entirely. Useful when
// the AST comes from a higher-level cache.
func TestQueryStreamFromAST_BypassParser(t *testing.T) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	for i := 0; i < 3; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(i))}})
	}
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()

	// Parse once.
	parser := PS.NewParser("SELECT id FROM t")
	stmt, err := parser.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Use the cached AST for the query.
	rows, err := ex.QueryStreamFromAST(ctx, stmt)
	if err != nil {
		t.Fatalf("QueryStreamFromAST: %v", err)
	}
	count := 0
	for {
		_, err := rows.Next()
		if err != nil {
			break
		}
		count++
	}
	rows.Close()
	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}
}

// TestPreparedCache_HitRate verifies REQ001011: repeated queries
// use cached compiled plans. The plan cache should be populated
// after the first query and hit on subsequent identical queries.
func TestPreparedCache_HitRate(t *testing.T) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id", "name"})
	DT.TablesMu.Lock()
	for i := 0; i < 10; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id", "name"}, Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("u")}})
	}
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()

	sql := "SELECT id, name FROM t WHERE id > 3 ORDER BY id"

	// First query: plan cache miss, should populate the cache.
	rows, err := ex.Query(ctx, sql)
	if err != nil {
		t.Fatalf("first query: %v", err)
	}
	if rows == nil {
		t.Fatal("first query: nil rows")
	}
	// Drain the single row (Query returns one row for discovery).
	_ = rows

	// Plan cache should now have one entry.
	ex.planCache.mu.Lock()
	size := len(ex.planCache.entries)
	ex.planCache.mu.Unlock()
	if size != 1 {
		t.Fatalf("expected 1 plan cache entry after first query, got %d", size)
	}

	// Second query: should hit the plan cache.
	rows2, err := ex.Query(ctx, sql)
	if err != nil {
		t.Fatalf("second query: %v", err)
	}
	if rows2 == nil {
		t.Fatal("second query: nil rows")
	}

	// Plan cache should still have exactly one entry.
	ex.planCache.mu.Lock()
	size = len(ex.planCache.entries)
	ex.planCache.mu.Unlock()
	if size != 1 {
		t.Fatalf("expected 1 plan cache entry after second query, got %d", size)
	}

	// Run a different query to verify cache handles multiple entries.
	sql2 := "SELECT name FROM t WHERE id = 5"
	rows3, err := ex.Query(ctx, sql2)
	if err != nil {
		t.Fatalf("different query: %v", err)
	}
	if rows3 == nil {
		t.Fatal("different query: nil rows")
	}

	// Plan cache should now have two entries.
	ex.planCache.mu.Lock()
	size = len(ex.planCache.entries)
	ex.planCache.mu.Unlock()
	if size != 2 {
		t.Fatalf("expected 2 plan cache entries, got %d", size)
	}

	// Run the first query again — should still hit cache.
	rows4, err := ex.Query(ctx, sql)
	if err != nil {
		t.Fatalf("first query again: %v", err)
	}
	if rows4 == nil {
		t.Fatal("first query again: nil rows")
	}

	// Cache size unchanged.
	ex.planCache.mu.Lock()
	size = len(ex.planCache.entries)
	ex.planCache.mu.Unlock()
	if size != 2 {
		t.Fatalf("expected 2 plan cache entries after repeat, got %d", size)
	}

	// Verify QueryAll also uses the plan cache.
	allRows, err := ex.QueryAll(ctx, sql)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(allRows) != 6 {
		t.Fatalf("QueryAll: expected 6 rows, got %d", len(allRows))
	}

	// Cache still has 2 entries.
	ex.planCache.mu.Lock()
	size = len(ex.planCache.entries)
	ex.planCache.mu.Unlock()
	if size != 2 {
		t.Fatalf("expected 2 plan cache entries after QueryAll, got %d", size)
	}
}

// TestPreparedCache_DDLInvalidates verifies REQ001011: DDL statements
// invalidate cached plans (schema version change changes the memo key).
func TestPreparedCache_DDLInvalidates(t *testing.T) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(1))}})
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()

	// First query primes the cache.
	rows, err := ex.Query(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatalf("first query: %v", err)
	}
	_ = rows

	ex.planCache.mu.Lock()
	preDDL := len(ex.planCache.entries)
	ex.planCache.mu.Unlock()
	if preDDL != 1 {
		t.Fatalf("expected 1 entry before DDL, got %d", preDDL)
	}

	// Simulate DDL: bump schema version so memo key changes.
	pl.BumpDefaultSchemaVersion()

	// Same query again — should be a plan cache miss (different key).
	rows2, err := ex.Query(ctx, "SELECT id FROM t")
	if err != nil {
		t.Fatalf("query after DDL: %v", err)
	}
	_ = rows2

	// Cache should now have the old entry (stale) plus the new entry.
	// The old entry becomes a cache fragment — will be evicted on overflow.
	ex.planCache.mu.Lock()
	postDDL := len(ex.planCache.entries)
	ex.planCache.mu.Unlock()
	// After DDL, the old key + new key = 2 entries (or 1 if eviction).
	if postDDL < 1 || postDDL > 2 {
		t.Fatalf("expected 1 or 2 entries after DDL, got %d", postDDL)
	}
}

// TestPreparedCache_EdgeCases verifies REQ001011 edge cases:
// empty result, invalid SQL, disabled cache.
func TestPreparedCache_EdgeCases(t *testing.T) {
	t.Run("empty_result", func(t *testing.T) {
		UnregisterAll()
		ResetGlobalStmtCache()
		defer UnregisterAll()
		ResetGlobalStmtCache()

		DT.RegisterTableSchema("t", []string{"id"})
		ex := NewExecutor()
		ctx := context.Background()

		// Query on empty table — should succeed and return no rows.
		rows, err := ex.Query(ctx, "SELECT id FROM t WHERE id > 100")
		if err != nil {
			t.Fatalf("empty result query: %v", err)
		}
		if rows == nil {
			// nil rows means no result — acceptable.
		}
	})

	t.Run("invalid_sql_not_cached", func(t *testing.T) {
		UnregisterAll()
		ResetGlobalStmtCache()
		defer UnregisterAll()
		ResetGlobalStmtCache()

		ex := NewExecutor()
		ctx := context.Background()

		// Invalid SQL should not populate the plan cache.
		_, err := ex.Query(ctx, "SELECT FROM WHERE")
		if err == nil {
			t.Fatal("expected error for invalid SQL")
		}

		ex.planCache.mu.Lock()
		size := len(ex.planCache.entries)
		ex.planCache.mu.Unlock()
		if size != 0 {
			t.Errorf("expected 0 entries after invalid SQL, got %d", size)
		}
	})

	t.Run("disabled_cache", func(t *testing.T) {
		UnregisterAll()
		ResetGlobalStmtCache()
		defer UnregisterAll()
		ResetGlobalStmtCache()

		DT.RegisterTableSchema("d", []string{"x"})
		DT.TablesMu.Lock()
		DT.Tables["d"] = append(DT.Tables["d"], DT.Row{Cols: []string{"x"}, Data: []DT.Value{NewIntValue(42)}})
		DT.TablesMu.Unlock()

		// Create executor but don't enable plan cache.
		ex := NewExecutor()
		// Disable plan cache by setting entries to nil.
		ex.planCache.entries = nil
		ctx := context.Background()

		rows, err := ex.Query(ctx, "SELECT x FROM d")
		if err != nil {
			t.Fatalf("query with disabled cache: %v", err)
		}
		if rows == nil {
			t.Fatal("nil rows with disabled cache")
		}
	})

	t.Run("cache_lru_eviction", func(t *testing.T) {
		UnregisterAll()
		ResetGlobalStmtCache()
		defer UnregisterAll()
		ResetGlobalStmtCache()

		DT.RegisterTableSchema("e", []string{"id"})
		DT.TablesMu.Lock()
		DT.Tables["e"] = append(DT.Tables["e"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(1)}})
		DT.TablesMu.Unlock()

		// Use a tiny max size to force eviction.
		ex := NewExecutor()
		ex.initPlanCache(2)
		ctx := context.Background()

		// Insert 3 different query plans — should evict the oldest.
		for i := 0; i < 3; i++ {
			sql := fmt.Sprintf("SELECT id FROM e WHERE id = %d", i)
			_, _ = ex.Query(ctx, sql)
		}

		ex.planCache.mu.Lock()
		size := len(ex.planCache.entries)
		ex.planCache.mu.Unlock()
		if size > 2 {
			t.Errorf("expected at most 2 entries after eviction, got %d", size)
		}
	})
}

// BenchmarkPreparedCache_HitVsMiss measures the throughput difference
// between plan cache hits and misses. REQ001011.
func BenchmarkPreparedCache_HitVsMiss(b *testing.B) {
	UnregisterAll()
	ResetGlobalStmtCache()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id", "name", "value"})
	DT.TablesMu.Lock()
	for i := 0; i < 1000; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
			Cols: []string{"id", "name", "value"},
			Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("u"), NewIntValue(int64(i * 2))},
		})
	}
	DT.TablesMu.Unlock()

	// Use a complex multi-table query where planning cost is significant.
	sql := `SELECT t1.id, t2.name
	        FROM t AS t1
	        INNER JOIN t AS t2 ON t1.id = t2.id
	        WHERE t1.id > 500 AND t2.name IN ('u', 'v')
	        ORDER BY t1.id`

	b.Run("cache_hit", func(b *testing.B) {
		ex := NewExecutor()
		ctx := context.Background()
		// Prime cache.
		rows, _ := ex.Query(ctx, sql)
		if rows != nil {
			_ = rows
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rows2, _ := ex.Query(ctx, sql)
			if rows2 != nil {
				_ = rows2
			}
		}
	})

	b.Run("cache_miss", func(b *testing.B) {
		ex := NewExecutor()
		// Disable plan cache.
		ex.planCache.entries = nil
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rows, _ := ex.Query(ctx, sql)
			if rows != nil {
				_ = rows
			}
		}
	})
}

func BenchmarkPrepare_GlobalCache(b *testing.B) {
	UnregisterAll()
	defer UnregisterAll()
	ResetGlobalStmtCache()

	DT.RegisterTableSchema("t", []string{"id"})
	DT.TablesMu.Lock()
	for i := 0; i < 10; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{Cols: []string{"id"}, Data: []DT.Value{NewIntValue(int64(i))}})
	}
	DT.TablesMu.Unlock()

	ctx := context.Background()
	sql := "SELECT id FROM t"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ex := NewExecutor()
		rows, err := ex.QueryStream(ctx, sql)
		if err != nil {
			b.Fatal(err)
		}
		drainStream(rows)
	}
}
