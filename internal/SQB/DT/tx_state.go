package DT

import (
	"sync/atomic"
)

// currentTxWriter is the package-level current TxWriter. Set by
// Executor.SetTxWriter and read by Insert/Update/Delete operators
// (both in-memory and store-backed) so that transactions can
// capture pre-write state for rollback. REQ000588.
var currentTxWriter atomic.Pointer[TxWriter]

// foreignKeysEnabled controls whether FK constraint enforcement is
// active. PRAGMA foreign_keys = ON/OFF toggles this per-session.
// Default is true (enforced). REQ000905.
var foreignKeysEnabled atomic.Bool

func init() {
	foreignKeysEnabled.Store(true)
}

// SetForeignKeysEnabled stores the FK enforcement toggle.
func SetForeignKeysEnabled(v bool) {
	foreignKeysEnabled.Store(v)
}

// IsForeignKeysEnabled returns the current FK enforcement toggle.
func IsForeignKeysEnabled() bool {
	return foreignKeysEnabled.Load()
}

// SetCurrentTxWriter stores w in the package-level slot.
func SetCurrentTxWriter(w TxWriter) {
	var boxed *TxWriter
	if w != nil {
		boxed = &w
	}
	currentTxWriter.Store(boxed)
}

// CurrentTxWriter returns the package-level current TxWriter.
func CurrentTxWriter() TxWriter {
	if p := currentTxWriter.Load(); p != nil {
		return *p
	}
	return nil
}