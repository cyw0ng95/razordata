package OP

import (
	"bytes"
	"context"
	"sort"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// REQ001616/REQ001627: Pure batch BitmapHeapScan — eliminates the row-based
// wrapper. Drains child BatchProducers to collect primary keys into a
// sorted dedup'd bitmap, then fetches each key from the store and emits
// output batches with up to BatchSize rows of the raw heap value.
//
// The win over per-column IndexScan-then-Filter is that the heap is
// fetched exactly once per row even when multiple index conditions
// match it (avoiding the tuple-duplication that a pure IndexScan
// OR-chain would produce).
//
// Output contract: each emitted row carries exactly one column
// ("__heap__") with KindText. Matches the row-based contract.
type BatchBitmapHeapScan struct {
	children []UT.BatchProducer
	store    DT.Store
	prefix   []byte

	bitmap [][]byte
	pos    int
	built  bool

	// Schema (single __heap__ column).
	colName string
	colType LX.TokenType

	done bool
}

// NewBatchBitmapHeapScan creates a pure batch bitmap heap scan.
// children are BatchProducers (e.g., VectorizedIndexScan) that emit
// primary keys. The store is used to fetch heap values by key.
// REQ001616/REQ001627.
func NewBatchBitmapHeapScan(table string, store DT.Store, children []UT.BatchProducer) *BatchBitmapHeapScan {
	return &BatchBitmapHeapScan{
		children: children,
		store:    store,
		prefix:   DT.TablePrefix(table),
		colName:  bitmapHeapValueCol,
		colType:  LX.T_TEXT,
	}
}

// buildBitmap drains each child BatchProducer, collects primary keys,
// sorts and dedupes them. Idempotent.
func (b *BatchBitmapHeapScan) buildBitmap(ctx context.Context) error {
	if b.built {
		return nil
	}
	seen := make(map[string]struct{})
	for _, child := range b.children {
		if child == nil {
			continue
		}
		for {
			batch, err := child.NextBatch(ctx)
			if err != nil {
				return err
			}
			if batch == nil {
				break
			}
			// Extract primary key from column 0 (matches row-based
			// BitmapHeapScan.buildBitmap which uses row.Data[0]).
			for r := 0; r < batch.Size; r++ {
				key := extractKeyFromBatch(batch, r)
				if key == nil {
					continue
				}
				if _, dup := seen[string(key)]; dup {
					continue
				}
				seen[string(key)] = struct{}{}
				b.bitmap = append(b.bitmap, key)
			}
			batch.Put()
		}
	}
	sort.Slice(b.bitmap, func(i, j int) bool {
		return bytes.Compare(b.bitmap[i], b.bitmap[j]) < 0
	})
	b.built = true
	return nil
}

// NextBatch returns the next batch of heap-resolved rows.
func (b *BatchBitmapHeapScan) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if b.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !b.built {
		if err := b.buildBitmap(ctx); err != nil {
			return nil, err
		}
		defer b.closeChildren()
	}

	// Drain bitmap into output batches.
	for b.pos < len(b.bitmap) {
		output := UT.GetBatch(1)
		output.Cols[0].Name = b.colName
		output.Cols[0].Type = b.colType
		allocateColData(&output.Cols[0], UT.BatchSize, b.colType)

		n := 0
		for b.pos < len(b.bitmap) && n < UT.BatchSize {
			key := b.bitmap[b.pos]
			b.pos++
			val, ok, err := b.store.Get(key)
			if err != nil {
				output.Put()
				return nil, err
			}
			if !ok {
				continue
			}
			output.Cols[0].Data.Strs[n] = string(val)
			n++
		}

		if n == 0 {
			output.Put()
			continue
		}
		output.Size = n
		return output, nil
	}

	b.done = true
	return nil, nil
}

// closeChildren closes each child BatchProducer after their keys have
// been drained. Errors are swallowed because the bitmap is already
// materialised.
func (b *BatchBitmapHeapScan) closeChildren() {
	for _, child := range b.children {
		if child == nil {
			continue
		}
		_ = child.Close()
	}
}

// Close releases the operator. Idempotent.
func (b *BatchBitmapHeapScan) Close() error {
	b.closeChildren()
	b.bitmap = nil
	b.built = false
	b.pos = 0
	return nil
}

// extractKeyFromBatch extracts the primary key (column 0) as bytes
// from a batch at the given row index.
func extractKeyFromBatch(batch *UT.Batch, rowIdx int) []byte {
	if batch == nil || rowIdx >= batch.Size || len(batch.Cols) == 0 {
		return nil
	}
	col := &batch.Cols[0]
	switch col.Type {
	case LX.T_INT_KW, LX.T_BIGINT:
		if rowIdx < len(col.Data.Ints) {
			return []byte(itoaInt64(col.Data.Ints[rowIdx]))
		}
	case LX.T_TEXT, LX.T_VARCHAR:
		if rowIdx < len(col.Data.Strs) {
			return []byte(col.Data.Strs[rowIdx])
		}
	case LX.T_BLOB:
		if rowIdx < len(col.Data.Strs) {
			return []byte(col.Data.Strs[rowIdx])
		}
	}
	return nil
}

// itoaInt64 converts an int64 to its decimal string representation.
func itoaInt64(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}