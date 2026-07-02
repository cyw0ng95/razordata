package VL

import (
	"fmt"
	"strings"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// DeadlockError captures a detected deadlock between transactions.
type DeadlockError struct {
	TxID    uint64
	Holders []uint64
	Cause   error
	Op      string
}

func (e *DeadlockError) Error() string {
	var holders []string
	for _, h := range e.Holders {
		holders = append(holders, fmt.Sprintf("%d", h))
	}
	return fmt.Sprintf("deadlock detected: tx %d waiting on tx %s", e.TxID, strings.Join(holders, ", "))
}

func (e *DeadlockError) Unwrap() error { return e.Cause }

// ToAPError converts to a structured AP.Error.
func (e *DeadlockError) ToAPError() *AP.Error {
	ae := AP.Wrap(AP.KindConflict, e.Cause)
	ae.Module = "TXN/VL"
	ae.Layer = AP.LayerTXN
	ae.Op = e.Op
	ae.Fields = map[string]string{
		"tx_id": fmt.Sprintf("%d", e.TxID),
	}
	return ae
}
