package AP

import (
	"errors"
	"fmt"
)

type Kind int

const (
	KindNotFound Kind = iota
	KindDuplicateKey
	KindLocked
	KindCorrupt
	KindSyntax
	KindTypeMismatch
	KindTxAborted
	KindIO
	KindUpgradeRequired
	KindReadOnly
	KindDeadlineExceeded
	KindConstraint
	KindClosed
	KindInvalidOptions
)

func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "NotFound"
	case KindDuplicateKey:
		return "DuplicateKey"
	case KindLocked:
		return "Locked"
	case KindCorrupt:
		return "Corrupt"
	case KindSyntax:
		return "Syntax"
	case KindTypeMismatch:
		return "TypeMismatch"
	case KindTxAborted:
		return "TxAborted"
	case KindIO:
		return "IO"
	case KindUpgradeRequired:
		return "UpgradeRequired"
	case KindReadOnly:
		return "ReadOnly"
	case KindDeadlineExceeded:
		return "DeadlineExceeded"
	case KindConstraint:
		return "Constraint"
	case KindClosed:
		return "Closed"
	case KindInvalidOptions:
		return "InvalidOptions"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

type Error struct {
	Kind    Kind
	Message string
	wrapped error
}

func (e *Error) Error() string {
	if e.wrapped != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.wrapped)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.wrapped }

func New(kind Kind, msg string) *Error {
	return &Error{Kind: kind, Message: msg}
}

func Wrap(kind Kind, err error) *Error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Message: err.Error(), wrapped: err}
}

func IsKind(err error, kind Kind) bool {
	var target *Error
	if errors.As(err, &target) {
		return target.Kind == kind
	}
	return false
}

func IsRetryable(err error) bool {
	var target *Error
	if errors.As(err, &target) {
		switch target.Kind {
		case KindIO, KindLocked:
			return true
		}
	}
	return false
}

func IsFatal(err error) bool {
	var target *Error
	if errors.As(err, &target) {
		switch target.Kind {
		case KindIO, KindLocked:
			return false
		}
		return true
	}
	return err != nil
}
