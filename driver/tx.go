package driver

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// Tx is a database/sql transaction backed by AP.Transaction.
type Tx struct {
	tx AP.Transaction
}

// Commit commits the transaction.
func (t *Tx) Commit() error {
	if t == nil || t.tx == nil {
		return nil
	}
	return t.tx.Commit(context.Background())
}

// Rollback aborts the transaction.
func (t *Tx) Rollback() error {
	if t == nil || t.tx == nil {
		return nil
	}
	return t.tx.Rollback(context.Background())
}
