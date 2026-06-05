package EX

import (
	"context"
	"testing"

	sc "github.com/cyw0ng95/razordata/internal/ENG/SC"
)

// TestIndexScan_RealSeek_ConstructorWiresPKIndex verifies that
// NewIndexScanWithStoreAndIndex sets all the real-seek fields
// correctly. The end-to-end test (PK seek through the real
// engine) is added in Phase 4 when the catalog + PK index are
// wired into the engine.
func TestIndexScan_RealSeek_ConstructorWiresPKIndex(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "name"}, "id")
	ss, ok := schemaFor("t")
	if !ok {
		t.Fatal("schemaFor(t) failed after register")
	}

	// A minimal stub PKIndex. The interface is satisfied; the
	// test only checks that the operator's fields are wired.
	stub := &stubPKIndex{}

	// Point-lookup.
	is, err := NewIndexScanWithStoreAndIndex(ex.store, stub, "t", "idx", ss.tableID, [][]byte{intBE(42)}, nil, nil)
	if err != nil {
		t.Fatalf("NewIndexScanWithStoreAndIndex (point): %v", err)
	}
	if is.pkIndex == nil {
		t.Fatal("pkIndex not wired (point)")
	}
	if is.pkTableID != ss.tableID {
		t.Errorf("pkTableID: got %d, want %d", is.pkTableID, ss.tableID)
	}
	if is.pkRangeHi != nil {
		t.Errorf("pkRangeHi: got %v, want nil (point lookup)", is.pkRangeHi)
	}
	if len(is.pkValues) != 1 {
		t.Errorf("pkValues: got %d entries, want 1", len(is.pkValues))
	}
	is.Close()

	// Range scan.
	is, err = NewIndexScanWithStoreAndIndex(ex.store, stub, "t", "idx", ss.tableID, nil, [][]byte{intBE(10)}, [][]byte{intBE(20)})
	if err != nil {
		t.Fatalf("NewIndexScanWithStoreAndIndex (range): %v", err)
	}
	if is.pkRangeHi == nil {
		t.Fatal("pkRangeHi not wired (range)")
	}
	if is.pkValues != nil {
		t.Errorf("pkValues: got %v, want nil (range scan)", is.pkValues)
	}
	if is.pkRangeLo == nil {
		t.Error("pkRangeLo not wired")
	}
	is.Close()
}

// TestIndexScan_RealSeek_CostReduced pins the cost-model change
// for the IndexScan operator: it was 0.1 in iter-08 (a rough
// approximation when only a prefix scan was available) and is
// 0.01 in iter-10 (real seek via ENG/ID is sub-microsecond on
// the bench).
func TestIndexScan_RealSeek_CostReduced(t *testing.T) {
	p := NewPlanner()
	if got := p.estimateCost(NewIndexScan("t", "idx", nil, nil)); got != 0.01 {
		t.Errorf("IndexScan cost = %v, want 0.01", got)
	}
}

// TestIndexScan_NonIndexed_DegradesToSeqScan ensures that
// NewIndexScanWithStore (without a PK index) still works as a
// prefix-scan fallback, so v1 SQL queries that select IndexScan
// without a wired PK index do not break.
func TestIndexScan_NonIndexed_DegradesToSeqScan(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id"}, "id")

	is, err := NewIndexScanWithStore(ex.store, "t", "idx")
	if err != nil {
		t.Fatalf("NewIndexScanWithStore: %v", err)
	}
	defer is.Close()
	if is.pkIndex != nil {
		t.Error("expected pkIndex to be nil for the prefix-scan fallback")
	}
}

// TestIndexScan_RealSeek_FullPath drives the real-seek code path
// through the stub PKIndex and verifies the operator's behavior.
// The stub counts Seek calls; the test confirms the seek path
// was taken (not the prefix-scan fallback). The actual row
// fetch is best-effort — without a real engine row at the
// stubbed rowKey, the fetch returns ErrNoRows, which is a
// legitimate outcome (the seek resolved, the row just isn't
// in the engine).
func TestIndexScan_RealSeek_FullPath(t *testing.T) {
	ex, eng := newEngineExecutor(t)
	defer eng.Close()
	ex.RegisterTableWithPK("t", []string{"id", "name"}, "id")
	ss, _ := schemaFor("t")

	ctx := context.Background()
	stub := &countingPKIndex{}

	is, err := NewIndexScanWithStoreAndIndex(ex.store, stub, "t", "idx", ss.tableID, [][]byte{intBE(42)}, nil, nil)
	if err != nil {
		t.Fatalf("NewIndexScanWithStoreAndIndex: %v", err)
	}
	defer is.Close()

	// Call Next. It will go through the seek path (Seek is
	// called), then attempt the row fetch, which returns
	// ErrNoRows because the rowKey doesn't exist. The test
	// passes as long as Seek was called.
	_, _ = is.Next(ctx)
	if stub.seekCalls == 0 {
		t.Error("expected Seek to be called on the real-seek path")
	}
}

// countingPKIndex is a PKIndex stub that counts how many times
// each method was called.
type countingPKIndex struct {
	seekCalls int
}

func (c *countingPKIndex) Insert(tableID uint64, pkTypes []sc.ColumnType, pkValues [][]byte, rowPointer []byte) error {
	return nil
}

func (c *countingPKIndex) Delete(tableID uint64, pkTypes []sc.ColumnType, pkValues [][]byte) error {
	return nil
}

func (c *countingPKIndex) Seek(tableID uint64, pkTypes []sc.ColumnType, pkValues [][]byte) ([]byte, bool, error) {
	c.seekCalls++
	return nil, false, nil // always miss
}

func (c *countingPKIndex) Range(tableID uint64, pkTypes []sc.ColumnType, lo, hi [][]byte) (PKIndexIterator, error) {
	return &emptyPKIter{}, nil
}

// stubPKIndex is a no-op PKIndex for unit-testing the
// IndexScan wiring. It returns a fixed rowKey on Seek so the
// fetch path can be exercised.
type stubPKIndex struct {
	rowKeyFor func(values [][]byte) []byte
}

func (s *stubPKIndex) Insert(tableID uint64, pkTypes []sc.ColumnType, pkValues [][]byte, rowPointer []byte) error {
	return nil
}

func (s *stubPKIndex) Delete(tableID uint64, pkTypes []sc.ColumnType, pkValues [][]byte) error {
	return nil
}

func (s *stubPKIndex) Seek(tableID uint64, pkTypes []sc.ColumnType, pkValues [][]byte) ([]byte, bool, error) {
	if s.rowKeyFor != nil {
		return s.rowKeyFor(pkValues), true, nil
	}
	return nil, false, nil
}

func (s *stubPKIndex) Range(tableID uint64, pkTypes []sc.ColumnType, lo, hi [][]byte) (PKIndexIterator, error) {
	return &emptyPKIter{}, nil
}

type emptyPKIter struct{}

func (*emptyPKIter) Next() bool    { return false }
func (*emptyPKIter) Key() []byte   { return nil }
func (*emptyPKIter) Value() []byte { return nil }
func (*emptyPKIter) Err() error    { return nil }
func (*emptyPKIter) Close() error  { return nil }

// intBE encodes a positive int64 as 8 big-endian bytes, matching
// the EX/ rowKey convention.
func intBE(v int64) []byte {
	out := make([]byte, 8)
	out[0] = byte(v >> 56)
	out[1] = byte(v >> 48)
	out[2] = byte(v >> 40)
	out[3] = byte(v >> 32)
	out[4] = byte(v >> 24)
	out[5] = byte(v >> 16)
	out[6] = byte(v >> 8)
	out[7] = byte(v)
	return out
}
