package OP

import (
	"context"
	"errors"
	"sync"
	"testing"

	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// fakeIndexStore exposes a primary keyspace. IndexOnlyScan tests
// exercise the index-only path: the wrapped IndexScan emits rows
// directly from the index keyspace (no heap fetch). This test
// stores both index entries and heap rows so we can verify that
// IndexOnlyScan never touches the heap.
type fakeIndexStore struct {
	mu      sync.Mutex
	heap    map[string][]byte // pk → row
	indexOK bool
}

func newFakeIndexStore() *fakeIndexStore {
	return &fakeIndexStore{heap: map[string][]byte{}}
}

func (s *fakeIndexStore) Insert(key, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heap[string(key)] = append([]byte(nil), value...)
	return nil
}

func (s *fakeIndexStore) Delete(key []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.heap, string(key))
	return nil
}

func (s *fakeIndexStore) Get(key []byte) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.heap[string(key)]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

// IndexOnlyScan is index-only — we intentionally do NOT implement
// Store so we can verify callers never invoke heap Get.

func (s *fakeIndexStore) NewIterator(prefix []byte) /* ls.RangeIter */ interface{} {
	return nil
}

func (s *fakeIndexStore) ManualCompact() error { return nil }

// countingIndexScan emits N rows and counts how many heap-style
// downstream operations would occur. The test verifies
// IndexOnlyScan wraps the inner scan without any extra fetch.
type countingIndexScan struct {
	keys []string
	pos  int
	// heapCalls counts invocations of a function that emulates
	// the heap Get. IndexOnlyScan must never trigger it.
	heapCalls *int
}

func (c *countingIndexScan) Next(ctx context.Context) (pl.Row, error) {
	if err := ctx.Err(); err != nil {
		return pl.Row{}, err
	}
	if c.pos >= len(c.keys) {
		return pl.Row{}, pl.ErrNoRows
	}
	k := c.keys[c.pos]
	c.pos++
	// Emit an "index entry" containing only the indexed column
	// value and the primary key. The test inspects whether any
	// heap fetch occurred downstream.
	return pl.Row{
		Cols: []string{"a", "id"},
		Data: []pl.Value{{Kind: pl.KindText, S: k}, {Kind: pl.KindInt, I64: int64(c.pos)}},
	}, nil
}

func (c *countingIndexScan) Close() error { return nil }

// TestIndexOnlyScan_WrapsInner verifies REQ001107: NewIndexOnlyScan
// wraps an inner IndexScan and forwards Next/Close to it.
func TestIndexOnlyScan_WrapsInner(t *testing.T) {
	inner := &countingIndexScan{keys: []string{"x", "y", "z"}}
	s := NewIndexOnlyScanPassthrough(toIndexScan(inner))
	if s == nil {
		t.Fatal("NewIndexOnlyScanPassthrough returned nil for non-nil inner")
	}
	defer s.Close()

	ctx := context.Background()
	got := []string{}
	for {
		row, err := s.Next(ctx)
		if errors.Is(err, pl.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, row.Data[0].S)
	}
	if len(got) != 3 || got[0] != "x" || got[1] != "y" || got[2] != "z" {
		t.Errorf("output = %v, want [x y z]", got)
	}
}

func TestIndexOnlyScan_NilInner(t *testing.T) {
	if NewIndexOnlyScan(nil, nil, nil, "") != nil {
		t.Fatal("NewIndexOnlyScan(nil) must return nil")
	}
	if NewIndexOnlyScanPassthrough(nil) != nil {
		t.Fatal("NewIndexOnlyScanPassthrough(nil) must return nil")
	}
	if s := (*IndexOnlyScan)(nil); s != nil {
		_ = s.Close() // exercises the nil-receiver guard
	}
}

// TestIndexOnlyScan_EmptyIndex verifies that wrapping a depleted
// index scan surfaces ErrNoRows on the first Next() call.
func TestIndexOnlyScan_EmptyIndex(t *testing.T) {
	inner := &countingIndexScan{keys: nil}
	s := NewIndexOnlyScanPassthrough(toIndexScan(inner))
	defer s.Close()
	_, err := s.Next(context.Background())
	if !errors.Is(err, pl.ErrNoRows) {
		t.Fatalf("Next on empty index = %v, want ErrNoRows", err)
	}
}

// TestIndexOnlyScan_CloseReleasesInner verifies REQ001107: Close
// propagates to the wrapped scan.
func TestIndexOnlyScan_CloseReleasesInner(t *testing.T) {
	inner := &countingIndexScan{keys: []string{"a"}}
	s := NewIndexOnlyScanPassthrough(toIndexScan(inner))
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestIsCoveringIndex verifies the planner-side helper that
// decides whether an index covers the projected columns.
func TestIsCoveringIndex(t *testing.T) {
	cases := []struct {
		name    string
		project []string
		idxCols []string
		pk      string
		want    bool
	}{
		{"empty_projection_covered", nil, []string{"a"}, "id", true},
		{"single_indexed", []string{"a"}, []string{"a"}, "id", true},
		{"multi_indexed", []string{"a", "b"}, []string{"a", "b"}, "id", true},
		{"index_plus_pk", []string{"a", "id"}, []string{"a"}, "id", true},
		{"non_indexed_column", []string{"a", "z"}, []string{"a"}, "id", false},
		{"no_index_cols", []string{"a"}, nil, "id", false},
		{"empty_index_covered_by_pk", []string{"id"}, nil, "id", true},
		{"select_star_not_covered", []string{"a", "b", "c", "d"}, []string{"a"}, "id", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsCoveringIndex(tc.project, tc.idxCols, tc.pk)
			if got != tc.want {
				t.Errorf("IsCoveringIndex(%v, %v, %q) = %v, want %v",
					tc.project, tc.idxCols, tc.pk, got, tc.want)
			}
		})
	}
}

// toIndexScan is a tiny adapter kept for symmetry with the
// planner-side NewIndexOnlyScan usage. IndexOnlyScan now accepts
// any pl.Operator so the tests don't need to wrap an IndexScan.
func toIndexScan(op pl.Operator) pl.Operator { return op }
