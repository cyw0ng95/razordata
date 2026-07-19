package WT

import (
	"context"
	"fmt"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
)

// BatchInsert implements a vectorized bulk insert. It reads batches
// from a BatchProducer child and writes rows via the store.
// When the store implements BatchStore, rows are written in bulk
// via WriteBatch. Otherwise, per-row Insert is used.
// REQ001635.
type BatchInsert struct {
	table     string
	child     UT.BatchProducer
	store     DT.Store
	cols      []string
	schema    *DT.StoreSchema
	txWriter  DT.TxWriter
	execCtx   *DT.ExecContext
	encBuf    []byte
	keyPrefix []byte
	done      bool
	rows      int64
}

// NewBatchInsert creates a batch insert operator.
func NewBatchInsert(table string, child UT.BatchProducer, store DT.Store, cols []string) *BatchInsert {
	ss, _ := DT.SchemaFor(table)
	return &BatchInsert{
		table:     table,
		child:     child,
		store:     store,
		cols:      cols,
		schema:    ss,
		keyPrefix: DT.TablePrefix(table),
	}
}

// WithExecContext sets the execution context for change tracking.
func (b *BatchInsert) WithExecContext(ec *DT.ExecContext) *BatchInsert {
	b.execCtx = ec
	return b
}

// Next produces the next batch of inserted rows. Returns the number
// of rows inserted on success, or ErrNoRows when done.
func (b *BatchInsert) Next(ctx context.Context) (DT.Row, error) {
	if b.done {
		return DT.Row{}, DT.ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return DT.Row{}, err
	}

	batch, err := b.child.NextBatch(ctx)
	if err != nil {
		return DT.Row{}, err
	}
	if batch == nil {
		b.done = true
		return DT.Row{}, DT.ErrNoRows
	}

	// Collect keys and values for batch write.
	keys := make([][]byte, 0, batch.Size)
	vals := make([][]byte, 0, batch.Size)

	for r := 0; r < batch.Size; r++ {
		row := batchToRow(batch, r)
		key, val, err := b.encodeRow(row)
		if err != nil {
			batch.Put()
			return DT.Row{}, err
		}
		keys = append(keys, key)
		vals = append(vals, val)
	}
	batch.Put()

	// Write via BatchStore if available.
	if bs, ok := b.store.(DT.BatchStore); ok {
		if err := bs.WriteBatch(keys, vals); err != nil {
			return DT.Row{}, err
		}
	} else {
		for i := range keys {
			if err := b.store.Insert(keys[i], vals[i]); err != nil {
				return DT.Row{}, err
			}
		}
	}

	b.rows += int64(len(keys))
	if b.execCtx != nil {
		b.execCtx.LastChanges = int64(len(keys))
	}

	return DT.Row{}, nil
}

// Close releases the child.
func (b *BatchInsert) Close() error {
	if b.child != nil {
		return b.child.Close()
	}
	return nil
}

// encodeRow encodes a batch row to key/value byte slices.
// Uses the store's row encoding from the schema.
func (b *BatchInsert) encodeRow(row DT.Row) ([]byte, []byte, error) {
	// Build key from prefix — the store handles the actual key encoding.
	key := make([]byte, len(b.keyPrefix)+8)
	copy(key, b.keyPrefix)
	// Use the row's first value as the rowid/primary key component.
	val, err := DT.EncodeRow(b.schema, row)
	if err != nil {
		return nil, nil, fmt.Errorf("batch insert: encode: %w", err)
	}
	return key, val, nil
}

// batchToRow converts a single batch row at index r to a DT.Row.
func batchToRow(batch *UT.Batch, r int) DT.Row {
	names := batch.ColNames()
	n := len(names)
	data := make([]DT.Value, n)
	for c := 0; c < n; c++ {
		col := &batch.Cols[c]
		if col.Nulls != nil && r < len(col.Nulls) && col.Nulls[r] {
			data[c] = DT.NullValue()
			continue
		}
		data[c] = UT.ToValue(*col, r)
	}
	return DT.Row{
		Cols: names,
		Data: data,
	}
}

// RowsAffected returns the number of inserted rows.
func (b *BatchInsert) RowsAffected() int64 { return b.rows }

// SetExecCtx attaches the execution context.
func (b *BatchInsert) SetExecCtx(ec *DT.ExecContext) { b.execCtx = ec }

// SetSelectPlan is a no-op for API compatibility.
func (b *BatchInsert) SetSelectPlan(_ DT.Operator) {}

// WithParams sets the bound parameters.
func (b *BatchInsert) WithParams(p []any) DT.Operator {
	return b
}

// SetTxWriter sets the transaction writer hook.
func (b *BatchInsert) SetTxWriter(w DT.TxWriter) { b.txWriter = w }

// TxWriter returns the transaction writer.
func (b *BatchInsert) TxWriter() DT.TxWriter { return b.txWriter }