package AP

import "errors"

// Classification describes an error's kind, code, SQLSTATE, and retry/fatal flags.
type Classification struct {
	Kind      Kind
	Code      Code
	SQLSTATE  SQLSTATE
	Retryable bool
	Fatal     bool
}

// retryableKinds lists kinds that should be retried.
var retryableKinds = map[Kind]bool{
	KindIO:     true,
	KindLocked: true,
}

// fatalKinds lists kinds that are fatal. By default, any *AP.Error not in this map is fatal.
var fatalKinds = map[Kind]bool{
	KindIO:     false,
	KindLocked: false,
}

// Classify returns the full classification of an error.
// Non-*AP.Error errors are classified as KindInternal, fatal.
func Classify(err error) Classification {
	var e *Error
	if AsError(err, &e) {
		return Classification{
			Kind:      e.Kind,
			Code:      e.Code,
			SQLSTATE:  e.SQLSTATE,
			Retryable: retryableKinds[e.Kind],
			Fatal:     !retryableKinds[e.Kind],
		}
	}
	if err == nil {
		return Classification{}
	}
	return Classification{Kind: KindInternal, Code: "RZR-INT-001", SQLSTATE: "58000", Fatal: true}
}

// IsRetryable returns true if the error is retryable.
func IsRetryable(err error) bool {
	return Classify(err).Retryable
}

// IsFatal returns true if the error is fatal.
func IsFatal(err error) bool {
	return Classify(err).Fatal
}

// AsError extracts the first *Error from the error chain.
// Exists as a package-level helper so callers don't need to import errors.
func AsError(err error, target **Error) bool {
	return errors.As(err, target)
}