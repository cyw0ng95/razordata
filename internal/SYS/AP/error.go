package AP

import (
	"errors"
	"fmt"
)

// Kind classifies an error for programmatic handling (retryability,
// fatality, user-facing messages). Each Kind maps to exactly one
// retry/fatal policy.
type Kind int

const (
	KindNotFound          Kind = iota // key/resource not found
	KindDuplicateKey                  // unique constraint violated
	KindLocked                        // resource temporarily locked
	KindCorrupt                       // data corruption detected
	KindSyntax                        // SQL parse/compile error
	KindTypeMismatch                  // type coercion failure
	KindTxAborted                     // transaction rolled back
	KindIO                            // transient I/O failure
	KindUpgradeRequired               // format version too old
	KindReadOnly                      // write attempted on read-only
	KindDeadlineExceeded              // context deadline
	KindConstraint                    // CHECK/NOT NULL/FK violation
	KindClosed                        // resource already closed
	KindInvalidOptions                // bad configuration
)

// String returns a human-readable label for the Kind.
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

// Error is the razordata unified error type. It carries a Kind for
// programmatic classification and supports wrapping lower-layer
// errors via Unwrap.
type Error struct {
	Kind    Kind
	Message string
	wrapped error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.wrapped != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.wrapped)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

// Unwrap returns the wrapped error, enabling errors.Is / errors.As
// chain traversal.
func (e *Error) Unwrap() error {
	return e.wrapped
}

// New creates a new Error with the given Kind and message.
func New(kind Kind, msg string) *Error {
	return &Error{Kind: kind, Message: msg}
}

// Wrap creates a new Error that wraps an existing error. The
// original error is preserved in the chain for errors.Is/As.
func Wrap(kind Kind, err error) *Error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	return &Error{Kind: kind, Message: msg, wrapped: err}
}

// IsKind reports whether err (or any error in its chain) is an
// *Error with the given Kind.
func IsKind(err error, kind Kind) bool {
	var target *Error
	if errors.As(err, &target) {
		return target.Kind == kind
	}
	return false
}

// IsRetryable reports whether err should be retried. Errors with
// KindIO or KindLocked are retryable; all others are not.
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

// IsFatal reports whether err is non-retryable. All Kinds except
// KindIO and KindLocked are fatal.
func IsFatal(err error) bool {
	var target *Error
	if errors.As(err, &target) {
		switch target.Kind {
		case KindIO, KindLocked:
			return false
		}
		return true
	}
	// Unknown errors are conservatively fatal.
	return err != nil
}
