package AP

// WrapAt creates a wrapped *Error with auto-filled Code, SQLSTATE, Layer,
// and the given Module and Layer from the caller.
func WrapAt(kind Kind, module Module, layer Layer, err error) *Error {
	if err == nil {
		return nil
	}
	e := &Error{
		Kind:     kind,
		Code:     CodeOf(kind),
		SQLSTATE: SQLStateOf(kind),
		Module:   module,
		Layer:    layer,
		Message:  err.Error(),
		wrapped:  err,
	}
	emit(e)
	return e
}

// sentinelEntry maps a known sentinel pointer to its Kind.
var sentinelTable = map[*Error]Kind{
	ErrNotFound:         KindNotFound,
	ErrDuplicateKey:     KindDuplicateKey,
	ErrLocked:           KindLocked,
	ErrCorrupt:          KindCorrupt,
	ErrSyntax:           KindSyntax,
	ErrTypeMismatch:     KindTypeMismatch,
	ErrTxAborted:        KindTxAborted,
	ErrIO:               KindIO,
	ErrUpgradeRequired:  KindUpgradeRequired,
	ErrReadOnly:         KindReadOnly,
	ErrDeadlineExceeded: KindDeadlineExceeded,
	ErrAlreadyOpen:      KindInvalidOptions,
	ErrNotOpen:          KindClosed,
	ErrClosed:           KindClosed,
	ErrInvalidOptions:   KindInvalidOptions,
	ErrNoActiveTxn:      KindConstraint,
	ErrUnknownSavepoint: KindConstraint,
	ErrConstraint:       KindConstraint,
}

// FromSentinel wraps a known sentinel error into a structural *AP.Error.
// Returns the wrapped error if the sentinel is known, or the original error
// wrapped as KindInternal if unknown.
func FromSentinel(err error, module Module, layer Layer) *Error {
	if err == nil {
		return nil
	}
	if apErr, ok := err.(*Error); ok {
		known, found := sentinelTable[apErr]
		if found {
			return WrapAt(known, module, layer, err)
		}
	}
	return WrapAt(KindInternal, module, layer, err)
}