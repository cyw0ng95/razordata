package ls

import (
	"crypto/rand"
	"fmt"
	"testing"
)

// benchValue is the value payload used by all skiplist/SST
// benchmarks. Pre-allocated once per package so the
// benchmarks themselves are not bottlenecked by value
// generation.
var benchValue = []byte("v")

// makeBenchKeys produces a deterministic-ish key set of n
// 16-byte keys. We use crypto/rand to spread the keys
// across the skiplist's level distribution rather than
// always-ascending sequences, which would degenerate the
// skiplist's randomLevel() draw.
func makeBenchKeys(n, keyLen int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		k := make([]byte, keyLen)
		// Mix the index into the prefix so all keys differ.
		// Random bytes fill the tail so the distribution is
		// closer to the production workload (random keys)
		// than a sequential iteration would produce.
		k[0] = byte(i >> 8)
		k[1] = byte(i)
		if _, err := rand.Read(k[2:]); err != nil {
			panic(fmt.Sprintf("bench rand: %v", err))
		}
		out[i] = k
	}
	return out
}

// BenchmarkSkiplistInsert measures raw insert throughput
// on a single goroutine. Each iteration inserts the next key
// modulo the bench set; the bench set is generated once
// outside the timer.
// Workload (R17-1): the write path is the dominant cost
// for memtable ingest; insert ns/op is the regression
// signal we want to expose.
func BenchmarkSkiplistInsert(b *testing.B) {
	sl := New()
	keys := makeBenchKeys(10_000, 16)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sl.Insert(keys[i%len(keys)], benchValue)
	}
}

// BenchmarkSkiplistIterator measures the per-element cost
// of the full-table iterator. We build the skiplist first
// (outside the timer), then drive the iterator to completion
// per iteration.
// Workload (R17-3): the iterator path is the hot path for
// memtable scan and the prefix-scan fallback for IndexScan
// in SQL/EX; a regression in Next/Key/Value would surface
// here.
func BenchmarkSkiplistIterator(b *testing.B) {
	sl := New()
	keys := makeBenchKeys(10_000, 16)
	for _, k := range keys {
		sl.Insert(k, benchValue)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		it := sl.Iterator()
		for it.Next() {
			_ = it.Key()
			_ = it.Value()
		}
	}
}

// BenchmarkSkiplistMixed measures the realistic workload of
// insert + range-iterate interleaved, simulating the
// memtable accepting writes while a SeqScan drains it.
func BenchmarkSkiplistMixed(b *testing.B) {
	sl := New()
	keys := makeBenchKeys(10_000, 16)
	// Pre-populate half.
	for i := 0; i < len(keys)/2; i++ {
		sl.Insert(keys[i], benchValue)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Insert one, iterate the rest.
		sl.Insert(keys[(i+len(keys)/2)%len(keys)], benchValue)
		it := sl.Iterator()
		count := 0
		for it.Next() {
			_ = it.Key()
			count++
		}
	}
}

// BenchmarkSSTWriterAddFinish measures the SST write path:
// add N keys, finish the block, return the encoded bytes.
// This is the work the flushManager does on every flush.
// Workload (R17-4): flush latency is dominated by SST
// encoding; the writer's allocation profile is a strong
// signal for memory pressure during heavy write load.
func BenchmarkSSTWriterAddFinish(b *testing.B) {
	keys := makeBenchKeys(10_000, 16)
	values := make([][]byte, len(keys))
	for i := range values {
		values[i] = []byte(fmt.Sprintf("v%d", i))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := newSSTWriter()
		for j, k := range keys {
			w.Add(k, values[j])
		}
		_, _ = w.Finish()
	}
}

// BenchmarkSSTReaderOpenAndIterate measures the SST read
// path: open an encoded SST (from outside the timer), then
// per-iteration open + iterate. Reports the cost of the
// per-read parse setup plus the per-element walk.
// Workload (R17-5): the reader is hit on every compaction
// input read, every SeqScan over an SST, and every
// nextFileID() scan during manifest recovery.
func BenchmarkSSTReaderOpenAndIterate(b *testing.B) {
	keys := makeBenchKeys(10_000, 16)
	values := make([][]byte, len(keys))
	for i := range values {
		values[i] = []byte(fmt.Sprintf("v%d", i))
	}
	w := newSSTWriter()
	for j, k := range keys {
		w.Add(k, values[j])
	}
	data, err := w.Finish()
	if err != nil {
		b.Fatalf("setup: w.Finish: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, err := openSST(data)
		if err != nil {
			b.Fatalf("openSST: %v", err)
		}
		it := r.Iterator()
		for it.Next() {
			_ = it.Key()
			_ = it.Value()
		}
		_ = r.Close()
	}
}

// BenchmarkSSTReader_ReadBlock measures the block-batched iteration path
// (REQ001995). Consumes the entire SST block-by-block instead of pair-by-pair.
// Comparable to BenchmarkSSTReaderOpenAndIterate to quantify the
// per-row iterator overhead.
func BenchmarkSSTReader_ReadBlock(b *testing.B) {
	keys := makeBenchKeys(10_000, 16)
	values := make([][]byte, len(keys))
	for i := range values {
		values[i] = []byte(fmt.Sprintf("v%d", i))
	}
	w := newSSTWriter()
	for j, k := range keys {
		w.Add(k, values[j])
	}
	data, err := w.Finish()
	if err != nil {
		b.Fatalf("setup: w.Finish: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, err := openSST(data)
		if err != nil {
			b.Fatalf("openSST: %v", err)
		}
		it := r.Iterator()
		for {
			_, values, ok := it.ReadBlock()
			if !ok {
				break
			}
			_ = values
		}
		_ = r.Close()
	}
}

// BenchmarkFlushMemtableToSST measures the end-to-end flush
// path: build a memtable with N keys, drive requestFlush +
// WaitForFlush. This is what a real writer hits when
// the memtable hits its size threshold.
// Workload (R17-6): flush latency dominates the write
// path under sustained load; ns/op here is the per-flush
// cost that the user-visible INSERT throughput budget
// must absorb.
// The benchmark uses a tempdir-managed manifest so
// updateManifest's atomic-rename write happens off the
// critical path; this matches production behavior.
func BenchmarkFlushMemtableToSST(b *testing.B) {
	tmp := b.TempDir()
	manifest, err := newManifest(tmp)
	if err != nil {
		b.Fatalf("newManifest: %v", err)
	}
	defer func() {
		_ = manifest.Close()
	}()
	fm := newFlushManager(DefaultFS(), tmp, 1<<20, manifest)
	defer func() {
		_ = fm.Close()
	}()

	// Pre-generate the keys/values once so the bench measures
	// flush, not key generation.
	keys := makeBenchKeys(1_000, 16)
	values := make([][]byte, len(keys))
	for i := range values {
		values[i] = []byte(fmt.Sprintf("v%d", i))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mt := newMemtable(1024 * 1024)
		for j, k := range keys {
			mt.Insert(k, values[j])
		}
		fm.requestFlush(mt)
		fm.WaitForFlush()
	}
}

// BenchmarkSelect1_MergeIterator measures the mergeIterator
// read path on a small in-memory memtable (the dominant case
// for select1 with no flushed data). REQ001257 + REQ001258.
//
// Each iteration creates a fresh mergeIterator via the pool
// and scans all rows. The pool + ring-buffer optimizations
// should keep allocs/op near zero (excluding the skiplist
// iterator returned by memtable.Iterator()).
func BenchmarkSelect1_MergeIterator(b *testing.B) {
	mt := newMemtable(1 << 20)
	for i := 0; i < 20; i++ {
		key := []byte(fmt.Sprintf("key%02d", i))
		val := []byte(fmt.Sprintf("val%02d", i))
		mt.Insert(key, val)
	}
	dir := b.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		b.Fatalf("newManifest: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mi := newMergeIterator([]*memtable{mt}, m, dir, DefaultFS(), nil, nil, 0, nil)
		for mi.Next() {
		}
		_ = mi.Close()
	}
}

// BenchmarkSelect1_HeapBufferReuse measures the per-Push
// allocation cost of the iterHeap. REQ001258.
//
// Each iteration pushes a single item (the minimum) so the
// bench is sensitive to the per-push alloc cost.
func BenchmarkSelect1_HeapBufferReuse(b *testing.B) {
	mt := newMemtable(1 << 20)
	mt.Insert([]byte("a"), []byte("1"))
	mt.Insert([]byte("b"), []byte("2"))
	mt.Insert([]byte("c"), []byte("3"))
	mt.Insert([]byte("d"), []byte("4"))
	mt.Insert([]byte("e"), []byte("5"))

	dir := b.TempDir()
	m, err := newManifest(dir)
	if err != nil {
		b.Fatalf("newManifest: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mi := newMergeIterator([]*memtable{mt}, m, dir, DefaultFS(), nil, nil, 0, nil)
		for mi.Next() {
		}
		_ = mi.Close()
	}
}

