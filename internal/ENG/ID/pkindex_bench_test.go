package id

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/SC"
	"github.com/cyw0ng95/razordata/internal/ENG/TB"
)

// newBenchStore returns a fresh tb.MapStore wrapped to satisfy
// the ID cluster's Store interface.
func newBenchStore() Store {
	return &tbStoreAdapter{ms: tb.NewMapStore()}
}

// tbStoreAdapter bridges tb.MapStore to the ID.Store interface.
// It is a thin shim so the ID package can keep its Store
// interface independent of tb's package.
type tbStoreAdapter struct {
	ms *tb.MapStore
}

func (a *tbStoreAdapter) Insert(k, v []byte) error     { return a.ms.Insert(k, v) }
func (a *tbStoreAdapter) Get(k []byte) ([]byte, error) { return a.ms.Get(k) }
func (a *tbStoreAdapter) Delete(k []byte) error        { return a.ms.Delete(k) }
func (a *tbStoreAdapter) NewIterator(prefix []byte) tb.Iterator {
	return a.ms.NewIterator(prefix)
}

func benchIntBytes(v int64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(v))
	return out
}

// BenchmarkPKIndex_Insert inserts 10k keys. Setup is excluded
// from the timed region.
func BenchmarkPKIndex_Insert(b *testing.B) {
	const indexSize = 10000
	idx := NewPKIndex(emptyStore())
	types := []sc.ColumnType{sc.CTInt}
	for i := int64(0); i < indexSize; i++ {
		_ = idx.Insert(1, types, [][]byte{benchIntBytes(i)}, []byte(fmt.Sprintf("row:%d", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v := int64(i % indexSize)
		_ = idx.Insert(1, types, [][]byte{benchIntBytes(v)}, []byte(fmt.Sprintf("row:%d", v)))
	}
}

// BenchmarkPKIndex_Seek performs 1k point-lookups against a 10k
// key index. Each lookup is a Store.Get (the underlying store
// is a MapStore, so the cost is map-lookup dominated).
func BenchmarkPKIndex_Seek(b *testing.B) {
	const indexSize = 10000
	idx := NewPKIndex(emptyStore())
	types := []sc.ColumnType{sc.CTInt}
	for i := int64(0); i < indexSize; i++ {
		_ = idx.Insert(1, types, [][]byte{benchIntBytes(i)}, []byte(fmt.Sprintf("row:%d", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v := int64(i % indexSize)
		_, _, _ = idx.Seek(1, types, [][]byte{benchIntBytes(v)})
	}
}

// BenchmarkPKIndex_Range performs 1k range scans of 100 keys
// each against a 10k key index.
func BenchmarkPKIndex_Range(b *testing.B) {
	const indexSize = 10000
	idx := NewPKIndex(emptyStore())
	types := []sc.ColumnType{sc.CTInt}
	for i := int64(0); i < indexSize; i++ {
		_ = idx.Insert(1, types, [][]byte{benchIntBytes(i)}, []byte(fmt.Sprintf("row:%d", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v := int64(i % (indexSize - 100))
		it, err := idx.Range(1, types, [][]byte{benchIntBytes(v)}, [][]byte{benchIntBytes(v + 100)})
		if err != nil {
			b.Fatalf("Range: %v", err)
		}
		for it.Next() {
		}
		it.Close()
	}
}

// BenchmarkPKIndex_Delete deletes 1k keys from a 10k key index.
func BenchmarkPKIndex_Delete(b *testing.B) {
	const indexSize = 10000
	idx := NewPKIndex(emptyStore())
	types := []sc.ColumnType{sc.CTInt}
	for i := int64(0); i < indexSize; i++ {
		_ = idx.Insert(1, types, [][]byte{benchIntBytes(i)}, []byte(fmt.Sprintf("row:%d", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v := int64(i % indexSize)
		_ = idx.Delete(1, types, [][]byte{benchIntBytes(v)})
		// Re-insert so the index stays the same size for the next iteration.
		_ = idx.Insert(1, types, [][]byte{benchIntBytes(v)}, []byte(fmt.Sprintf("row:%d", v)))
	}
}

// emptyStore returns a fresh in-memory MapStore wrapped as the
// ID cluster's Store interface. Using a closure pattern keeps
// the bench file in the same package as the production code
// without exporting MapStore through ID.
func emptyStore() Store {
	return newBenchStore()
}
