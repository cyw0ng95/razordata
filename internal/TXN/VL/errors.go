package VL

import "errors"

var (
	ErrNoSlotsAvailable   = errors.New("txn: no slots available")
	ErrInsertFailed      = errors.New("txn: insert failed")
	ErrDeleteFailed      = errors.New("txn: delete failed")
	ErrValidationFailed  = errors.New("txn: validation failed")
	ErrCommitFailed      = errors.New("txn: commit failed")
	ErrInvalidWALRecord  = errors.New("txn: invalid WAL record")
)
