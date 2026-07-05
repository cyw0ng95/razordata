package EC

import (
	"errors"
	"fmt"
)

// Code is a stable internal error code (e.g. "RZR-SQL-001").
type Code string

// SQLSTATE is a 5-character standard SQL state code.
type SQLSTATE string

func (c Code) String() string     { return string(c) }
func (s SQLSTATE) String() string { return string(s) }

// codeEntry maps a Kind to its Code and SQLSTATE.
type codeEntry struct {
	Code     Code
	SQLSTATE SQLSTATE
}

var codeTable = map[Kind]codeEntry{
	// stable since v0.9.0
	KindNotFound:         {"RZR-SQL-001", "02000"},
	KindDuplicateKey:     {"RZR-SQL-002", "23000"},
	KindTypeMismatch:     {"RZR-SQL-003", "22005"},
	KindSyntax:           {"RZR-SQL-004", "42000"},
	KindParse:            {"RZR-SQL-005", "42000"},
	KindConstraint:       {"RZR-SQL-006", "23000"},
	KindLocked:           {"RZR-SQL-007", "40001"},
	KindTxAborted:        {"RZR-SQL-008", "40001"},
	KindDeadlineExceeded: {"RZR-SQL-009", "57014"},
	KindIO:               {"RZR-IO-001", "08006"},
	KindCorrupt:          {"RZR-IO-002", "08001"},
	KindReadOnly:         {"RZR-CFG-001", "25006"},
	KindUpgradeRequired:  {"RZR-CFG-002", "08001"},
	KindClosed:           {"RZR-CFG-003", "08003"},
	KindInvalidOptions:   {"RZR-CFG-004", "08001"},
	KindInternal:         {"RZR-INT-001", "58000"},
	KindNotImplemented:   {"RZR-INT-002", "0A000"},
	KindConflict:         {"RZR-INT-003", "40001"},
	// stable since v0.9.0
	KindResourceExhausted: {"RZR-INT-004", "54000"},
}

// CodeOf returns the stable Code for a Kind. Panics on unknown Kind.
func CodeOf(k Kind) Code {
	if e, ok := codeTable[k]; ok {
		return e.Code
	}
	panic(fmt.Sprintf("EC.CodeOf: unknown Kind %d", int(k)))
}

// SQLStateOf returns the SQLSTATE for a Kind. Panics on unknown Kind.
func SQLStateOf(k Kind) SQLSTATE {
	if e, ok := codeTable[k]; ok {
		return e.SQLSTATE
	}
	panic(fmt.Sprintf("EC.SQLStateOf: unknown Kind %d", int(k)))
}

// IsCode checks whether any error in the chain matches the given Code.
func IsCode(err error, code Code) bool {
	for err != nil {
		var e *Error
		if errors.As(err, &e) {
			if e.Code == code {
				return true
			}
		}
		if e, ok := err.(*Error); ok {
			err = e.wrapped
		} else {
			type unwrapper interface{ Unwrap() error }
			if u, ok := err.(unwrapper); ok {
				err = u.Unwrap()
			} else {
				break
			}
		}
	}
	return false
}

// IsSQLState checks whether any error in the chain matches the given SQLSTATE.
func IsSQLState(err error, state SQLSTATE) bool {
	for err != nil {
		var e *Error
		if errors.As(err, &e) {
			if e.SQLSTATE == state {
				return true
			}
		}
		if e, ok := err.(*Error); ok {
			// Move to wrapped error
			err = e.wrapped
		} else {
			type unwrapper interface{ Unwrap() error }
			if u, ok := err.(unwrapper); ok {
				err = u.Unwrap()
			} else {
				break
			}
		}
	}
	return false
}
