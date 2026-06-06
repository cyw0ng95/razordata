package SY

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// BenchmarkEngineInsert measures the throughput of a single-row
// INSERT through a SYS.Session.Exec call. The benchmark uses a
// per-iteration fresh database to avoid state accumulation skewing
// the numbers.
func BenchmarkEngineInsert(b *testing.B) {
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(b.TempDir(), "db")
		eng, err := Open(context.Background(), dir, AP.Options{
			PageSize:     4096,
			MemTableSize: 1024 * 1024,
			BufferPoolMB: 64,
			WALSizeMB:    16,
			MaxLevel:     3,
			LogLevel:     8,
		})
		if err != nil {
			b.Fatal(err)
		}
		s, _ := eng.Begin(context.Background())
		_, _ = s.Exec(context.Background(), "CREATE TABLE t (id INTEGER, val TEXT, PRIMARY KEY (id))")
		b.ResetTimer()
		for j := 0; j < b.N; j++ {
			_, err := s.Exec(context.Background(), fmt.Sprintf("INSERT INTO t VALUES (%d, 'x')", j))
			if err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		_ = eng.Close(context.Background())
	}
}

// BenchmarkEngineSelect measures the throughput of a SELECT through
// a SYS.Session.Query call against a pre-populated table.
func BenchmarkEngineSelect(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "db")
	eng, err := Open(context.Background(), dir, AP.Options{
		PageSize:     4096,
		MemTableSize: 1024 * 1024,
		BufferPoolMB: 64,
		WALSizeMB:    16,
		MaxLevel:     3,
		LogLevel:     8,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close(context.Background())
	s, _ := eng.Begin(context.Background())
	_, _ = s.Exec(context.Background(), "CREATE TABLE t (id INTEGER, val TEXT, PRIMARY KEY (id))")
	for j := 0; j < 100; j++ {
		_, _ = s.Exec(context.Background(), fmt.Sprintf("INSERT INTO t VALUES (%d, 'x')", j))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.Query(context.Background(), "SELECT id, val FROM t")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEngineClose measures the cost of the 6-phase graceful
// shutdown sequence on a fresh engine with no workload. This is
// the lower-bound number — the production cost includes the time
// to drain pending memtable + WAL + force-abort any hung txs.
//
// Run with `go test -bench=BenchmarkEngineClose -run=^$ ./internal/SYS/SY/`.
func BenchmarkEngineClose(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := filepath.Join(b.TempDir(), "db")
		eng, err := Open(context.Background(), dir, AP.Options{
			PageSize:     4096,
			MemTableSize: 1024 * 1024,
			BufferPoolMB: 64,
			WALSizeMB:    16,
			MaxLevel:     3,
			LogLevel:     8,
		})
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := eng.Close(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEngineClose_After100Inserts measures the cost of
// graceful shutdown on an engine that has 100 committed inserts.
// The flush phase is expected to dominate (memtable -> SST ->
// WAL fsync -> directory fsync).
func BenchmarkEngineClose_After100Inserts(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := filepath.Join(b.TempDir(), "db")
		eng, err := Open(context.Background(), dir, AP.Options{
			PageSize:     4096,
			MemTableSize: 1024 * 1024,
			BufferPoolMB: 64,
			WALSizeMB:    16,
			MaxLevel:     3,
			LogLevel:     8,
		})
		if err != nil {
			b.Fatal(err)
		}
		s, _ := eng.Begin(context.Background())
		_, _ = s.Exec(context.Background(), "CREATE TABLE t (id INTEGER, val TEXT, PRIMARY KEY (id))")
		for j := 0; j < 100; j++ {
			_, _ = s.Exec(context.Background(), fmt.Sprintf("INSERT INTO t VALUES (%d, 'x')", j))
		}
		b.StartTimer()
		if err := eng.Close(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
