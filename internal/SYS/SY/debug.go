//go:build debug

package SY

import (
	"github.com/cyw0ng95/razordata/internal/DBG/DI"
)

// Debug returns the debugger handle, or nil if not initialized.
func (e *Engine) Debug() di.Debugger {
	return e.debugger.(di.Debugger)
}
