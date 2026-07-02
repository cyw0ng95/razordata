package rp

import (
	"fmt"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
)

// ReplayError captures a WAL replay failure with LSN context.
type ReplayError struct {
	LSN    uint64
	Cause  error
	Op     string
	Record string // type of record being replayed
}

func (e *ReplayError) Error() string {
	if e.Record != "" {
		return fmt.Sprintf("replay failed at LSN %d: %s: %v", e.LSN, e.Record, e.Cause)
	}
	return fmt.Sprintf("replay failed at LSN %d: %v", e.LSN, e.Cause)
}

func (e *ReplayError) Unwrap() error { return e.Cause }

// ToAPError converts to a structured AP.Error.
func (e *ReplayError) ToAPError() *AP.Error {
	ae := AP.Wrap(AP.KindCorrupt, e.Cause)
	ae.Module = "WAL/RP"
	ae.Layer = AP.LayerWAL
	ae.Op = e.Op
	ae.Fields = map[string]string{
		"lsn":    fmt.Sprintf("%d", e.LSN),
		"record": e.Record,
	}
	return ae
}
