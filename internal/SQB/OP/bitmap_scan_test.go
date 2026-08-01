package OP

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cyw0ng95/razordata/internal/ENG/LS"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// fakeIndexScan is a test-only Operator that emits the supplied
// primary keys in order. Used as input to BitmapHeapScan.
type fakeIndexScan struct {
	keys   []string
	pos    int
	mu     sync.Mutex
	closed bool
}

func (f *fakeIndexScan) Next(ctx context.Context) (PL.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return PL.Row{}, PL.ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return PL.Row{}, err
	}
	if f.pos >= len(f.keys) {
		return PL.Row{}, PL.ErrNoRows
	}
	k := f.keys[f.pos]
	f.pos++
	return PL.Row{
		Cols: []string{"pk"},
		Data: []PL.Value{{Kind: PL.KindText, S: k}},
	}, nil
}

func (f *fakeIndexScan) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

type memStore struct {
	mu sync.Mutex
	kv map[string][]byte
}

func newMemStore(kv map[string][]byte) *memStore {
	if kv == nil {
		kv = map[string][]byte{}
	}
	return &memStore{kv: kv}
}

func (m *memStore) Insert(key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[string(key)] = append([]byte(nil), value...)
	return nil
}

func (m *memStore) Delete(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, string(key))
	return nil
}

func (m *memStore) Get(key []byte) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[string(key)]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (m *memStore) NewIterator(prefix []byte) ls.RangeIter {
	return nil
}

func (m *memStore) ManualCompact() error { return nil }

func TestBitmapHeapScan_Basic(t *testing.T) {
	store := newMemStore(map[string][]byte{
		"a": []byte("alpha"),
		"b": []byte("beta"),
		"c": []byte("gamma"),
	})
	left := &fakeIndexScan{keys: []string{"a", "c"}}
	right := &fakeIndexScan{keys: []string{"b", "c", "a"}}
	bhs := NewBitmapHeapScan("t", store, []DT.Operator{left, right})
	defer bhs.Close()

	ctx := context.Background()
	seen := make(map[string]bool)
	for {
		row, err := bhs.Next(ctx)
		if errors.Is(err, PL.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if len(row.Data) == 0 {
			t.Fatal("row has no data")
		}
		// First column is the heap row value (KindText).
		v := row.Data[0].S
		if seen[v] {
			t.Fatalf("duplicate heap value in bitmap output: %q", v)
		}
		seen[v] = true
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !seen[want] {
			t.Errorf("missing heap value %q in bitmap output", want)
		}
	}
}

func TestBitmapHeapScan_Dedup(t *testing.T) {
	store := newMemStore(map[string][]byte{"a": []byte("x")})
	a := &fakeIndexScan{keys: []string{"a", "a", "a"}}
	b := &fakeIndexScan{keys: []string{"a"}}
	bhs := NewBitmapHeapScan("t", store, []DT.Operator{a, b})
	defer bhs.Close()
	count := 0
	for {
		_, err := bhs.Next(context.Background())
		if errors.Is(err, PL.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	if count != 1 {
		t.Errorf("dedup count = %d, want 1", count)
	}
}

func TestBitmapHeapScan_SortedOutput(t *testing.T) {
	store := newMemStore(map[string][]byte{
		"a": []byte("1"), "c": []byte("3"), "b": []byte("2"),
	})
	// Children emit in non-sorted order; bitmap must still produce
	// rows sorted by primary key (the heap order).
	left := &fakeIndexScan{keys: []string{"c"}}
	right := &fakeIndexScan{keys: []string{"a", "b"}}
	bhs := NewBitmapHeapScan("t", store, []DT.Operator{left, right})
	defer bhs.Close()
	var got []string
	for {
		row, err := bhs.Next(context.Background())
		if errors.Is(err, PL.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, row.Data[0].S)
	}
	want := []string{"1", "2", "3"}
	for i, v := range want {
		if i >= len(got) || got[i] != v {
			t.Fatalf("output = %v, want %v", got, want)
		}
	}
}

func TestBitmapHeapScan_NoChildren(t *testing.T) {
	store := newMemStore(nil)
	bhs := NewBitmapHeapScan("t", store, nil)
	defer bhs.Close()
	_, err := bhs.Next(context.Background())
	if !errors.Is(err, PL.ErrNoRows) {
		t.Fatalf("Next with no children = %v, want ErrNoRows", err)
	}
}

func TestBitmapHeapScan_MissingKey(t *testing.T) {
	// Index scan returns a key that has no row in the store; the
	// bitmap operator should skip the missing key rather than fail.
	store := newMemStore(map[string][]byte{"a": []byte("alpha")})
	idx := &fakeIndexScan{keys: []string{"a", "missing", "missing"}}
	bhs := NewBitmapHeapScan("t", store, []DT.Operator{idx})
	defer bhs.Close()
	count := 0
	for {
		_, err := bhs.Next(context.Background())
		if errors.Is(err, PL.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		count++
	}
	if count != 1 {
		t.Errorf("missing-key count = %d, want 1", count)
	}
}

func TestBitmapHeapScan_Close(t *testing.T) {
	store := newMemStore(map[string][]byte{"a": []byte("x")})
	idx := &fakeIndexScan{keys: []string{"a"}}
	bhs := NewBitmapHeapScan("t", store, []DT.Operator{idx})
	if err := bhs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !idx.closed {
		t.Error("child IndexScan should be closed by BitmapHeapScan.Close")
	}
}
