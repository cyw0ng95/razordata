package wr

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/FIL/LF"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"github.com/cyw0ng95/razordata/internal/MEM/SP"
)

// BenchmarkSequentialAppend measures throughput for sequential WAL writes.
// Run with: go test -bench=BenchmarkSequentialAppend -benchmem -count=3
func BenchmarkSequentialAppend(b *testing.B) {
	dir := b.TempDir()

	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		b.Fatalf("lf.New: %v", err)
	}
	defer sm.Close()

	sp := sp.New()
	log := lg.New(lg.Options{Output: &nullWriter{}})

	w, err := New(dir, sm, sp, log, false)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer w.Close()

	// Pre-build batch to avoid allocation in measurement loop
	batch := &WriteBatch{
		TxnID: 1,
		Recs: []LogRecord{
			{Type: RTData, BlockID: 1, Value: make([]byte, 100)},
			{Type: RTCommit, TxnID: 1},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		batch.TxnID = uint64(i)
		if _, err := w.Append(batch); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}

	w.Sync()
}

// BenchmarkSequentialAppendWithSync measures throughput including sync.
// This reflects actual commit latency.
func BenchmarkSequentialAppendWithSync(b *testing.B) {
	dir := b.TempDir()

	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		b.Fatalf("lf.New: %v", err)
	}
	defer sm.Close()

	sp := sp.New()
	log := lg.New(lg.Options{Output: &nullWriter{}})

	w, err := New(dir, sm, sp, log, false)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer w.Close()

	batch := &WriteBatch{
		TxnID: 1,
		Recs: []LogRecord{
			{Type: RTData, BlockID: 1, Value: make([]byte, 100)},
			{Type: RTCommit, TxnID: 1},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		batch.TxnID = uint64(i)
		if _, err := w.Append(batch); err != nil {
			b.Fatalf("Append: %v", err)
		}
		if err := w.Sync(); err != nil {
			b.Fatalf("Sync: %v", err)
		}
	}
}

// BenchmarkBatchAppend measures throughput when batching multiple records.
func BenchmarkBatchAppend(b *testing.B) {
	dir := b.TempDir()

	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		b.Fatalf("lf.New: %v", err)
	}
	defer sm.Close()

	sp := sp.New()
	log := lg.New(lg.Options{Output: &nullWriter{}})

	w, err := New(dir, sm, sp, log, false)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer w.Close()

	// Pre-build batch with 10 records
	const batchSize = 10
	recs := make([]LogRecord, batchSize)
	for i := 0; i < batchSize; i++ {
		recs[i] = LogRecord{Type: RTData, BlockID: uint64(i), Value: make([]byte, 50)}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		batch := &WriteBatch{
			TxnID: uint64(i),
			Recs:  recs,
		}
		if _, err := w.Append(batch); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}

	w.Sync()
}

// BenchmarkWAL_BulkInsert measures per-row insert+sync throughput for each
// WALMode. On a typical SSD, FSYNC_HEADER_ONLY should be >10x faster than
// FSYNC_EVERY because it skips the fsync barrier on every Sync call.
// Run with: go test -bench=BenchmarkWAL_BulkInsert -benchmem -count=3
func BenchmarkWAL_BulkInsert(b *testing.B) {
	for _, mode := range []WALMode{FSYNC_EVERY, FSYNC_HEADER_ONLY, FSYNC_BATCH} {
		b.Run(mode.String(), func(b *testing.B) {
			dir := b.TempDir()

			sm, err := lf.New(filepath.Join(dir, "wal"))
			if err != nil {
				b.Fatalf("lf.New: %v", err)
			}
			defer sm.Close()

			sp := sp.New()
			log := lg.New(lg.Options{Output: &nullWriter{}})

			w, err := NewWithOptions(dir, sm, sp, log, false, Options{Mode: mode})
			if err != nil {
				b.Fatalf("NewWithOptions: %v", err)
			}
			defer w.Close()

			batch := &WriteBatch{
				TxnID: 1,
				Recs: []LogRecord{
					{Type: RTData, BlockID: 1, Value: make([]byte, 100)},
					{Type: RTCommit, TxnID: 1},
				},
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				batch.TxnID = uint64(i + 1)
				if _, err := w.Append(batch); err != nil {
					b.Fatalf("Append: %v", err)
				}
				if err := w.Sync(); err != nil {
					b.Fatalf("Sync: %v", err)
				}
			}
		})
	}
}

// BenchmarkSegmentRotation measures overhead of segment rotation.
func BenchmarkSegmentRotation(b *testing.B) {
	dir := b.TempDir()

	sm, err := lf.New(filepath.Join(dir, "wal"))
	if err != nil {
		b.Fatalf("lf.New: %v", err)
	}
	defer sm.Close()

	sp := sp.New()
	log := lg.New(lg.Options{Output: &nullWriter{}})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		w, err := New(filepath.Join(dir, "wal"), sm, sp, log, false)
		if err != nil {
			b.Fatalf("New: %v", err)
		}

		// Write enough to trigger rotation
		for j := 0; j < 100; j++ {
			w.Append(&WriteBatch{
				TxnID: uint64(j),
				Recs: []LogRecord{
					{Type: RTData, BlockID: uint64(j), Value: make([]byte, 100)},
				},
			})
		}
		w.Sync()
		w.Close()
	}
}

// BenchmarkWAL_Append_Concurrent measures concurrent WAL append throughput
// under N goroutines. REQ001136 expects the producer-consumer pattern to
// scale beyond the old mutex-based serialization, especially as encoding
// happens outside the serial path. The old design serialized ALL appends
// on a single mutex (wr.go:194). The new design: encode (CPU) is parallel
// across goroutines, only the channel send+wait is serialized.
func BenchmarkWAL_Append_Concurrent(b *testing.B) {
	for _, n := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(formatGoroutineCount(n), func(b *testing.B) {
			dir := b.TempDir()

			sm, err := lf.New(filepath.Join(dir, "wal"))
			if err != nil {
				b.Fatalf("lf.New: %v", err)
			}
			defer sm.Close()

			sp := sp.New()
			log := lg.New(lg.Options{Output: &nullWriter{}})

			w, err := New(dir, sm, sp, log, false)
			if err != nil {
				b.Fatalf("New: %v", err)
			}
			defer w.Close()

			batch := &WriteBatch{
				TxnID: 1,
				Recs: []LogRecord{
					{Type: RTData, BlockID: 1, Value: make([]byte, 100)},
					{Type: RTCommit, TxnID: 1},
				},
			}

			b.ResetTimer()
			b.ReportAllocs()

			var wg sync.WaitGroup
			work := b.N
			perG := (work + n - 1) / n

			for g := 0; g < n; g++ {
				wg.Add(1)
				go func(base int) {
					defer wg.Done()
					for i := 0; i < perG; i++ {
						batch.TxnID = uint64(base + i)
						if _, err := w.Append(batch); err != nil {
							b.Errorf("Append: %v", err)
							return
						}
					}
				}(g * perG)
			}
			wg.Wait()

			w.Sync()
		})
	}
}

func formatGoroutineCount(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s + "g"
}
