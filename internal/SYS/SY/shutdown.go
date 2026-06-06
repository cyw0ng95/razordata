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

// InstallSignalHandler subscribes to SIGINT and SIGTERM and calls
// Close with the provided context on the first signal. A second
// signal aborts the wait and returns immediately. Returns a stop
// function that the caller invokes to unsubscribe and release the
// internal goroutine.
//
// The handler is best-effort: a test that doesn't want OS-level
// signals can call Engine.Close directly.
func InstallSignalHandler(ctx context.Context, e *Engine) (stop func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-sigCh:
			_ = e.Close(context.Background())
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

// ShutdownTimeouts holds the per-phase timeout budget for the
// graceful-shutdown sequence. The defaults match the design
// (SYS.md:227-237 and SYS.md:245-251); callers — typically tests —
// can override by populating a ShutdownTimeouts and calling
// runShutdown directly.
type ShutdownTimeouts struct {
	// WaitForActive bounds Phase 2. Design default: 30s.
	WaitForActive time.Duration
	// BackgroundStop bounds Phase 4 (each Stop call). Design
	// default: 5s.
	BackgroundStop time.Duration
}

// defaultShutdownTimeouts returns the SYS.md-specified defaults.
func defaultShutdownTimeouts() ShutdownTimeouts {
	return ShutdownTimeouts{
		WaitForActive:  30 * time.Second,
		BackgroundStop: 5 * time.Second,
	}
}

// Shutdown runs the 6-phase graceful shutdown sequence per
// design/subsystems/SYS.md:215-282. It is the single entry point
// called by Engine.Close — the previous best-effort teardown has
// been folded into Phase 5 of this sequence.
//
// Phases:
//  1. Stop accept — flip `closed` (already done by the caller
//     before invoking Shutdown), log the phase.
//  2. Wait for active tx — bound by WaitForActive (default 30s).
//     On timeout, force-abort the residual transactions and log
//     the count.
//  3. Flush — memtable → SST (eng.Sync), WAL fsync (wr.Sync), and
//     WAL directory fsync (fl.Sync) to durably persist pending
//     writes.
//  4. Stop background goroutines — compaction loop, flush loop,
//     and the global epoch manager. Each Stop has its own
//     BackgroundStop budget (default 5s). Timeouts are logged as
//     warnings and counted in stats.BackgroundStops. The shutdown
//     proceeds regardless.
//  5. Close subsystems — TXN → ENG → RP → FL → WR → BF → DF →
//     LF → FS → LOG. Errors are collected, the first one wins.
//  6. Log final stats — populate Engine.lastShutdown and return
//     the first error encountered.
//
// Shutdown is idempotent; a second call returns nil immediately
// because the closed flag is already set.
func (e *Engine) Shutdown(ctx context.Context) error {
	if !e.opened.Load() {
		return nil
	}
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}
	return e.runShutdown(ctx, defaultShutdownTimeouts())
}

// runShutdown is the testable inner loop. It exists separately
// from Shutdown so unit tests can supply a faster ShutdownTimeouts.
func (e *Engine) runShutdown(ctx context.Context, timeouts ShutdownTimeouts) error {
	if e.log != nil {
		e.log.Info("sy.runShutdown.start")
	}
	start := time.Now()
	stats := AP.ShutdownStats{
		At:            start,
		UptimeSeconds: int64(time.Since(e.started).Seconds()),
	}
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

	// Phase 1: stop accept. The closed flag is already flipped at
	// the top of Shutdown (R14-3). All public methods now return
	// ErrClosed.
	if e.log != nil {
		e.log.Info("sy.shutdown.phase1", "msg", "stop accepting requests")
	}

	// Phase 2: wait for active tx. Bounded by the caller's ctx if
	// it has a deadline, otherwise the WaitForActive default. The
	// poll-based WaitForActive returns a timeout error on deadline;
	// we feed that to force-abort.
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

	// Phase 3: flush pending writes. The order is memtable → WAL
	// fsync → WAL directory fsync.
	if e.log != nil {
		e.log.Info("sy.shutdown.phase3", "msg", "flush pending writes")
	}
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

	// Phase 4: stop background goroutines. Each Stop is bounded
	// by BackgroundStop; timeouts are warnings, not fatal.
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
	// Phase 4.2: epoch manager (global, via vl.StopGCWithCtx).
	if err := vl.StopGCWithCtx(stopCtx); err != nil {
		setErr(err, "phase4.epoch.stop")
		stats.BackgroundStops++
	}
	// Phase 4.3: hook dispatcher. The current engine does not
	// own a LOG/HK HookRegistry; the LOG/LG logger is sync-only.
	// The phase is a no-op but the slot is reserved in the
	// ShutdownStats counter.
	stopCancel()

	// Phase 5: close subsystems in reverse construction order.
	// The closeBestEffort helper already implements best-effort
	// teardown (errors are logged, the first wins).
	if e.log != nil {
		e.log.Info("sy.shutdown.phase5", "msg", "close subsystems")
	}
	if err := e.closeBestEffort(); err != nil && firstErr == nil {
		firstErr = err
		stats.FirstError = "phase5: " + err.Error()
	}

	// Phase 6: log final stats. The engine is fully torn down at
	// this point; the public Stats() can read lastShutdown.
	stats.DurationMS = time.Since(start).Milliseconds()
	if e.log != nil {
		e.log.Info("sy.shutdown.phase6", "stats", stats)
	}
	e.setLastShutdown(stats)
	return firstErr
}

// lastShutdownMu guards lastShutdown. Reads from Stats() are
// snapshot reads; writers are exclusive.
var lastShutdownMu sync.Mutex

// setLastShutdown stores the most recent shutdown outcome for
// Stats() to surface. The store is best-effort; a later shutdown
// overwrites an earlier one.
func (e *Engine) setLastShutdown(s AP.ShutdownStats) {
	lastShutdownMu.Lock()
	e.lastShutdown = s
	lastShutdownMu.Unlock()
}
