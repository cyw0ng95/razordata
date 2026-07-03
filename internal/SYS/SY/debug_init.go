//go:build debug

package SY

import (
	"github.com/cyw0ng95/razordata/internal/DBG/DI"
)

func (e *Engine) initDebugger() {
	d, err := di.NewDebugger(di.Options{
		DebugDir:           e.opts.DebugDir,
		EnableDebugSocket:  e.opts.EnableDebugSocket,
		EnableDebugSignals: e.opts.EnableDebugSignals,
		TraceEventCapacity: e.opts.TraceEventCapacity,
		SlowQueryThreshold: e.opts.SlowQueryThreshold,
	})
	if err != nil {
		e.log.Warn("dbg.init", "err", err)
		return
	}
	e.debugger = d
}
