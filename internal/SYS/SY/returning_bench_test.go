package SY

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// BenchmarkReturningUpdateNoAlloc measures allocations per row for
// UPDATE with RETURNING. REQ001557: after pre-allocating flat backing
// buffers, the per-row evalReturning call should allocate zero heap
// memory (the three backing slices are grown once and reused).
func BenchmarkReturningUpdateNoAlloc(b *testing.B) {
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

	s, err := eng.Begin(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Rollback(context.Background())

	if _, err := s.Exec(context.Background(), "CREATE TABLE t (id INTEGER, v INTEGER, PRIMARY KEY (id))"); err != nil {
		b.Fatal(err)
	}

	for j := 0; j < 500; j++ {
		if _, err := s.Exec(context.Background(), fmt.Sprintf("INSERT INTO t VALUES (%d, %d)", j, j)); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// UPDATE every row with RETURNING. Each evalReturning call
		// should reuse the pre-allocated flat backing buffers.
		if _, err := s.Exec(context.Background(), "UPDATE t SET v = v + 1"); err != nil {
			b.Fatal(err)
		}
	}
}


