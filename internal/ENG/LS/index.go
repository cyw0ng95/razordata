package ls

import (
	"bytes"
	"sync"
	"sync/atomic"
)

type pkEntry struct {
	key     []byte
	primary []byte
	next    atomic.Pointer[pkEntry]
}

type pkIterator struct {
	current *pkEntry
	head    *pkEntry
}

func (it *pkIterator) Next() bool {
	if it.current == nil {
		it.current = it.head.next.Load()
	} else {
		it.current = it.current.next.Load()
	}
	return it.current != nil
}

func (it *pkIterator) Key() []byte {
	if it.current == nil {
		return nil
	}
	return it.current.key
}

func (it *pkIterator) Primary() []byte {
	if it.current == nil {
		return nil
	}
	return it.current.primary
}

func (it *pkIterator) Seek(key []byte) bool {
	return false
}

type primaryIndex struct {
	head    atomic.Pointer[pkEntry]
	len     atomic.Int64
	mu      sync.RWMutex
	tableID uint64
	indexID uint64
}

func newPrimaryIndex(tableID, indexID uint64) *primaryIndex {
	idx := &primaryIndex{
		tableID: tableID,
		indexID: indexID,
	}
	idx.head.Store(&pkEntry{})
	return idx
}

func (idx *primaryIndex) Insert(key, primary []byte) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	head := idx.head.Load()
	// REQ000604: walk the list looking for an existing entry
	// with the same key. If found, atomically update its primary
	// pointer rather than appending a duplicate. The previous
	// implementation always appended, causing Find to return the
	// stale entry (oldest first) and Len() to inflate.
	curr := head
	for {
		next := curr.next.Load()
		if next == nil {
			break
		}
		if bytes.Equal(next.key, key) {
			next.primary = primary
			return
		}
		curr = next
	}

	newEntry := &pkEntry{
		key:     key,
		primary: primary,
	}
	newEntry.next.Store(nil)
	if head.next.CompareAndSwap(nil, newEntry) {
		idx.len.Add(1)
		return
	}
	// Concurrent insert raced us; fall through to walk-and-retry.
	for {
		curr := head
		for {
			next := curr.next.Load()
			if next == nil {
				newEntry.next.Store(nil)
				if curr.next.CompareAndSwap(nil, newEntry) {
					idx.len.Add(1)
					return
				}
				continue
			}
			if bytes.Equal(next.key, key) {
				next.primary = primary
				return
			}
			curr = next
		}
	}
}

func (idx *primaryIndex) Find(key []byte) ([]byte, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	curr := idx.head.Load()
	for {
		next := curr.next.Load()
		if next == nil {
			return nil, false
		}
		curr = next
		if string(curr.key) == string(key) {
			return curr.primary, true
		}
	}
}

func (idx *primaryIndex) Delete(key []byte) bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	prev := idx.head.Load()
	curr := prev.next.Load()

	for curr != nil {
		if string(curr.key) == string(key) {
			prev.next.Store(curr.next.Load())
			idx.len.Add(-1)
			return true
		}
		prev = curr
		curr = curr.next.Load()
	}

	return false
}

func (idx *primaryIndex) Len() int64 {
	return idx.len.Load()
}

func (idx *primaryIndex) Iterator() *pkIterator {
	return &pkIterator{current: nil, head: idx.head.Load()}
}
