package wr

import (
	"path/filepath"
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
