package EX

import (
	"bytes"
	"sort"
	"strings"
	"sync"

	ls "github.com/cyw0ng95/razordata/internal/ENG/LS"
)

type memStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{m: make(map[string][]byte)}
}

func (s *memStore) Insert(k, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[string(k)] = append([]byte(nil), v...)
	return nil
}

func (s *memStore) Delete(k []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, string(k))
	return nil
}

func (s *memStore) Get(k []byte) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[string(k)]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *memStore) NewIterator(prefix []byte) ls.RangeIter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pfx := string(prefix)
	var entries []struct{ key, val []byte }
	for k, v := range s.m {
		if strings.HasPrefix(k, pfx) {
			entries = append(entries, struct{ key, val []byte }{[]byte(k), v})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].key, entries[j].key) < 0
	})
	return &memIter{data: entries, idx: -1}
}

func (s *memStore) ManualCompact() error { return nil }

type memIter struct {
	data []struct{ key, val []byte }
	idx  int
}

func (it *memIter) Next() bool {
	it.idx++
	return it.idx < len(it.data)
}

func (it *memIter) Key() []byte {
	if it.idx < 0 || it.idx >= len(it.data) {
		return nil
	}
	return it.data[it.idx].key
}

func (it *memIter) Value() []byte {
	if it.idx < 0 || it.idx >= len(it.data) {
		return nil
	}
	return it.data[it.idx].val
}

func (it *memIter) Err() error { return nil }

func (it *memIter) Close() error { return nil }

var _ ls.RangeIter = (*memIter)(nil)
