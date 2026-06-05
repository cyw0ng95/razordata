// Package TB is the table-registry cluster. It owns the system
// catalog (a persistent map of tableID to TableSchema, with a
// name → tableID secondary index) and exposes it through a small
// byte-oriented API. The catalog persists to whatever Store the
// caller provides; in production this is the LS engine, in tests it
// is the MapStore defined in this file.
//
// This file (store.go) defines the minimal Store/Iterator surface
// the catalog needs. It is byte-keyed and byte-valued; the catalog
// is agnostic to row layout, indexes, or LSM/SST details.
package tb

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// ErrNotFound is returned by Store.Get when the key is absent or
// has been tombstoned. Callers should use errors.Is(err, ErrNotFound)
// to detect it.
var ErrNotFound = errors.New("tb: key not found")

// Store is the minimal byte-store surface the catalog needs. The
// production implementation is the LS engine (*ls.Engine satisfies
// this interface); tests use MapStore.
type Store interface {
	Insert(key, value []byte) error
	Get(key []byte) ([]byte, error) // returns ErrNotFound for missing
	Delete(key []byte) error
	NewIterator(prefix []byte) Iterator
}

// Iterator is a streaming scan over a key range. The semantics
// match the LS engine's RangeIter: Next returns true while a row
// is available, false on exhaustion or error. Tombstoned values
// are skipped. Close releases any held resources.
type Iterator interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

// MapStore is an in-memory Store backed by a map. It is intended
// for tests; it is goroutine-safe and iterates keys in sorted
// order, which makes catalog tests deterministic.
type MapStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMapStore returns an empty MapStore.
func NewMapStore() *MapStore {
	return &MapStore{data: make(map[string][]byte)}
}

// Insert copies v into the store under key. The catalog treats
// keys as opaque bytes; the copy semantics here are the same as
// the LS engine (callers may mutate the input slice after Insert
// returns).
func (m *MapStore) Insert(key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := append([]byte(nil), value...)
	m.data[string(key)] = cp
	return nil
}

// Get returns a copy of the value stored at key, or ErrNotFound
// if the key is absent. The returned slice is owned by the caller
// and may be mutated.
func (m *MapStore) Get(key []byte) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[string(key)]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

// Delete removes key. Delete on a missing key is a no-op (matches
// the LS engine semantics).
func (m *MapStore) Delete(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, string(key))
	return nil
}

// NewIterator returns a streaming iterator over all keys with the
// given prefix, in sorted byte order. The snapshot is taken at
// iterator construction time; subsequent mutations to the store do
// not affect an in-flight iterator.
func (m *MapStore) NewIterator(prefix []byte) Iterator {
	m.mu.RLock()
	keys := make([][]byte, 0, len(m.data))
	for k := range m.data {
		if bytes.HasPrefix([]byte(k), prefix) {
			keys = append(keys, []byte(k))
		}
	}
	m.mu.RUnlock()
	sort.Slice(keys, func(i, j int) bool {
		return bytes.Compare(keys[i], keys[j]) < 0
	})
	return &mapIter{store: m, keys: keys, prefixLen: len(prefix)}
}

type mapIter struct {
	store     *MapStore
	keys      [][]byte
	pos       int
	prefixLen int
	curKey    []byte
	curVal    []byte
	err       error
	closed    bool
}

func (it *mapIter) Next() bool {
	if it.closed {
		return false
	}
	for it.pos < len(it.keys) {
		k := it.keys[it.pos]
		it.pos++
		v, err := it.store.Get(k)
		if err != nil {
			// Tombstoned between snapshot and read; skip.
			continue
		}
		it.curKey = append([]byte(nil), k...)
		it.curVal = v
		return true
	}
	return false
}

func (it *mapIter) Key() []byte   { return it.curKey }
func (it *mapIter) Value() []byte { return it.curVal }
func (it *mapIter) Err() error    { return it.err }
func (it *mapIter) Close() error  { it.closed = true; return nil }
