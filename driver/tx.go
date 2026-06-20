package driver

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SE"
)

type Tx struct {
	tx      AP.Transaction
	session *SE.Session
}

func (t *Tx) Commit() error {
	if t == nil || t.tx == nil {
		return nil
	}
	if err := t.tx.Commit(context.Background()); err != nil {
		return err
	}
	if t.session != nil {
		t.session.ClearTxn()
	}
	return nil
}

func (t *Tx) Rollback() error {
	if t == nil || t.tx == nil {
		return nil
	}
	if err := t.tx.Rollback(context.Background()); err != nil {
		return err
	}
	if t.session != nil {
		t.session.ClearTxn()
	}
	return nil
}
