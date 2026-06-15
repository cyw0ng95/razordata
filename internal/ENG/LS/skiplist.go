package ls

import (
	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
)

const maxLevel = 12

type node struct {
	key   []byte
	value atomic.Value
	next  [maxLevel]atomic.Pointer[node]
	mu    sync.Mutex
}

type skipList struct {
	head  atomic.Pointer[node]
	level atomic.Int32
	len   atomic.Int64
}

// nodeSlicePool pools the predecessor/successor scratch slices
// used during Insert. Slices are pre-allocated to maxLevel capacity
// so they fit the largest possible insertion without re-allocation.
// REQ000198.
var nodeSlicePool = sync.Pool{
	New: func() any {
		s := make([]*node, maxLevel)
		return &s
	},
}

// acquireSlice fetches a scratch slice from the pool. The returned
// pointer must be returned via releaseSlice after use.
func acquireSlice() *[]*node {
	return nodeSlicePool.Get().(*[]*node)
}

// releaseSlice returns a scratch slice to the pool.
func releaseSlice(s *[]*node) {
	if s == nil {
		return
	}
	// Clear references so GC can reclaim referenced nodes.
	for i := range *s {
		(*s)[i] = nil
	}
	nodeSlicePool.Put(s)
}

func New() *skipList {
	sl := &skipList{}
	sl.level.Store(1)
	head := &node{}
	for i := 0; i < maxLevel; i++ {
		head.next[i].Store(nil)
	}
	sl.head.Store(head)
	return sl
}

func (sl *skipList) randomLevel() int {
	lvl := 1
	for lvl < maxLevel && rand.Intn(2) == 0 {
		lvl++
	}
	return lvl
}

func (sl *skipList) Insert(key, value []byte) {
	lvl := sl.randomLevel()

	// Acquire scratch slices from the pool (REQ000198). Slice
	// capacity is always maxLevel, so no re-allocation regardless
	// of insertion level. The pool returns 0 allocations on
	// steady-state operations.
	predecessorsPtr := acquireSlice()
	successorsPtr := acquireSlice()
	defer releaseSlice(predecessorsPtr)
	defer releaseSlice(successorsPtr)
	predecessors := (*predecessorsPtr)[:lvl]
	successors := (*successorsPtr)[:lvl]

	for {
		head := sl.head.Load()
		currentLevel := sl.level.Load()

		curr := head
		for i := currentLevel - 1; i >= 0; i-- {
			next := curr.next[i].Load()
			for next != nil && bytes.Compare(next.key, key) < 0 {
				curr = next
				next = curr.next[i].Load()
			}
			if int(i) < lvl {
				predecessors[i] = curr
				successors[i] = next
			}
		}

		for i := 0; i < lvl; i++ {
			if predecessors[i] == nil {
				predecessors[i] = head
				successors[i] = head.next[i].Load()
			}
		}

		next := successors[0]
		if next != nil && bytes.Equal(next.key, key) {
			next.mu.Lock()
			next.value.Store(value)
			next.mu.Unlock()
			return
		}

		newNode := &node{key: key, value: atomic.Value{}}
		newNode.value.Store(value)
		for i := 0; i < lvl; i++ {
			newNode.next[i].Store(successors[i])
		}

		inserted := predecessors[0].next[0].CompareAndSwap(next, newNode)
		if !inserted {
			continue
		}

		if lvl > int(currentLevel) {
			sl.level.CompareAndSwap(currentLevel, int32(lvl))
		}

		sl.len.Add(1)
		return
	}
}

func (sl *skipList) Find(key []byte) ([]byte, bool) {
	curr := sl.head.Load()
	for i := int(sl.level.Load()) - 1; i >= 0; i-- {
		next := curr.next[i].Load()
		for next != nil && bytes.Compare(next.key, key) < 0 {
			curr = next
			next = curr.next[i].Load()
		}
	}
	curr = curr.next[0].Load()
	if curr == nil {
		return nil, false
	}
	if bytes.Equal(curr.key, key) {
		curr.mu.Lock()
		val := curr.value.Load().([]byte)
		curr.mu.Unlock()
		return val, true
	}
	return nil, false
}

func (sl *skipList) Len() int64 {
	return sl.len.Load()
}

func (sl *skipList) Iterator() *Iterator {
	return &Iterator{current: nil, list: sl}
}

type Iterator struct {
	current *node
	list    *skipList
}

func (it *Iterator) Next() bool {
	if it.current == nil {
		it.current = it.list.head.Load().next[0].Load()
	} else {
		it.current = it.current.next[0].Load()
	}
	return it.current != nil
}

func (it *Iterator) Key() []byte {
	if it.current == nil {
		return nil
	}
	return it.current.key
}

func (it *Iterator) Value() []byte {
	if it.current == nil {
		return nil
	}
	it.current.mu.Lock()
	val := it.current.value.Load().([]byte)
	it.current.mu.Unlock()
	return val
}
