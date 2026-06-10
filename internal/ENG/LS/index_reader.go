package ls

// IndexReader is a cursor over a secondary index. It reads
// primary keys in index-value order from the underlying LSM.
// REQ000252 — secondary indexes MVP.
type IndexReader struct {
	store   *IndexStore
	iter    RangeIter
	current []byte // current primary key (clone of last Value())
}

// NewIndexReader returns a new reader bound to the given store.
func NewIndexReader(store *IndexStore) *IndexReader {
	return &IndexReader{store: store}
}

// SeekTo positions the reader at the first entry whose
// indexValue is >= the given value. Returns false if no such
// entry exists.
func (r *IndexReader) SeekTo(indexValue []byte) (bool, error) {
	if r.iter != nil {
		_ = r.iter.Close()
	}
	r.iter = r.store.Seek(indexValue)
	if !r.iter.Next() {
		err := r.iter.Err()
		_ = r.iter.Close()
		r.iter = nil
		return false, err
	}
	r.current = append([]byte(nil), r.iter.Value()...)
	return true, nil
}

// Next advances to the next entry. Returns false at end of
// iteration.
func (r *IndexReader) Next() (bool, error) {
	if r.iter == nil {
		return false, nil
	}
	if !r.iter.Next() {
		err := r.iter.Err()
		return false, err
	}
	r.current = append([]byte(nil), r.iter.Value()...)
	return true, nil
}

// PrimaryKey returns the primary key at the current position.
// The returned slice is owned by the reader and is valid until
// the next Seek/Next/Close call.
func (r *IndexReader) PrimaryKey() []byte {
	return r.current
}

// Err returns the first error encountered during iteration.
func (r *IndexReader) Err() error {
	if r.iter == nil {
		return nil
	}
	return r.iter.Err()
}

// Close releases the underlying iterator. Idempotent.
func (r *IndexReader) Close() error {
	if r.iter == nil {
		return nil
	}
	err := r.iter.Close()
	r.iter = nil
	r.current = nil
	return err
}
