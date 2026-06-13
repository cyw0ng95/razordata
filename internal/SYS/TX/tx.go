// Package TX implements the Transaction cluster: wraps a VL Tx, tracks
// every DML write the session issues so ROLLBACK can restore the
// pre-tx value, and supports savepoints.
package TX

import (
	"context"
	"sync"

	ex "github.com/cyw0ng95/razordata/internal/SQL/EX"
	ap "github.com/cyw0ng95/razordata/internal/SYS/AP"
	sy "github.com/cyw0ng95/razordata/internal/SYS/SY"
	vl "github.com/cyw0ng95/razordata/internal/TXN/VL"
)

// Transaction is the concrete ap.Transaction.
//
// Shadow write semantics: every Exec that goes through Transaction
// lands in the engine (so reads within the same session see the
// data) and is recorded in writeSet[key] = pre-tx value. On Commit,
// writeSet is discarded. On Rollback, each entry is restored: if
// the key existed before the tx its previous value is rewritten, if
// it was absent it is removed. This satisfies ap.R22/R23.
type Transaction struct {
	session  *sy.Engine
	tx       vl.Tx
	mu       sync.Mutex

	writeSet      map[string]writeEntry
	finished      bool
	savepoints    []savepoint
	isolationLevel ap.IsolationLevel // REQ000123
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
func NewTransaction(session *sy.Engine, tx vl.Tx) *Transaction {
	return &Transaction{
		session:       session,
		tx:            tx,
		writeSet:      make(map[string]writeEntry),
		isolationLevel: ap.IsolationReadCommitted, // REQ000061: default RC
	}
}

// SetIsolationLevel sets the transaction's isolation level (REQ000123).
func (t *Transaction) SetIsolationLevel(level ap.IsolationLevel) {
	t.isolationLevel = level
}

func (t *Transaction) Query(ctx context.Context, sql string, args ...any) (*ap.Rows, error) {
	if t.session.IsClosed() {
		return nil, ap.ErrClosed
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return nil, ap.ErrTxAborted
	}
	t.mu.Unlock()
	exe := t.session.Executor()
	// REQ000255: set per-statement snapshot for read-committed
	if t.isolationLevel == ap.IsolationReadCommitted {
		t.session.SetSnapshot(t.session.CurrentTS())
		defer t.session.SetSnapshot(0)
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
		return ap.Row{Cols: row.Cols, Types: row.Types, Data: row.Data}, nil
	}
	closer := func() error { return stream.Close() }
	return ap.NewRows(stream.Cols(), stream.Types(), next, closer), nil
}

func (t *Transaction) Exec(ctx context.Context, sql string, args ...any) (ap.Result, error) {
	if t.session.IsClosed() {
		return ap.Result{}, ap.ErrClosed
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return ap.Result{}, ap.ErrTxAborted
	}
	t.mu.Unlock()

	exe := t.session.Executor()
	// REQ000255: set per-statement snapshot for read-committed
	if t.isolationLevel == ap.IsolationReadCommitted {
		t.session.SetSnapshot(t.session.CurrentTS())
		defer t.session.SetSnapshot(0)
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
	eng := t.session.Engine()
	prev, err := eng.Get(key)
	entry := writeEntry{existed: false}
	if err == nil {
		entry.existed = true
		entry.value = prev
	}
	t.writeSet[k] = entry
}

func (t *Transaction) Commit(ctx context.Context) error {
	if t.session.IsClosed() {
		return ap.ErrClosed
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ap.ErrTxAborted
	}
	if err := t.tx.Commit(ctx); err != nil {
		return err
	}
	t.finished = true
	t.writeSet = nil
	t.savepoints = nil
	return nil
}

func (t *Transaction) Rollback(ctx context.Context) error {
	if t.session.IsClosed() {
		return ap.ErrClosed
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return ap.ErrTxAborted
	}
	eng := t.session.Engine()
	keys := make([]string, 0, len(t.writeSet))
	for k := range t.writeSet {
		keys = append(keys, k)
	}
	for _, k := range keys {
		entry := t.writeSet[k]
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
	if err := t.tx.Abort(ctx); err != nil {
		return err
	}
	t.finished = true
	t.writeSet = nil
	t.savepoints = nil
	return nil
}

func (t *Transaction) Savepoint(ctx context.Context, name string) error {
	if t.session.IsClosed() {
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

func (t *Transaction) RollbackTo(ctx context.Context, name string) error {
	if t.session.IsClosed() {
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
	eng := t.session.Engine()
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
