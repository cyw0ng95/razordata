package ls

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// IndexStore is a thin wrapper over the LSM Engine that stores
// secondary-index entries. The wrapper provides key encoding
// (so index entries don't collide with table data) and a seek
// helper that returns a stream of primary keys matching a given
// index value or range.
//
// Key format: "__idx__:" + tableID(u64, big-endian) + ":" +
// indexName + ":" + indexValue
//
// The leading "__idx__:" prefix guarantees namespace separation
// from regular table data (which uses the primary-key prefix).
// REQ000252 — secondary indexes MVP.
type IndexStore struct {
	eng    *Engine
	tableID uint64
	name    string
}

// NewIndexStore constructs a wrapper for the given (tableID, name).
// The engine is shared with the table; the wrapper only owns the
// key encoding.
func NewIndexStore(eng *Engine, tableID uint64, name string) *IndexStore {
	return &IndexStore{eng: eng, tableID: tableID, name: name}
}

// indexPrefix is "__idx__:" + tableID + ":" + name + ":"
// Used as the prefix for all operations on this index.
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

// encodeKey builds the full key for an index entry.
func (s *IndexStore) encodeKey(indexValue []byte) []byte {
	prefix := s.indexPrefix()
	out := make([]byte, 0, len(prefix)+len(indexValue))
	out = append(out, prefix...)
	out = append(out, indexValue...)
	return out
}

// Insert adds an index entry mapping indexValue -> primaryKey.
// Both are stored as opaque byte slices.
func (s *IndexStore) Insert(indexValue, primaryKey []byte) error {
	return s.eng.Insert(s.encodeKey(indexValue), primaryKey)
}

// Delete removes an index entry. The secondary-index LSM path
// stores a tombstone (single key); the (indexValue, primaryKey)
// pair is checked at the application layer for race-safety.
func (s *IndexStore) Delete(indexValue, primaryKey []byte) error {
	return s.eng.Delete(s.encodeKey(indexValue))
}

// Get returns the primary key for the given index value, or
// (nil, false, nil) if no entry exists.
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

// Seek returns a RangeIter over all index entries whose key
// starts with the given indexValue prefix. Used for both point
// lookups (exact key) and range scans.
func (s *IndexStore) Seek(indexValuePrefix []byte) RangeIter {
	return s.eng.NewIterator(s.encodeKey(indexValuePrefix))
}

// Range returns a RangeIter over all index entries in the given
// [start, end) half-open range. If end is nil, the range is
// open-ended (everything >= start).
func (s *IndexStore) Range(start, end []byte) (RangeIter, error) {
	if end == nil {
		return s.eng.NewIterator(s.encodeKey(start)), nil
	}
	// Strategy: use a wide prefix scan and filter via rangeIndexIter.
	// The prefix is the index prefix only, so we get all entries
	// sorted by index value. The rangeIndexIter skips entries
	// outside [start, end).
	raw := s.eng.NewIterator(s.indexPrefix())
	return &rangeIndexIter{
		raw:    raw,
		prefix: s.indexPrefix(),
		start:  s.encodeKey(start),
		end:    s.encodeKey(end),
	}, nil
}

// All returns a RangeIter over all entries in this index.
func (s *IndexStore) All() RangeIter {
	return s.eng.NewIterator(s.indexPrefix())
}

// Count returns the number of entries in the index by walking
// the iterator. Used for statistics (RECOMMENDED for ANALYZE
// in a future iteration).
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

// rangeIndexIter wraps a raw RangeIter and skips entries outside
// the [start, end) range.
type rangeIndexIter struct {
	raw    RangeIter
	prefix []byte
	start  []byte
	end    []byte
}

// Next advances the iterator. Returns false when the underlying
// iterator is exhausted OR the current key has exited [start, end).
func (r *rangeIndexIter) Next() bool {
	for r.raw.Next() {
		key := r.raw.Key()
		// Skip entries before start
		if r.start != nil && bytes.Compare(key, r.start) < 0 {
			continue
		}
		// Stop when key >= end
		if r.end != nil && bytes.Compare(key, r.end) >= 0 {
			return false
		}
		return true
	}
	return false
}

func (r *rangeIndexIter) Key() []byte { return r.raw.Key() }
func (r *rangeIndexIter) Value() []byte { return r.raw.Value() }
func (r *rangeIndexIter) Err() error { return r.raw.Err() }
func (r *rangeIndexIter) Close() error { return r.raw.Close() }

// StripPrefix returns the key with the index prefix removed.
// Convenience for callers that only care about the index value.
func (s *IndexStore) StripPrefix(key []byte) []byte {
	if !bytes.HasPrefix(key, s.indexPrefix()) {
		return key
	}
	return key[len(s.indexPrefix()):]
}

// indexMagic is the namespace prefix for all index entries.
// Underscored to be visually distinct from regular table keys
// and to avoid collisions with user-defined identifiers.
var indexMagic = []byte("__idx__:")

// isNotFound reports whether err is the engine's "not found"
// sentinel. We compare by string suffix to avoid an import cycle.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "not found" ||
		msg == "ls: not found" ||
		strings.HasSuffix(msg, "not found") ||
		strings.HasSuffix(msg, "key not found")
}

// String returns a debug representation of the index.
func (s *IndexStore) String() string {
	return fmt.Sprintf("IndexStore{table=%d, name=%q}", s.tableID, s.name)
}
