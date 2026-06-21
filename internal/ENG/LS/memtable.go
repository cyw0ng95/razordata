package ls

import (
	"sync/atomic"
)

type memtable struct {
	skiplist *skipList
	size     atomic.Int64
	maxSize  int64
	frozen   atomic.Bool
	refs     atomic.Int64
}

func newMemtable(maxSize int64) *memtable {
	return &memtable{
		skiplist: New(),
		maxSize:  maxSize,
	}
}

func (m *memtable) Insert(key, value []byte) error {
	if err := m.skiplist.Insert(key, value); err != nil {
		return err
	}
	m.size.Add(int64(len(key) + len(value)))
	return nil
}

func (m *memtable) Get(key []byte) ([]byte, bool) {
	return m.skiplist.Find(key)
}

func (m *memtable) Iterator() *Iterator {
	return m.skiplist.Iterator()
}

func (m *memtable) Size() int64 {
	return m.size.Load()
}

func (m *memtable) Len() int64 {
	return m.skiplist.Len()
}

func (m *memtable) ShouldFlush() bool {
	return m.size.Load() >= m.maxSize
}

func (m *memtable) Freeze() {
	m.frozen.Store(true)
}

func (m *memtable) IsFrozen() bool {
	return m.frozen.Load()
}

func (m *memtable) IncRef() {
	m.refs.Add(1)
}

func (m *memtable) DecRef() {
	m.refs.Add(-1)
}

func (m *memtable) RefCount() int64 {
	return m.refs.Load()
}

func encodeVarint(val int64) []byte {
	var buf [10]byte
	uv := uint64(val)
	n := 0
	for {
		buf[n] = byte(uv & 0x7f)
		uv >>= 7
		if uv == 0 {
			break
		}
		buf[n] |= 0x80
		n++
		if n >= len(buf) {
			return buf[:n]
		}
	}
	return buf[:n+1]
}

func decodeVarint(data []byte) (int64, int) {
	var val int64
	var shift uint
	for i, b := range data {
		if b < 0x80 {
			return val | int64(b)<<shift, i + 1
		}
		val |= int64(b&0x7f) << shift
		shift += 7
	}
	return val, len(data)
}
