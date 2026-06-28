package UT

import (
	"sync"
)

// PragmaListener is notified when a PRAGMA value changes (REQ000734).
// Subsystems register listeners to react to configuration changes.
type PragmaListener interface {
	OnPragmaChange(name, value string)
}

var (
	pragmaListeners []PragmaListener
	pragmaMu        sync.RWMutex
)

// RegisterPragmaListener adds a listener for PRAGMA changes.
func RegisterPragmaListener(l PragmaListener) {
	pragmaMu.Lock()
	defer pragmaMu.Unlock()
	pragmaListeners = append(pragmaListeners, l)
}

// UnregisterAllPragmaListeners clears all listeners (for testing).
func UnregisterAllPragmaListeners() {
	pragmaMu.Lock()
	defer pragmaMu.Unlock()
	pragmaListeners = nil
}

// NotifyPragmaChange calls all registered listeners.
func NotifyPragmaChange(name, value string) {
	pragmaMu.RLock()
	defer pragmaMu.RUnlock()
	for _, l := range pragmaListeners {
		l.OnPragmaChange(name, value)
	}
}
