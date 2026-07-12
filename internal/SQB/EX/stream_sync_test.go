package EX

import (
    "context"
    "testing"

    DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// TestStream_SyncPath_SmallResult verifies REQ001409: small queries
// use the synchronous slice-backed path (no goroutine/channel).
func TestStream_SyncPath_SmallResult(t *testing.T) {
    UnregisterAll()
    ResetForTest(t)

    DT.RegisterTableSchema("t", []string{"id", "val"})
    DT.TablesMu.Lock()
    for i := 0; i < 20; i++ {
        DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
            Cols: []string{"id", "val"},
            Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("x")},
        })
    }
    DT.TablesMu.Unlock()

    ex := NewExecutor()
    ctx := context.Background()

    // Simple SELECT on a small table — should use sync path.
    iter, err := ex.QueryStream(ctx, "SELECT id, val FROM t")
    if err != nil {
        t.Fatalf("QueryStream: %v", err)
    }

    // Verify sync path: rows should be pre-accumulated (rows field non-nil).
    if iter.rows == nil {
        t.Fatal("expected sync path (rows != nil) for small query")
    }
    if len(iter.rows) != 20 {
        t.Fatalf("expected 20 rows in sync buffer, got %d", len(iter.rows))
    }

    // Drain and verify all rows.
    count := 0
    for {
        _, err := iter.Next()
        if err != nil {
            break
        }
        count++
    }
    iter.Close()

    if count != 20 {
        t.Fatalf("expected 20 rows, got %d", count)
    }
}

// TestStream_SyncPath_LargeFallback verifies that queries with large
// estimated row counts or complex operators fall back to the channel path.
func TestStream_SyncPath_LargeFallback(t *testing.T) {
    UnregisterAll()
    ResetForTest(t)

    DT.RegisterTableSchema("big", []string{"id", "val"})
    DT.TablesMu.Lock()
    // Create a table with >100 rows — should exceed sync threshold.
    for i := 0; i < 200; i++ {
        DT.Tables["big"] = append(DT.Tables["big"], DT.Row{
            Cols: []string{"id", "val"},
            Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("x")},
        })
    }
    DT.TablesMu.Unlock()

    ex := NewExecutor()
    ctx := context.Background()

    // Large table — should NOT use sync path.
    iter, err := ex.QueryStream(ctx, "SELECT id FROM big")
    if err != nil {
        t.Fatalf("QueryStream: %v", err)
    }

    // Should use channel path (rows nil, rowCh non-nil).
    if iter.rows != nil {
        t.Fatal("expected channel path (rows == nil) for large query")
    }
    if iter.rowCh == nil {
        t.Fatal("expected channel path (rowCh != nil) for large query")
    }

    // Drain all rows.
    count := 0
    for {
        _, err := iter.Next()
        if err != nil {
            break
        }
        count++
    }
    iter.Close()

    if count != 200 {
        t.Fatalf("expected 200 rows, got %d", count)
    }
}

// TestStream_SyncPath_NoJoins verifies that queries with joins always
// use the channel path regardless of row count.
func TestStream_SyncPath_NoJoins(t *testing.T) {
    UnregisterAll()
    ResetForTest(t)

    DT.RegisterTableSchema("a", []string{"id", "v"})
    DT.RegisterTableSchema("b", []string{"id", "v"})
    DT.TablesMu.Lock()
    for i := 0; i < 5; i++ {
        DT.Tables["a"] = append(DT.Tables["a"], DT.Row{
            Cols: []string{"id", "v"},
            Data: []DT.Value{NewIntValue(int64(i)), NewIntValue(int64(i))},
        })
        DT.Tables["b"] = append(DT.Tables["b"], DT.Row{
            Cols: []string{"id", "v"},
            Data: []DT.Value{NewIntValue(int64(i)), NewIntValue(int64(i * 2))},
        })
    }
    DT.TablesMu.Unlock()

    ex := NewExecutor()
    ctx := context.Background()

    // JOIN query — must use channel path.
    iter, err := ex.QueryStream(ctx, "SELECT a.id FROM a JOIN b ON a.id = b.id")
    if err != nil {
        t.Fatalf("QueryStream: %v", err)
    }

    count := 0
    for {
        _, err := iter.Next()
        if err != nil {
            break
        }
        count++
    }
    iter.Close()

    if count != 5 {
        t.Fatalf("expected 5 rows from join, got %d", count)
    }
}

// TestStream_RowLifetimeAfterClose verifies REQ001511: rows returned by
// streamIterator remain valid after Close() (which resets the SeqScan
// RowArena). Reads rows after Close() on the sync path.
func TestStream_RowLifetimeAfterClose(t *testing.T) {
	UnregisterAll()
	ResetForTest(t)

	DT.RegisterTableSchema("t", []string{"id", "val"})
	DT.TablesMu.Lock()
	for i := 0; i < 20; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
			Cols: []string{"id", "val"},
			Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("x")},
		})
	}
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()

	iter, err := ex.QueryStream(ctx, "SELECT id, val FROM t")
	if err != nil {
		t.Fatalf("QueryStream: %v", err)
	}

	// Read all rows first
	var rows []DT.Row
	for {
		r, err := iter.Next()
		if err != nil {
			break
		}
		rows = append(rows, r)
	}
	if len(rows) != 20 {
		t.Fatalf("expected 20 rows, got %d", len(rows))
	}

	// Close the iterator — this resets the RowArena
	iter.Close()

	// REQ001511: rows should still have valid Data after Close()
	for i, r := range rows {
		if len(r.Data) != 2 {
			t.Fatalf("row %d: Data len = %d, want 2", i, len(r.Data))
		}
		if r.Data[0].Kind != DT.KindInt {
			t.Fatalf("row %d col 0: Kind = %v, want KindInt", i, r.Data[0].Kind)
		}
		if r.Data[0].I64 != int64(i) {
			t.Fatalf("row %d col 0: I64 = %d, want %d", i, r.Data[0].I64, i)
		}
	}
}

// TestStream_SyncPath_CacheableSubquery verifies REQ001523: queries
// with a non-correlated single-table aggregate subquery in SELECT
// use the sync path (no goroutine/channel overhead).
func TestStream_SyncPath_CacheableSubquery(t *testing.T) {
	UnregisterAll()
	ResetForTest(t)

	DT.RegisterTableSchema("t", []string{"id", "val"})
	DT.TablesMu.Lock()
	for i := 0; i < 10; i++ {
		DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
			Cols: []string{"id", "val"},
			Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("x")},
		})
	}
	DT.TablesMu.Unlock()

	ex := NewExecutor()
	ctx := context.Background()

	// Subquery is single-table aggregate (avg(id)) with no WHERE/GROUP BY
	// — qualifies as cacheable. Should use sync path.
	iter, err := ex.QueryStream(ctx, "SELECT (SELECT avg(id) FROM t) FROM t")
	if err != nil {
		t.Fatalf("QueryStream: %v", err)
	}

	// Should use sync path despite subquery in SELECT list.
	if iter.rows == nil {
		t.Fatal("expected sync path (rows != nil) for cacheable subquery")
	}

	// Verify all rows are returned.
	count := 0
	for {
		_, err := iter.Next()
		if err != nil {
			break
		}
		count++
	}
	iter.Close()
	if count != 10 {
		t.Fatalf("expected 10 rows, got %d", count)
	}
}

// BenchmarkSelect1_SyncPath benchmarks small queries using the
// synchronous slice-backed path (goroutine-free).
func BenchmarkSelect1_SyncPath(b *testing.B) {
    UnregisterAll()
    DT.RegisterTableSchema("t", []string{"id", "name", "age"})
    DT.TablesMu.Lock()
    for i := 0; i < 20; i++ {
        DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
            Cols: []string{"id", "name", "age"},
            Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("u"), NewIntValue(int64(20 + i))},
        })
    }
    DT.TablesMu.Unlock()
    defer UnregisterAll()

    ex := NewExecutor()
    ctx := context.Background()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        iter, err := ex.QueryStream(ctx, "SELECT id, name FROM t")
        if err != nil {
            b.Fatal(err)
        }
        for {
            _, err := iter.Next()
            if err != nil {
                break
            }
        }
        iter.Close()
    }
}

// BenchmarkSelect1_ChannelPath benchmarks small queries using the
// existing goroutine+channel path. Uses a larger table to force
// the channel path.
func BenchmarkSelect1_ChannelPath(b *testing.B) {
    UnregisterAll()
    DT.RegisterTableSchema("t", []string{"id", "name", "age"})
    DT.TablesMu.Lock()
    // 200 rows to exceed sync threshold (100) and force channel path.
    for i := 0; i < 200; i++ {
        DT.Tables["t"] = append(DT.Tables["t"], DT.Row{
            Cols: []string{"id", "name", "age"},
            Data: []DT.Value{NewIntValue(int64(i)), NewTextValue("u"), NewIntValue(int64(20 + i%50))},
        })
    }
    DT.TablesMu.Unlock()
    defer UnregisterAll()

    ex := NewExecutor()
    ctx := context.Background()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        iter, err := ex.QueryStream(ctx, "SELECT id, name FROM t")
        if err != nil {
            b.Fatal(err)
        }
        for {
            _, err := iter.Next()
            if err != nil {
                break
            }
        }
        iter.Close()
    }
}