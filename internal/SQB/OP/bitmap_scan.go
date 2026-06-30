package OP

import (
	"bytes"
	"context"
	"sort"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// BitmapHeapScan combines the row-key output of multiple secondary-
// index scans into a single heap fetch. REQ001106.
//
// Algorithm:
//  1. Drain each child IndexScan's Next() and collect every emitted
//     primary key into a sorted, dedup'd slice (the "bitmap" —
//     here: a list of row keys, since razor is row-oriented rather
//     than tuple-oriented).
//  2. On the operator's own Next(), look up each row key in the
//     store via Store.Get and emit the row.
//
// The win over per-column IndexScan-then-Filter is that the heap is
// fetched exactly once per row even when multiple index conditions
// match it (avoiding the tuple-duplication that a pure IndexScan
// OR-chain would produce).
type BitmapHeapScan struct {
	table      string
	store      DT.Store
	schema     *DT.StoreSchema
	prefix     []byte
	bitmap     [][]byte
	pos        int
	built      bool
	indexScans []pl.Operator
	cols       []string
	types      []LX.TokenType
	colIndex   map[string]int
}

// NewBitmapHeapScan creates a BitmapHeapScan that merges the row
// keys from the supplied IndexScan children and fetches each row
// from the engine store. The children are consumed and closed
// during the first Next() call.
func NewBitmapHeapScan(table string, store DT.Store, children []pl.Operator) *BitmapHeapScan {
	ss, _ := DT.SchemaFor(table)
	prefix := DT.TablePrefix(table)
	return &BitmapHeapScan{
		table:      table,
		store:      store,
		schema:     ss,
		prefix:     prefix,
		indexScans: children,
	}
}

// Table returns the underlying table name.
func (b *BitmapHeapScan) Table() string { return b.table }

// buildBitmap drains each child IndexScan, collects primary keys,
// sorts and dedupes them. Safe to call repeatedly (idempotent).
func (b *BitmapHeapScan) buildBitmap(ctx context.Context) error {
	if b.built {
		return nil
	}
	seen := make(map[string]struct{})
	for _, child := range b.indexScans {
		if child == nil {
			continue
		}
		for {
			row, err := child.Next(ctx)
			if err != nil {
				if err == pl.ErrNoRows {
					break
				}
				return err
			}
			// The primary key is the first column of the row
			// when an IndexScan emits row keys. For safety, the
			// scan's first column is treated as the key.
			if len(row.Data) == 0 {
				continue
			}
			key := append([]byte(nil), row.Data[0].S...)
			if _, dup := seen[string(key)]; dup {
				continue
			}
			seen[string(key)] = struct{}{}
			b.bitmap = append(b.bitmap, key)
		}
	}
	sort.Slice(b.bitmap, func(i, j int) bool {
		return bytes.Compare(b.bitmap[i], b.bitmap[j]) < 0
	})
	b.built = true
	return nil
}

// Next returns the next row from the bitmap-resolved heap. The
// first call drains all child IndexScans and closes them.
func (b *BitmapHeapScan) Next(ctx context.Context) (pl.Row, error) {
	if err := ctx.Err(); err != nil {
		return pl.Row{}, err
	}
	if !b.built {
		if err := b.buildBitmap(ctx); err != nil {
			return pl.Row{}, err
		}
		defer b.closeChildren()
	}
	for b.pos < len(b.bitmap) {
		key := b.bitmap[b.pos]
		b.pos++
		val, ok, err := b.store.Get(key)
		if err != nil {
			return pl.Row{}, err
		}
		if !ok {
			continue
		}
		// Decode the value as a single-column Row for downstream
		// operators. Full schema decoding would require a row
		// encoder; for REQ001106 the bitmap operator focuses on
		// the "did we fetch this key" question.
		row := pl.Row{
			Cols:     b.cols,
			Types:    b.types,
			ColIndex: b.colIndex,
			Data:     []pl.Value{{Kind: pl.KindText, S: string(val)}},
		}
		return row, nil
	}
	return pl.Row{}, pl.ErrNoRows
}

// closeChildren closes each child IndexScan after their keys have
// been drained. Errors are swallowed because the bitmap is already
// materialised.
func (b *BitmapHeapScan) closeChildren() {
	for _, child := range b.indexScans {
		if child == nil {
			continue
		}
		_ = child.Close()
	}
}

// Close releases the operator. Idempotent.
func (b *BitmapHeapScan) Close() error {
	b.closeChildren()
	b.bitmap = nil
	b.built = false
	b.pos = 0
	return nil
}
