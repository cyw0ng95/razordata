// Package TX implements the Transaction cluster: wraps a VL Tx, tracks
// every DML write the session issues so ROLLBACK can restore the
// pre-tx value, and supports savepoints.
package TX

import (
	"context"
	"sync"

	ex "github.com/cyw0ng95/razordata/internal/SQB/EX"
	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
	sy "github.com/cyw0ng95/razordata/internal/SYS/SY"
	vl "github.com/cyw0ng95/razordata/internal/TXN/VL"
)

// Transaction is the concrete ap.Transaction.
// Shadow write semantics: every Exec that goes through Transaction
// lands in the engine (so reads within the same session see the
// data) and is recorded in writeSet[key] = pre-tx value. On Commit,
// writeSet is discarded. On Rollback, each entry is restored: if
// the key existed before the tx its previous value is rewritten, if
// it was absent it is removed. This satisfies ap.R22/R23.
//
// REQ000641: in-memory tables (no engine backing) record a
// snapshot of the table before the first mutation. On rollback
// the snapshot is restored via the in-memory table registry.
type Transaction struct {
	onFinish func()
	engine   *sy.Engine
	tx       vl.Tx
	mu       sync.Mutex

	writeSet          map[string]writeEntry
	inMemorySnapshots map[string][]ex.Row // table → pre-tx rows
	finished          bool
	savepoints        []savepoint
	isolationLevel    ap.IsolationLevel // REQ000123
}

type writeEntry struct {
	existed bool
	value   []byte
}

type savepoint struct {
	name     string
	writeSet map[string]writeEntry
}

// NewTransaction binds a freshly-allocated VL Tx to a session for SQL
// execution. writeSet is initialized lazily on first write.
func NewTransaction(engine *sy.Engine, tx vl.Tx) *Transaction {
	return &Transaction{
		engine:            engine,
		tx:                tx,
		writeSet:          make(map[string]writeEntry),
		inMemorySnapshots: make(map[string][]ex.Row),
		isolationLevel:    ap.IsolationReadCommitted, // REQ000061: default RC
	}
}

// SetOnFinish registers a callback invoked after the transaction
// commits or rolls back. Used by Session.Begin to clear its
// active-transaction reference.
func (t *Transaction) SetOnFinish(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFinish = fn
}

// SetIsolationLevel sets the transaction's isolation level (REQ000123).
func (t *Transaction) SetIsolationLevel(level ap.IsolationLevel) {
	t.isolationLevel = level
}

func (t *Transaction) Query(ctx context.Context, sql string, args ...any) (*ap.Rows, error) {
	if t.engine.IsClosed() {
		return nil, ap.ErrClosed
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return nil, ap.ErrTxAborted
	}
	t.mu.Unlock()
	exe := t.engine.Executor()
	// REQ000255: set per-statement snapshot for read-committed
	if t.isolationLevel == ap.IsolationReadCommitted {
		t.engine.SetSnapshot(t.engine.CurrentTS())
		defer t.engine.SetSnapshot(0)
	}
	stream, err := exe.QueryStream(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	next := func() (ap.Row, error) {
		row, err := stream.Next()
		if err != nil {
			if err == ex.ErrNoRows {
				return ap.Row{}, ap.ErrNoRows
			}
			return ap.Row{}, err
		}
		// REQ000862: AP.Row.Data is now []AP.Value (same type as EX.Row.Data),
		// so no boxing conversion is needed.
		return ap.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
	}
	closer := func() error { return stream.Close() }
	return ap.NewRows(stream.Cols(), stream.Types(), next, closer), nil
}

func (t *Transaction) Exec(ctx context.Context, sql string, args ...any) (ap.Result, error) {
	if t.engine.IsClosed() {
		return ap.Result{}, ap.ErrClosed
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return ap.Result{}, ap.ErrTxAborted
	}
	t.mu.Unlock()

	exe := t.engine.Executor()
	// REQ000255: set per-statement snapshot for read-committed
	if t.isolationLevel == ap.IsolationReadCommitted {
		t.engine.SetSnapshot(t.engine.CurrentTS())
		defer t.engine.SetSnapshot(0)
	}
	exe.SetTxWriter(t)
	defer exe.ClearTxWriter()
	res, err := exe.Exec(ctx, sql, args...)
	if err != nil {
		return ap.Result{}, err
	}
	return ap.Result{
		RowsAffected: res.RowsAffected,
		LastInsertID: res.LastInsertID,
	}, nil
}

// RecordWrite is invoked by the executor for each key/value it writes
// during a TxExec. It captures the pre-tx value so Rollback can
// restore.
func (t *Transaction) RecordWrite(key []byte, newValue []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	k := string(key)
	if _, seen := t.writeSet[k]; seen {
		// First-write's pre-tx value wins.
		return
	}
	eng := t.engine.Engine()
	prev, err := eng.Get(key)
	entry := writeEntry{existed: false}
	if err == nil {
		entry.existed = true
		entry.value = prev
	}
	t.writeSet[k] = entry
}

// RecordInMemoryTable captures the pre-tx snapshot of an in-memory
// table. Only the first call per table is recorded; subsequent
// writes to the same table use the original snapshot for rollback.
// REQ000641.
func (t *Transaction) RecordInMemoryTable(table string, snapshot []ex.Row) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	if _, seen := t.inMemorySnapshots[table]; seen {
		return
	}
	t.inMemorySnapshots[table] = snapshot
}

func (t *Transaction) Commit(ctx context.Context) error {
	if t.engine.IsClosed() {
		return ap.ErrClosed
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return ap.ErrTxAborted
	}
	if err := t.tx.Commit(ctx); err != nil {
		t.mu.Unlock()
		return err
	}
	t.finished = true
	t.writeSet = nil
	t.inMemorySnapshots = nil
	t.savepoints = nil
	fn := t.onFinish
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

// Rollback aborts the transaction.
func (t *Transaction) Rollback(ctx context.Context) error {
	if t.engine.IsClosed() {
		return ap.ErrClosed
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return ap.ErrTxAborted
	}
	// REQ000617: persist the abort WAL record (RTRollback) first so a
	// crash during engine state restoration is recoverable.
	if err := t.tx.Abort(ctx); err != nil {
		t.mu.Unlock()
		return err
	}
	eng := t.engine.Engine()
	keys := make([]string, 0, len(t.writeSet))
	for k := range t.writeSet {
		keys = append(keys, k)
	}
	rollbackErr := error(nil)
	for _, k := range keys {
		entry := t.writeSet[k]
		if entry.existed {
			if err := eng.Insert([]byte(k), entry.value); err != nil {
				rollbackErr = err
				break
			}
		} else {
			if err := eng.Delete([]byte(k)); err != nil {
				rollbackErr = err
				break
			}
		}
	}
	// REQ000641: restore in-memory tables to their pre-tx snapshots.
	if rollbackErr == nil && len(t.inMemorySnapshots) > 0 {
		rollbackErr = ex.RestoreInMemoryTables(t.inMemorySnapshots)
	}
	t.finished = true
	t.writeSet = nil
	t.inMemorySnapshots = nil
	t.savepoints = nil
	fn := t.onFinish
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
	return rollbackErr
}

func (t *Transaction) Savepoint(ctx context.Context, name string) error {
	if t.engine.IsClosed() {
		return ap.ErrClosed
	}
	if name == "" {
		return ap.ErrUnknownSavepoint
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ap.ErrTxAborted
	}
	snap := make(map[string]writeEntry, len(t.writeSet))
	for k, v := range t.writeSet {
		snap[k] = v
	}
	t.savepoints = append(t.savepoints, savepoint{name: name, writeSet: snap})
	return nil
}

func (t *Transaction) ReleaseSavepoint(ctx context.Context, name string) error {
	if t.engine.IsClosed() {
		return ap.ErrClosed
	}
	if name == "" {
		return ap.ErrUnknownSavepoint
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ap.ErrTxAborted
	}
	idx := -1
	for i := len(t.savepoints) - 1; i >= 0; i-- {
		if t.savepoints[i].name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ap.ErrUnknownSavepoint
	}
	t.savepoints = t.savepoints[:idx]
	return nil
}

func (t *Transaction) RollbackTo(ctx context.Context, name string) error {
	if t.engine.IsClosed() {
		return ap.ErrClosed
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ap.ErrTxAborted
	}
	idx := -1
	for i := len(t.savepoints) - 1; i >= 0; i-- {
		if t.savepoints[i].name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ap.ErrUnknownSavepoint
	}
	target := t.savepoints[idx].writeSet
	eng := t.engine.Engine()
	for k, entry := range t.writeSet {
		if _, ok := target[k]; ok {
			continue
		}
		if entry.existed {
			if err := eng.Insert([]byte(k), entry.value); err != nil {
				return err
			}
		} else {
			if err := eng.Delete([]byte(k)); err != nil {
				return err
			}
		}
	}
	t.writeSet = target
	t.savepoints = t.savepoints[:idx]
	return nil
}

func (t *Transaction) Finished() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.finished
}

// init wires the constructor into SY's txNew hook to break the
// SY↔TX import cycle. SY exposes SetTxNew; TX calls it on init.
func init() {
	sy.RegisterTxConstructor(func(e *sy.Engine, tx vl.Tx) ap.Transaction {
		return NewTransaction(e, tx)
	})
}
