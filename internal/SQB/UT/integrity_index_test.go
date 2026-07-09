package UT

import (
	"context"
	"testing"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
)

// Compile-time interface checks.
var _ DT.Store = (*mockIndexVerifier)(nil)
var _ indexRefVerifier = (*mockIndexVerifier)(nil)

type mockIndexVerifier struct {
	errors []string
}

func (m *mockIndexVerifier) Insert(key, value []byte) error { return nil }
func (m *mockIndexVerifier) Delete(key []byte) error        { return nil }
func (m *mockIndexVerifier) Get(key []byte) ([]byte, bool, error) {
	return nil, false, nil
}
func (m *mockIndexVerifier) NewIterator(prefix []byte) ls.RangeIter {
	return &emptyIter{}
}
func (m *mockIndexVerifier) ManualCompact() error { return nil }
func (m *mockIndexVerifier) Close() error         { return nil }
func (m *mockIndexVerifier) VerifyIndexReferences() []string {
	return m.errors
}

type emptyIter struct{}

func (e *emptyIter) Next() bool    { return false }
func (e *emptyIter) Key() []byte   { return nil }
func (e *emptyIter) Value() []byte { return nil }
func (e *emptyIter) Err() error    { return nil }
func (e *emptyIter) Close() error  { return nil }

func TestIntegrityCheck_IndexReference_Passes(t *testing.T) {
	ic := NewIntegrityCheckWithStore(&mockIndexVerifier{errors: nil})
	defer ic.Close()

	ctx := context.Background()
	var rows []Row
	for {
		row, err := ic.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 0 {
		t.Errorf("got %d error rows, want 0 (clean)", len(rows))
	}
}

func TestIntegrityCheck_IndexReference_ReportsErrors(t *testing.T) {
	m := &mockIndexVerifier{
		errors: []string{"index: table t1 index i1 entry references missing row key=abc"},
	}
	ic := NewIntegrityCheckWithStore(m)
	if _, ok := ic.store.(indexRefVerifier); !ok {
		t.Fatal("store does not implement indexRefVerifier")
	}
	// Verify the mock returns the expected error
	verifier := ic.store.(indexRefVerifier)
	if got := verifier.VerifyIndexReferences(); len(got) != 1 {
		t.Fatalf("VerifyIndexReferences returned %d errors, want 1", len(got))
	}
	defer ic.Close()

	ctx := context.Background()
	var rows []Row
	for {
		row, err := ic.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d error rows, want 1", len(rows))
	}
	if rows[0].Data[2].S != "index: table t1 index i1 entry references missing row key=abc" {
		t.Errorf("unexpected error message: %s", rows[0].Data[2].S)
	}
}

func TestIntegrityCheck_IndexReference_MultipleErrors(t *testing.T) {
	ic := NewIntegrityCheckWithStore(&mockIndexVerifier{
		errors: []string{
			"index: table t1 index i1 entry references missing row key=abc",
			"index: table t2 index i2 entry references missing row key=def",
		},
	})
	defer ic.Close()

	ctx := context.Background()
	var rows []Row
	for {
		row, err := ic.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d error rows, want 2", len(rows))
	}
}

func TestIntegrityCheck_IndexReference_NoStore(t *testing.T) {
	ic := NewIntegrityCheck()
	defer ic.Close()

	ctx := context.Background()
	var rows []Row
	for {
		row, err := ic.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 0 {
		t.Errorf("got %d error rows, want 0 (no store = no index check)", len(rows))
	}
}

func TestIntegrityCheck_IndexReference_StoreWithoutInterface(t *testing.T) {
	ic := NewIntegrityCheckWithStore(nil)
	defer ic.Close()

	ctx := context.Background()
	var rows []Row
	for {
		row, err := ic.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 0 {
		t.Errorf("got %d error rows, want 0", len(rows))
	}
}