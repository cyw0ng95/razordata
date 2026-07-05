package EC

// WrapAt creates a wrapped *Error with auto-filled Code, SQLSTATE, Layer,
// and the given Module and Layer from the caller.
func WrapAt(kind Kind, module Module, layer Layer, err error) *Error {
	if err == nil {
		return nil
	}
	// Build wrapped error with module and layer metadata
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
