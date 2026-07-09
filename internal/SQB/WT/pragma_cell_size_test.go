package WT

import (
	"context"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// mockCellVerifier implements both DT.Store and cellSizeVerifier.
type mockCellVerifier struct {
	errors []string
}

func (m *mockCellVerifier) Insert(key, value []byte) error { return nil }
func (m *mockCellVerifier) Delete(key []byte) error        { return nil }
func (m *mockCellVerifier) Get(key []byte) ([]byte, bool, error) {
	return nil, false, nil
}
func (m *mockCellVerifier) NewIterator(prefix []byte) ls.RangeIter {
	return &emptyRangeIter{}
}
func (m *mockCellVerifier) ManualCompact() error { return nil }
func (m *mockCellVerifier) VerifyCellSizes() []string {
	return m.errors
}

type emptyRangeIter struct{}

func (e *emptyRangeIter) Next() bool    { return false }
func (e *emptyRangeIter) Key() []byte   { return nil }
func (e *emptyRangeIter) Value() []byte { return nil }
func (e *emptyRangeIter) Err() error    { return nil }
func (e *emptyRangeIter) Close() error  { return nil }

func TestPragma_CellSizeCheck_Ok(t *testing.T) {
	p := NewPragma(&PS.PragmaStmt{Name: "cell_size_check"})
	p.store = &mockCellVerifier{errors: nil}

	ctx := context.Background()
	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(row.Data) < 1 || row.Data[0].S != "ok" {
		t.Errorf("got %v, want 'ok'", row.Data[0])
	}
}

func TestPragma_CellSizeCheck_ReportsErrors(t *testing.T) {
	p := NewPragma(&PS.PragmaStmt{Name: "cell_size_check"})
	p.store = &mockCellVerifier{errors: []string{"block 0: size mismatch"}}

	ctx := context.Background()
	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "block 0: size mismatch" {
		t.Errorf("got %q, want 'block 0: size mismatch'", row.Data[0].S)
	}
}

func TestPragma_CellSizeCheck_NoStore(t *testing.T) {
	p := NewPragma(&PS.PragmaStmt{Name: "cell_size_check"})
	// p.store is nil

	ctx := context.Background()
	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "ok" {
		t.Errorf("got %q, want 'ok' (no store = skip)", row.Data[0].S)
	}
}

func TestPragma_CellSizeCheck_StoreWithoutInterface(t *testing.T) {
	p := NewPragma(&PS.PragmaStmt{Name: "cell_size_check"})
	p.store = nil

	ctx := context.Background()
	row, err := p.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if row.Data[0].S != "ok" {
		t.Errorf("got %q, want 'ok' (no interface = ok)", row.Data[0].S)
	}
}