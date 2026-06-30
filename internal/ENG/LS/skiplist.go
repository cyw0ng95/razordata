package ls

import (
	"bytes"
	"sync"
	"sync/atomic"
	"time"
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
	// REQ001132: lock-free xorshift64 PRNG replaces the previous
	// sl.rmu + sl.rng combination. Every insert used to acquire
	// sl.rmu for randomLevel(), serializing all concurrent inserts.
	// The xorshift64 state lives in an atomic.Uint64 and is updated
	// via CAS — no lock needed.
	rng atomic.Uint64
}

var nodeSlicePool = sync.Pool{
	New: func() any {
		s := make([]*node, maxLevel)
		return &s
	},
}

func acquireSlice() *[]*node { return nodeSlicePool.Get().(*[]*node) }

func releaseSlice(s *[]*node) {
	if s == nil {
		return
	}
	for i := range *s {
		(*s)[i] = nil
	}
	nodeSlicePool.Put(s)
}

func New() *skipList {
	sl := &skipList{}
	sl.level.Store(1)
	head := &node{}
	for i := range maxLevel {
		head.next[i].Store(nil)
	}
	sl.head.Store(head)
	seed := uint64(time.Now().UnixNano())
	if seed == 0 {
		seed = 1
	}
	sl.rng.Store(seed)
	return sl
}

// REQ001132: lock-free randomLevel using xorshift64.
// The original implementation held sl.rmu for every call,
// serializing all concurrent inserts. This version uses a
// CAS loop on an atomic.Uint64 xorshift64 state — no lock.
func (sl *skipList) randomLevel() int {
	for {
		old := sl.rng.Load()
		// xorshift64: three shifts, three xors
		x := old
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		if x == 0 {
			x = 1
		}
		if sl.rng.CompareAndSwap(old, x) {
			lvl := 1
			for lvl < maxLevel && (x&1) == 0 {
				lvl++
				// Use successive bits for the coin flip,
				// matching the original IntN(2) == 0 distribution.
				x >>= 1
			}
			return lvl
		}
		// CAS lost — another goroutine updated the RNG state.
		// Retry with the new value.
	}
}

func (sl *skipList) Insert(key, value []byte) error {
	lvl := sl.randomLevel()
	predecessorsPtr := acquireSlice()
	successorsPtr := acquireSlice()
	defer releaseSlice(predecessorsPtr)
	defer releaseSlice(successorsPtr)
	predecessors := (*predecessorsPtr)[:lvl]
	successors := (*successorsPtr)[:lvl]
	const maxOuterRetries = 100
	for outerRetries := 0; outerRetries < maxOuterRetries; outerRetries++ {
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
		for i := range lvl {
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
			return nil
		}
		newNode := &node{key: key, value: atomic.Value{}}
		newNode.value.Store(value)
		for i := range lvl {
			newNode.next[i].Store(successors[i])
		}
		inserted := predecessors[0].next[0].CompareAndSwap(next, newNode)
		if !inserted {
			continue
		}
		linkedHigher := true
		for i := 1; i < lvl; i++ {
			const maxRetries = 8
			retries := 0
			levelLinked := false
			for retries < maxRetries {
				pred := predecessors[i]
				succ := successors[i]
				if pred.next[i].CompareAndSwap(succ, newNode) {
					levelLinked = true
					break
				}
				retries++
				pred, succ = findPredSucc(head, currentLevel, i, key)
				predecessors[i] = pred
				successors[i] = succ
			}
			if !levelLinked {
				linkedHigher = false
				break
			}
		}
		_ = linkedHigher
		if lvl > int(currentLevel) {
			sl.level.CompareAndSwap(currentLevel, int32(lvl))
		}
		sl.len.Add(1)
		return nil
	}
	return ErrRetryExceeded
}

func findPredSucc(head *node, level int32, i int, key []byte) (*node, *node) {
	c := head
	n := c.next[i].Load()
	for n != nil && bytes.Compare(n.key, key) < 0 {
		c = n
		n = c.next[i].Load()
	}
	return c, n
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
		val := curr.value.Load().([]byte)
		return val, true
	}
	return nil, false
}

func (sl *skipList) Len() int64 { return sl.len.Load() }

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
	return it.current.value.Load().([]byte)
}
