//go:build debug

package di

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/cyw0ng95/razordata/internal/DBG/CT"
	"github.com/cyw0ng95/razordata/internal/DBG/DC"
	"github.com/cyw0ng95/razordata/internal/DBG/PR"
	"github.com/cyw0ng95/razordata/internal/DBG/SK"
	"github.com/cyw0ng95/razordata/internal/DBG/TE"
	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/LOG/HK"
)

// Debugger is the top-level debug handle.
type Debugger interface {
	Socket() string
	SetLogLevel(subsystem string, level slog.Level) error
	EnableTrace(class string, enabled bool) error
	DumpProfile(kind string, dur time.Duration) (string, error)
	Stats() ct.DebugStats
	Close() error
}

// Options configures the debugger.
type Options struct {
	DebugDir           string
	EnableDebugSocket  bool
	EnableDebugSignals bool
	TraceEventCapacity int
	SlowQueryThreshold time.Duration
}

type debugger struct {
	opts    Options
	control *dc.Control
	server  *sk.Server
	ring    *te.Ring
	mu      sync.Once
}

// NewDebugger creates and starts the debugger.
func NewDebugger(opts Options) (Debugger, error) {
	if opts.DebugDir == "" {
		opts.DebugDir = ".debug"
	}
	d := &debugger{opts: opts, control: dc.NewControl()}

	if opts.TraceEventCapacity > 0 {
		d.ring = te.NewRing(opts.TraceEventCapacity)
		hk.DefaultSink = te.NewSink(d.ring)
	}

	hk.DefaultMetricSink = ct.NewMetricHook()

	if opts.EnableDebugSocket {
		d.server = sk.NewServer(opts.DebugDir)
		if err := d.server.Serve(); err != nil {
			return nil, err
		}
	}

	if opts.EnableDebugSignals {
		pr.InstallSignalHandlers()
	}

	pr.SetProfileDir(opts.DebugDir)

	ec.RegisterBuiltinAssertCases()
	ec.SetEngineStatsCallback(func() string {
		snap := ct.GlobalStats.Snapshot()
		keys := make([]string, 0, len(snap))
		for k := range snap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var s string
		for _, k := range keys {
			s += fmt.Sprintf("  %s = %d\n", k, snap[k])
		}
		return s
	})

	return d, nil
}

func (d *debugger) Socket() string {
	if d.server == nil {
		return ""
	}
	return d.opts.DebugDir + "/debug.sock"
}

func (d *debugger) SetLogLevel(subsystem string, level slog.Level) error {
	d.control.SetLogLevel(subsystem, level)
	return nil
}

func (d *debugger) EnableTrace(class string, enabled bool) error {
	d.control.EnableTrace(class, enabled)
	return nil
}

func (d *debugger) DumpProfile(kind string, dur time.Duration) (string, error) {
	return pr.DumpProfile(kind, dur)
}

func (d *debugger) Stats() ct.DebugStats {
	return ct.GlobalStats
}

func (d *debugger) Close() error {
	var err error
	d.mu.Do(func() {
		if d.server != nil {
			err = d.server.Close()
		}
	})
	return err
}
