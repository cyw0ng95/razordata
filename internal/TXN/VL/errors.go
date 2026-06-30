package VL

import "errors"

var (
	ErrNoSlotsAvailable = errors.New("txn: no slots available")
	ErrInsertFailed     = errors.New("txn: insert failed")
	ErrDeleteFailed     = errors.New("txn: delete failed")
	ErrValidationFailed = errors.New("txn: validation failed")
	ErrCommitFailed     = errors.New("txn: commit failed")
	ErrInvalidWALRecord = errors.New("txn: invalid WAL record")
	ErrManagerClosed    = errors.New("txn: manager is closed")
	ErrTxFinished       = errors.New("txn: transaction already finished")
	ErrWriteConflict    = errors.New("txn: write-write conflict")
	// REQ000995: savepoint errors.
	ErrSavepointNotSupported = errors.New("txn: savepoints not supported on this transaction")
	ErrSavepointNotFound     = errors.New("txn: savepoint not found")
	ErrRollbackPastSavepoint = errors.New("txn: cannot rollback past savepoint")
)
