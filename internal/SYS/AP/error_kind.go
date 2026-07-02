package AP

import "fmt"

// Kind classifies the nature of an error.
type Kind int

const (
	KindNotFound          Kind = iota
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
	KindInternal
	KindNotImplemented
	KindConflict
	KindResourceExhausted
	KindParse
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
	case KindInternal:
		return "Internal"
	case KindNotImplemented:
		return "NotImplemented"
	case KindConflict:
		return "Conflict"
	case KindResourceExhausted:
		return "ResourceExhausted"
	case KindParse:
		return "Parse"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Entity constants for KindNotFound.Fields["entity"].
const (
	EntityTable      = "table"
	EntityColumn     = "column"
	EntityKey        = "key"
	EntitySavepoint  = "savepoint"
	EntityIndex      = "index"
	EntityView       = "view"
	EntityTrigger    = "trigger"
	EntityConstraint = "constraint"
	EntityRow        = "row"
)