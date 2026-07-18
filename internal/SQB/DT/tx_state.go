package DT

import (
	"sync"
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

// walAutocheckpointPages controls how many WAL pages may accumulate
// between automatic checkpoints. Default 1000 (SQLite compat).
// Setting 0 disables automatic checkpointing. REQ001300.
var walAutocheckpointPages atomic.Int64

// busyTimeoutMs is the millisecond budget for lock acquisition when
// busy_handler is not set. Default 0 (no wait). REQ001301.
var busyTimeoutMs atomic.Int64

// busyHandlerName is the active busy-handler callback name. Empty
// string means "no callback registered"; busy_timeout applies instead.
// REQ001302.
var busyHandlerNameStr atomic.Value // string

// busyHandlerRegistry holds user-registered Go callbacks indexed by
// name. The SQL PRAGMA accepts the name and lock acquisition looks
// up the callback here. REQ001302.
var busyHandlerRegistry sync.Map // map[string]BusyHandlerFunc

// BusyHandlerFunc is the signature of a user-supplied busy handler.
// attempt is the 1-based retry index. Return true to retry, false to
// surface ErrBusy. REQ001302.
type BusyHandlerFunc func(attempt int) bool

// autoCompactMode controls whether LSM auto-compaction is triggered
// on commit. Values: 0=none, 1=incremental, 2=full. Default 0. REQ001304.
var autoCompactMode atomic.Int32

// autoCompactThreshold stores the garbage ratio threshold * 1000 to
// avoid atomic.Float64 (unsupported in Go 1.26). Default 300 (= 0.3). REQ001304.
var autoCompactThreshold atomic.Int64

func init() {
	foreignKeysEnabled.Store(true)
	walAutocheckpointPages.Store(1000)
	busyHandlerNameStr.Store("")
	autoCompactMode.Store(0)
	autoCompactThreshold.Store(300) // 0.3 * 1000
}

// SetForeignKeysEnabled stores the FK enforcement toggle.
func SetForeignKeysEnabled(v bool) {
	foreignKeysEnabled.Store(v)
}

// IsForeignKeysEnabled returns the current FK enforcement toggle.
func IsForeignKeysEnabled() bool {
	return foreignKeysEnabled.Load()
}

// SetWalAutocheckpoint stores the autocheckpoint page threshold.
// A value of 0 disables automatic checkpointing. REQ001300.
func SetWalAutocheckpoint(pages int64) {
	walAutocheckpointPages.Store(pages)
}

// GetWalAutocheckpoint returns the current autocheckpoint threshold.
// REQ001300.
func GetWalAutocheckpoint() int64 {
	return walAutocheckpointPages.Load()
}

// SetBusyTimeout stores the busy-timeout budget in milliseconds.
// A value of 0 means "do not wait" (immediate ErrBusy). REQ001301.
func SetBusyTimeout(ms int64) {
	busyTimeoutMs.Store(ms)
}

// GetBusyTimeout returns the current busy-timeout budget in ms.
// REQ001301.
func GetBusyTimeout() int64 {
	return busyTimeoutMs.Load()
}

// SetBusyHandler registers a busy-handler callback under a name and
// stores the name as the active handler. Empty name clears the
// active handler without unregistering callbacks. REQ001302.
func SetBusyHandler(name string, fn BusyHandlerFunc) {
	if name == "" {
		busyHandlerNameStr.Store("")
		return
	}
	if fn != nil {
		busyHandlerRegistry.Store(name, fn)
	}
	busyHandlerNameStr.Store(name)
}

// GetBusyHandler returns the active handler name and the callback
// (nil if no callback is registered under that name). REQ001302.
func GetBusyHandler() (string, BusyHandlerFunc) {
	name, _ := busyHandlerNameStr.Load().(string)
	if name == "" {
		return "", nil
	}
	if v, ok := busyHandlerRegistry.Load(name); ok {
		return name, v.(BusyHandlerFunc)
	}
	return name, nil
}

// UnregisterBusyHandler removes a callback from the registry. Does
// not clear the active name (callers can still resolve it). REQ001302.
func UnregisterBusyHandler(name string) {
	busyHandlerRegistry.Delete(name)
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

// IsInTransaction returns true if a TxWriter is currently active,
// meaning a transaction is in progress. REQ001307.
func IsInTransaction() bool {
	return CurrentTxWriter() != nil
}

// AutoCompactMode represents the auto_compact PRAGMA mode. REQ001304.
type AutoCompactMode int32

const (
	AutoCompactNone       AutoCompactMode = 0
	AutoCompactIncremental AutoCompactMode = 1
	AutoCompactFull        AutoCompactMode = 2
)

// SetAutoCompactMode stores the auto-compaction mode. REQ001304.
func SetAutoCompactMode(m AutoCompactMode) {
	autoCompactMode.Store(int32(m))
}

// GetAutoCompactMode returns the current auto-compaction mode. REQ001304.
func GetAutoCompactMode() AutoCompactMode {
	return AutoCompactMode(autoCompactMode.Load())
}

// SetAutoCompactThreshold stores the garbage ratio threshold (0.0–1.0). REQ001304.
func SetAutoCompactThreshold(t float64) {
	autoCompactThreshold.Store(int64(t * 1000))
}

// GetAutoCompactThreshold returns the current garbage ratio threshold. REQ001304.
func GetAutoCompactThreshold() float64 {
	return float64(autoCompactThreshold.Load()) / 1000.0
}