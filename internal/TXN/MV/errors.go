package MV

import "errors"

var (
	ErrNotFound       = errors.New("txn: key not found")
	ErrDuplicateKey   = errors.New("txn: duplicate key")
	ErrTxAborted      = errors.New("txn: transaction aborted")
	ErrArenaExhausted = errors.New("txn: arena exhausted")
	ErrCASFailure     = errors.New("txn: CAS failed")
	ErrInvalidTx      = errors.New("txn: invalid transaction")
)
