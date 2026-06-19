package ls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// IndexStore stores secondary-index entries over the LSM engine (REQ000252).
type IndexStore struct {
	eng     *Engine
	tableID uint64
	name    string
}

// NewIndexStore constructs a wrapper for the given (tableID, name).
func NewIndexStore(eng *Engine, tableID uint64, name string) *IndexStore {
	return &IndexStore{eng: eng, tableID: tableID, name: name}
}

func (s *IndexStore) indexPrefix() []byte {
	prefix := make([]byte, 0, len(indexMagic)+8+len(s.name)+2)
	prefix = append(prefix, indexMagic...)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], s.tableID)
	prefix = append(prefix, buf[:]...)
	prefix = append(prefix, ':')
	prefix = append(prefix, s.name...)
	prefix = append(prefix, ':')
	return prefix
}

func (s *IndexStore) encodeKey(indexValue []byte) []byte {
	prefix := s.indexPrefix()
	out := make([]byte, 0, len(prefix)+len(indexValue))
	out = append(out, prefix...)
	out = append(out, indexValue...)
	return out
}

// Insert adds an index entry mapping indexValue -> primaryKey.
func (s *IndexStore) Insert(indexValue, primaryKey []byte) error {
	return s.eng.Insert(s.encodeKey(indexValue), primaryKey)
}

// Delete removes an index entry.
func (s *IndexStore) Delete(indexValue, primaryKey []byte) error {
	return s.eng.Delete(s.encodeKey(indexValue))
}

// Get returns the primary key for the given index value.
func (s *IndexStore) Get(indexValue []byte) ([]byte, bool, error) {
	v, err := s.eng.Get(s.encodeKey(indexValue))
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return v, true, nil
}

// Seek returns a RangeIter over entries matching the given prefix.
func (s *IndexStore) Seek(indexValuePrefix []byte) RangeIter {
	return s.eng.NewIterator(s.encodeKey(indexValuePrefix))
}

// Range returns a RangeIter over entries in [start, end).
func (s *IndexStore) Range(start, end []byte) (RangeIter, error) {
	if end == nil {
		return s.eng.NewIterator(s.encodeKey(start)), nil
	}
	raw := s.eng.NewIterator(s.indexPrefix())
	return &rangeIndexIter{
		raw:    raw,
		prefix: s.indexPrefix(),
		start:  s.encodeKey(start),
		end:    s.encodeKey(end),
	}, nil
}

func (s *IndexStore) All() RangeIter {
	return s.eng.NewIterator(s.indexPrefix())
}

// Count returns the number of entries in the index.
func (s *IndexStore) Count() (int64, error) {
	it := s.All()
	defer it.Close()
	var n int64
	for it.Next() {
		if it.Err() != nil {
			return n, it.Err()
		}
		n++
	}
	return n, it.Err()
}

type rangeIndexIter struct {
	raw    RangeIter
	prefix []byte
	start  []byte
	end    []byte
}

func (r *rangeIndexIter) Next() bool {
	for r.raw.Next() {
		key := r.raw.Key()
		if r.start != nil && bytes.Compare(key, r.start) < 0 {
			continue
		}
		if r.end != nil && bytes.Compare(key, r.end) >= 0 {
			return false
		}
		return true
	}
	return false
}

func (r *rangeIndexIter) Key() []byte   { return r.raw.Key() }
func (r *rangeIndexIter) Value() []byte { return r.raw.Value() }
func (r *rangeIndexIter) Err() error    { return r.raw.Err() }
func (r *rangeIndexIter) Close() error  { return r.raw.Close() }

// StripPrefix returns the key with the index prefix removed.
func (s *IndexStore) StripPrefix(key []byte) []byte {
	if !bytes.HasPrefix(key, s.indexPrefix()) {
		return key
	}
	return key[len(s.indexPrefix()):]
}

var indexMagic = []byte("__idx__:")

func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func (s *IndexStore) String() string {
	return fmt.Sprintf("IndexStore{table=%d, name=%q}", s.tableID, s.name)
}
