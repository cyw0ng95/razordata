package SY

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	vl "github.com/cyw0ng95/razordata/internal/TXN/VL"
)

// REQ002049: package-level singleton signal channel to prevent
// leaks when InstallSignalHandler is called multiple times.
var (
	sigCh   chan os.Signal
	sigMu   sync.Mutex
)

func InstallSignalHandler(ctx context.Context, e *Engine) (stop func()) {
	sigMu.Lock()
	// Close the old channel if this is a re-install so the previous
	// goroutine wakes up and exits instead of leaking (REQ002049).
	if sigCh != nil {
		signal.Stop(sigCh)
		close(sigCh)
	}
	mySigCh := make(chan os.Signal, 1)
	sigCh = mySigCh
	sigMu.Unlock()
	signal.Notify(mySigCh, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-mySigCh:
			_ = e.Close(context.Background())
		}
	}()
	return func() {
		once.Do(func() {
			sigMu.Lock()
			if sigCh == mySigCh {
				signal.Stop(mySigCh)
				sigCh = nil
			}
			sigMu.Unlock()
			close(done)
		})
	}
}

type ShutdownTimeouts struct {
	WaitForActive  time.Duration
	BackgroundStop time.Duration
}

func defaultShutdownTimeouts() ShutdownTimeouts {
	return ShutdownTimeouts{WaitForActive: 30 * time.Second, BackgroundStop: 5 * time.Second}
}

func (e *Engine) Shutdown(ctx context.Context) error {
	if !e.opened.Load() {
		return nil
	}
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	timeouts := defaultShutdownTimeouts()
	// REQ000687: use configured shutdown timeout
	if e.opts.ShutdownTimeout > 0 {
		timeouts.WaitForActive = e.opts.ShutdownTimeout
	}
	return e.runShutdown(ctx, timeouts)
}

func (e *Engine) runShutdown(ctx context.Context, timeouts ShutdownTimeouts) error {
	if e.log != nil {
		e.log.Info("sy.runShutdown.start")
	}
	start := time.Now()
	stats := AP.ShutdownStats{At: start, UptimeSeconds: int64(time.Since(e.started).Seconds())}
	var firstErr error
	setErr := func(err error, label string) {
		if err == nil {
			return
		}
		if e.log != nil {
			e.log.Warn("sy.shutdown."+label, "err", err)
		}
		if firstErr == nil {
			firstErr = err
			stats.FirstError = label + ": " + err.Error()
		}
	}
	// Phase 1: stop accept.
	if e.log != nil {
		e.log.Info("sy.shutdown.phase1", "msg", "stop accepting requests")
	}
	// REQ000688: emergency shutdown skips Phase 2+3
	if e.opts.EmergencyShutdown {
		if e.log != nil {
			e.log.Warn("sy.shutdown.emergency", "msg", "emergency mode: skipping Phase 2+3")
		}
		goto phase4
	}
	// Phase 2: wait for active tx.
	if e.log != nil {
		e.log.Info("sy.shutdown.phase2", "msg", "wait for active transactions")
	}
	if e.txn != nil {
		waitCtx, waitCancel := context.WithTimeout(ctx, timeouts.WaitForActive)
		err := e.txn.WaitForActive(waitCtx, timeouts.WaitForActive)
		waitCancel()
		if err != nil {
			if e.log != nil {
				e.log.Warn("sy.shutdown.phase2.force_abort", "err", err, "active", e.txn.Stats().Active)
			}
			stats.ForceAborted = e.txn.ForceAbortAll()
		}
	}
	// Phase 3: flush pending writes.
	if e.log != nil {
		e.log.Info("sy.shutdown.phase3", "msg", "flush pending writes")
	}
	// REQ002071: skip flush in MemoryOnly mode — nothing to flush.
	if !e.opts.MemoryOnly {
		if e.eng != nil {
			if err := e.eng.Sync(); err != nil {
				setErr(err, "phase3.engine.sync")
			}
		}
		if e.wr != nil {
			if err := e.wr.Sync(); err != nil {
				setErr(err, "phase3.wal.sync")
			}
		}
		if e.fl != nil {
			if err := e.fl.Sync(); err != nil {
				setErr(err, "phase3.flusher.sync")
			}
		}
	}
phase4:
	// Phase 4: stop background goroutines.
	if e.log != nil {
		e.log.Info("sy.shutdown.phase4", "msg", "stop background goroutines")
	}
	stopCtx, stopCancel := context.WithTimeout(ctx, timeouts.BackgroundStop)
	if e.eng != nil {
		if cm := e.eng.Compaction(); cm != nil {
			if err := cm.Stop(stopCtx); err != nil {
				setErr(err, "phase4.compaction.stop")
				stats.BackgroundStops++
			}
		}
		if fm := e.eng.Flush(); fm != nil {
			if err := fm.Stop(stopCtx); err != nil {
				setErr(err, "phase4.flush.stop")
				stats.BackgroundStops++
			}
		}
	}
	if err := vl.StopGCWithCtx(stopCtx); err != nil {
		setErr(err, "phase4.epoch.stop")
		stats.BackgroundStops++
	}
	stopCancel()
	// Phase 5: close subsystems.
	if e.log != nil {
		e.log.Info("sy.shutdown.phase5", "msg", "close subsystems")
	}
	if err := e.closeBestEffort(); err != nil && firstErr == nil {
		firstErr = err
		stats.FirstError = "phase5: " + err.Error()
	}
	// Phase 6: log final stats.
	stats.DurationMS = time.Since(start).Milliseconds()
	if e.log != nil {
		e.log.Info("sy.shutdown.phase6", "stats", stats)
	}
	e.setLastShutdown(stats)
	return firstErr
}

var lastShutdownMu sync.Mutex

func (e *Engine) setLastShutdown(s AP.ShutdownStats) {
	lastShutdownMu.Lock()
	e.lastShutdown = s
	lastShutdownMu.Unlock()
}
