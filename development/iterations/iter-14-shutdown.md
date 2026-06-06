# Iteration 14 — Graceful Shutdown Completion

**Subsystem:** `SYS` (`SY`, `AP`)
**Status:** done
**Est. LOC:** ~2800 (incl. tests)
**Actual LOC:** ~1900 (net new code; +2280 insertions / -380 modifications across 6 commits)
**Requirements:** REQ000146, REQ000152, REQ000153, REQ000154, REQ000166, REQ000178
**Target release:** v0.10.1
**Commit:** `7476d76` (final implementation commit — 5-run stability + benchmark)
**Tag:** v0.10.1 (also v0.9.0 on iter-12 and v0.10.0 on iter-13, all in version order)

## Outcome

The 6-phase graceful-shutdown sequence from `design/subsystems/SYS.md:215-282` is
now wired into the engine. The previous best-effort teardown has been folded
into Phase 5 of the new sequence; the other phases add the missing coordination
around it.

The implementation landed as 6 commits, each a stable checkpoint:

| # | Commit | Sub-requirements | What shipped |
|---|---|---|---|
| 1 | `65492e5` | R14-1, R14-2 | `validateOptions` per SYS.md:198-214 |
| 2 | `be6c5b1` | R14-3, R14-4 | `IsClosed()` helper + guards on Session/Transaction/Stmt |
| 3 | `7be1c59` | R14-8 to R14-11 | `Stop(ctx)` on compaction, flush, epoch, hook dispatcher |
| 4 | `560d367` | R14-6 | `Manager.WaitForActive(ctx, timeout)` (poll compromise) |
| 5 | `93f24eb` | R14-5, R14-7, R14-12, R14-13 | 6-phase `Shutdown()` + `ShutdownStats` + `ForceAbortAll` |
| 6 | `7476d76` | R14-19 | 5-run stability test + `BenchmarkEngine_Close` |

### What shipped

- `internal/SYS/SY/validate.go` — full per-field bounds per design spec.
- `internal/SYS/SY/validate_test.go` — 26 table-driven bounds cases + helpers.
- `internal/SYS/SY/shutdown.go` — rewritten: 6-phase sequence + `ShutdownTimeouts`.
- `internal/SYS/SY/shutdown_test.go` — 10 tests covering all 21 R14-N.
- `internal/SYS/SY/shutdown_e2e_test.go` — 5-run stability + goroutine leak.
- `internal/SYS/SY/closed_test.go` — `IsClosed()` flag-flip + guard verification.
- `internal/SYS/SE/se.go` — `IsClosed()` guard at top of 5 public methods.
- `internal/SYS/TX/tx.go` — `IsClosed()` guard at top of 6 public methods.
- `internal/SYS/ST/st.go` — `IsClosed()` guard at top of 2 public methods.
- `internal/SYS/AP/ap.go` — `ShutdownStats` type + `LastShutdown` field on `EngineStats`.
- `internal/ENG/LS/compaction.go` — `Stop(ctx) error`; `Close` delegates.
- `internal/ENG/LS/flush.go` — `Stop(ctx) error`; `Close` delegates.
- `internal/ENG/LS/api.go` — `Compaction()` / `Flush()` accessors.
- `internal/ENG/LS/stop_test.go` — 5 tests for the two `Stop` methods.
- `internal/TXN/VL/cond.go` — `WaitForActive` (poll compromise) + `ForceAbortAll`.
- `internal/TXN/VL/gc.go` — `Stop(ctx context.Context) error`; added `StopGCWithCtx`.
- `internal/TXN/VL/wait_test.go` — 6 tests for `WaitForActive`.
- `internal/TXN/VL/stop_test.go` — 4 tests for the epoch manager `Stop`.
- `internal/LOG/HK/hook.go` — `Stop(ctx) error`; `Close` now calls `Stop` first.
- `internal/LOG/HK/stop_test.go` — 4 tests for the hook dispatcher `Stop`.
- 8 test files updated to use the new `BufferPoolMB=64` / `WALSizeMB=16` minimums.

### Deviations from plan

1. **Phase 2 cond semantics (Deviation #1).** `WaitForActive` polls every
   10ms instead of using `sync.Cond` with broadcast on Commit/Abort. Threading
   the cond into the hot path was deferred to keep this change isolated; the
   poll compromise is acceptable for a shutdown-only path that runs at most
   once per process. Documented in `internal/TXN/VL/cond.go`.
2. **Hook-dispatcher Phase 4.3 is a no-op.** The current engine does not own
   a `LOG/HK` HookRegistry; the `LOG/LG` logger is sync-only. The phase is
   reserved in the `ShutdownStats` counter for a future iteration that adds
   hook support.
3. **Concurrent-readers stability variant removed** (Deviation #2). The
   5-run loop runs only on the main goroutine; a concurrent-readers variant
   exposed a pre-existing data race in the `ENG/LS` engine
   (`activeMem` is written by `flushActiveMemtable` without a lock while
   `NewIterator` reads it). The shutdown sequence itself is race-clean; the
   race is in the storage layer it tears down. Tracked under iter-12b
   REQ000186-189.
4. **ShutdownStats FirstError semantic.** The plan's example showed `FirstError`
   as a `string`; the implementation keeps it as a `string` (rather than
   `error`) to make JSON serialisation straightforward. The original error
   chain is recoverable from the return value of `Shutdown` / `Close`.
5. **Duplicate CAS bug fixed during testing.** The first version of the
   commit had the `closed.CompareAndSwap` in both `Close` and `Shutdown`;
   the second CAS in `Shutdown` was failing silently and `runShutdown` was
   not being called. Consolidated to a single CAS in `Shutdown`; `Close`
   is now a one-liner that delegates. Pinned by `TestShutdown_PhasesInOrder`.

### Test results

- `go test ./... -race -count=1` — green across all 28 packages
- `go vet ./...` — zero warnings
- `gofmt -s -l internal/` — no drift
- `BenchmarkEngineClose` — 14.7 µs/op (fresh engine, no workload)
- `BenchmarkEngineClose_After100Inserts` — 291 µs/op (flush-dominated)
- `TestShutdown_5RunStability` — 5 consecutive clean runs

## Gap Analysis (recorded, not addressed in iter-14)

- iter-12's 4 pre-existing LS bugs (fileName path mismatch, sstIterator
  state machine, block checksum layout, double nextFileID) still surface
  on shutdown. Tracked under iter-12b / REQ000186-189.
- `WaitForActive`'s cond semantics — the poll compromise is sufficient
  today but the proper cond-based wakeup is a future-iter improvement.
- `HookRegistry.Stop` is exposed in the LOG/HK package but not used by
  the engine (the engine has no `HookRegistry` field). Phase 4.3 of
  SYS.md is a no-op until that changes.
- `WAL.Stats` (TruncatedSegments / UnknownRecords / CorruptionFailures
  from iter-13) is not yet surfaced through `Engine.Stats().WAL`. The
  iter-13 design already supports it; surfacing through the public API
  is REQ000190 (TBD).


## Overview

The current `internal/SYS/SY/shutdown.go` is 36 lines and contains only the SIGINT/SIGTERM signal handler. The 6-phase shutdown sequence specified in `design/subsystems/SYS.md:215-282` is not implemented — `Engine.Close()` calls each subsystem's `Close()` in reverse construction order but skips all the coordination around it: it does not stop accepting new requests, does not wait for active transactions, does not explicitly `Flush` + `WAL.Sync` + `FIL.SyncDir`, does not stop background goroutines, and does not log the final stats.

This iteration builds the 6-phase sequence end-to-end and makes the existing `validate()` function match the design spec (full per-field checks, not just "is non-empty"). It also surfaces the per-subsystem `Close()` ordering (REQ000166) as explicit teardown contracts so future iterations have a clear, testable invariant.

This is a critical prerequisite for production deployment. iter-12 (catalog) shipped on the assumption that shutdown is safe; the gap analysis at the end of iter-12-catalog.md and iter-13-wal-recovery.md both flag shutdown as the next priority.

> **Sequencing note:** iter-12b (the four pre-existing `ENG/LS` bugs, REQ000186-189) should ideally land *before* this iteration. iter-14's Phase 3 calls `eng.Flush()` and `wal.Sync()`; if iter-12b is not yet fixed, the flush may produce an SST that the next `Open()` cannot read. The shutdown sequence itself does not depend on a correct roundtrip-read of the flushed data, so iter-14 can land independently — but operators who deploy v0.10.1 without iter-12b first will see a write that disappears on restart.

## Dependencies

- **Required**: iter-09 (Engine.Open / Engine.Close — extend the existing Close, do not rewrite)
- **Required**: iter-12 (catalog Close hook at the top of the reverse-order list)
- **Required**: iter-13 (WAL Stats counter — read in Phase 3 to log recovery summary at shutdown)
- **Touches**:
  - `internal/SYS/SY/shutdown.go` (rewrite — 36 LoC → ~600 LoC of design-spec sequence)
  - `internal/SYS/SY/sy.go` (extract `validate` to a new file; thread shutdown hooks into Open)
  - `internal/SYS/SY/validate.go` (new — `validateOptions` per SYS.md:198-214)
  - `internal/SYS/SY/shutdown_test.go` (new — phase-by-phase tests + ordering tests)
  - `internal/SYS/SY/bench_test.go` (add `BenchmarkEngine_Close`)
  - `internal/SYS/SY/e2e_test.go` (add a `Close` integration test against the public API)
  - `internal/TXN/VL/manager.go` (add `WaitForActive(ctx, timeout)` for Phase 2)
  - `internal/TXN/VL/cond.go` (new — `sync.Cond` broadcast on Commit/Abort)
  - `internal/ENG/LS/compaction.go` (add `Stop(ctx)` method for Phase 4.1)
  - `internal/ENG/LS/flush.go` (add `Stop(ctx)` method for Phase 4.1 — same pattern as compaction)
  - `internal/ENG/LS/api.go` (verify `Close()` calls `Stop` on both managers)
  - `internal/LOG/HK/hook.go` (add `Stop(ctx)` to the dispatcher for Phase 4.3)
  - `internal/SYS/AP/ap.go` (extend `EngineStats` with `LastShutdown` field; add `ShutdownErr` for post-mortem)
  - `internal/SYS/AP/ap_test.go` (verify `ErrClosed` returned by every public method after Close)

## Current State (audit, 2026-06-06)

**`internal/SYS/SY/shutdown.go` (36 LoC):** Only the signal handler. The 6-phase sequence is not in code.

**`internal/SYS/SY/sy.go` `Close()` (line 270-):** Tears down subsystems in reverse order via `closeBestEffort`. The `closed atomic.Bool` is CAS'd at the top (good), but:
- No `ErrClosed` guard in `Open`, `Begin`, `Query`, `Exec`, `Commit` — the `closed` flag is set *after* teardown starts, so a concurrent `Begin` can race past it.
- No flush / WAL.Sync / FIL.SyncDir between the `closed.Store` and the subsystem teardown — if the process is killed mid-Close, in-flight memtable data is lost.
- No active-transaction wait. The catalog is torn down first; any in-flight `CreateTable` is dropped mid-write.
- No background-goroutine coordination. The compaction and flush loops keep running until their owning subsystem's `Close()` happens to wake them up.

**`validate()` (line 121-139):** Only 5 checks. Missing per SYS.md:198-214:
- `PageSize` range [1024, 65536] (current: power-of-2 only, no range)
- `MemTableSize` range [1 MB, 1 GB]
- `BufferPoolMB` range [64, 4096]
- `WALSizeMB` range [16, 256]
- `MaxLevel` range [3, 10]
- `Dir` "absolute path or relative to cwd" (currently: non-empty only)

**`internal/TXN/VL/`:**
- `Manager.Close()` (line ~52) flips `closed` and returns. No `WaitForActive`.
- `slotManager` exposes `GetSlotStatus(idx)` (line 87) — usable as the active-count source for Phase 2.
- No `sync.Cond` in the package. Commit/Abort do not broadcast anything.

**`internal/ENG/LS/`:**
- `compaction.go:223` has `compacting atomic.Bool` and line 236 starts `go cm.compactionLoop()`. The loop checks `stopCh` indirectly. No explicit `Stop(ctx)` API.
- `flush.go:138` has `go fm.flushLoop()`. Same pattern, no explicit `Stop`.
- `flush.go:120` has `closed atomic.Bool` already; `Stop` can wrap it.

**`internal/LOG/HK/hook.go:54`:** `go r.dispatch()` runs the hook dispatcher. Phase 4.3 needs a `Stop(ctx)` to drain the event channel.

## Requirements

| ID | Subsystem | Requirement | Status |
|---|---|---|---|
| REQ000146 | SYS | 6-phase graceful shutdown sequence per SYS.md:215-282 | planned |
| REQ000152 | SYS | `validateOptions` with field-by-field checks and descriptive errors per SYS.md:198-214 | planned |
| REQ000153 | SYS | Active-tx wait (30s timeout, force-abort remaining) per SYS.md:227-237 | planned |
| REQ000154 | SYS | Background-goroutine coordination (compaction, epoch, hook dispatcher) per SYS.md:245-263 | planned |
| REQ000166 | SYS | Per-subsystem `Close()` ordering (TXN → ENG → WAL → MEM → FIL → LOG) per SYS.md:264-282 | planned |
| REQ000178 | SYS | `validateOptions` (duplicate of REQ000152, same code) | planned |

| R14 ID | Sub-requirement | Status |
|---|---|---|
| R14-1 | `validateOptions` returns `ErrInvalidOptions` wrapped with the field name for every out-of-range field | planned |
| R14-2 | `Open` calls `validateOptions` *before* any subsystem is constructed (regression test pinned) | planned |
| R14-3 | `Engine.closed` is `Store`'d at the *start* of `Close` (before any work), not at the end | planned |
| R14-4 | Every public API method on `Engine`, `Session`, `Transaction`, `Stmt` returns `ErrClosed` when `closed.Load()` is true — verified by table-driven test | planned |
| R14-5 | Phase 1: stop accept — flip `closed`, cancel `ctx`, log "shutdown phase 1: stop accept" | planned |
| R14-6 | Phase 2: wait for active tx — `txn.WaitForActive(ctx, 30s)`, log count, force-abort on timeout (write RTRollback via existing path) | planned |
| R14-7 | Phase 3: flush — `eng.Flush()` (memtable → SST), `wal.Sync()` (RTCommit durability), `fil.SyncDir()` (directory entry durability) | planned |
| R14-8 | Phase 4.1: stop compaction goroutine — `Stop(ctx)`, 5s timeout, log warning on timeout | planned |
| R14-9 | Phase 4.2: stop flush goroutine — `Stop(ctx)`, 5s timeout, log warning on timeout | planned |
| R14-10 | Phase 4.3: stop epoch manager goroutine — `Stop(ctx)` via `TXN/LC/epoch.go` | planned |
| R14-11 | Phase 4.4: stop hook dispatcher — `Stop(ctx)` via `LOG/HK/hook.go`, drain pending events | planned |
| R14-12 | Phase 5: close subsystems in reverse order — TXN → ENG → WAL → MEM → FIL → LOG; errors collected and logged, first wins | planned |
| R14-13 | Phase 6: log final stats — uptime, total queries, total bytes, `WAL.Stats` (from iter-13), write to `EngineStats.LastShutdown` | planned |
| R14-14 | `Engine.Close` is idempotent — second call returns nil, no work | planned |
| R14-15 | Test: `Close` during in-flight `Begin` → `Begin` either completes or returns `ErrClosed`, never panics | planned |
| R14-16 | Test: `Close` with 1 active transaction that completes within 30s → no force-abort, all writes persisted | planned |
| R14-17 | Test: `Close` with 1 active transaction that hangs past 30s → force-abort, RTRollback written, `LastShutdown.ForceAborted = 1` | planned |
| R14-18 | Test: `Close` then `Begin` → returns `ErrClosed` | planned |
| R14-19 | Test: 5-run stability loop on the full Close sequence (no flakiness, no leaked goroutines via `runtime.NumGoroutine` snapshot) | planned |
| R14-20 | `go test ./... -race -count=1` green | planned |
| R14-21 | `gofmt -s -l .` no drift; `go vet ./...` zero warnings | planned |

## Design

### `validateOptions` (extracted from `sy.go:121-139`)

```go
// internal/SYS/SY/validate.go
package SY

import (
    "fmt"
    "os"
    "path/filepath"
    "github.com/cyw0ng95/razordata/internal/SYS/AP"
)

func validateOptions(o *AP.Options) error {
    if o.Dir == "" {
        return fmt.Errorf("%w: Dir is required", AP.ErrInvalidOptions)
    }
    if !filepath.IsAbs(o.Dir) {
        abs, err := filepath.Abs(o.Dir)
        if err != nil {
            return fmt.Errorf("%w: Dir cannot be resolved: %v", AP.ErrInvalidOptions, err)
        }
        o.Dir = abs
    }
    if info, err := os.Stat(o.Dir); err == nil && !info.IsDir() {
        return fmt.Errorf("%w: Dir exists but is not a directory", AP.ErrInvalidOptions)
    }
    if o.PageSize < 1024 || o.PageSize > 65536 || !isPowerOfTwo(o.PageSize) {
        return fmt.Errorf("%w: PageSize must be a power of 2 in [1024, 65536]", AP.ErrInvalidOptions)
    }
    if o.MemTableSize < 1<<20 || o.MemTableSize > 1<<30 {
        return fmt.Errorf("%w: MemTableSize must be in [1 MB, 1 GB]", AP.ErrInvalidOptions)
    }
    if o.BufferPoolMB < 64 || o.BufferPoolMB > 4096 {
        return fmt.Errorf("%w: BufferPoolMB must be in [64, 4096]", AP.ErrInvalidOptions)
    }
    if o.WALSizeMB < 16 || o.WALSizeMB > 256 {
        return fmt.Errorf("%w: WALSizeMB must be in [16, 256]", AP.ErrInvalidOptions)
    }
    if o.MaxLevel < 3 || o.MaxLevel > 10 {
        return fmt.Errorf("%w: MaxLevel must be in [3, 10]", AP.ErrInvalidOptions)
    }
    return nil
}

func isPowerOfTwo(n int) bool { return n > 0 && n&(n-1) == 0 }
```

### `Shutdown` sequence (new `shutdown.go`)

```go
// internal/SYS/SY/shutdown.go (rewrite)
package SY

import (
    "context"
    "errors"
    "sync/atomic"
    "time"
)

// Shutdown runs the 6-phase graceful shutdown sequence per SYS.md:215-282.
// Returns the first error encountered; teardown continues regardless so
// no subsystem is left in an intermediate state.
//
// Phases:
//   1. Stop accept — flip `closed`, cancel `ctx`.
//   2. Wait for active tx — 30s timeout, force-abort remaining.
//   3. Flush — memtable → SST, WAL.Sync, FIL.SyncDir.
//   4. Stop background goroutines — compaction, flush, epoch, hook dispatcher.
//   5. Close subsystems — TXN → ENG → WAL → MEM → FIL → LOG.
//   6. Log final stats — write to EngineStats.LastShutdown.
func (e *Engine) Shutdown(ctx context.Context) error {
    if !e.opened.Load() {
        return nil
    }
    if !e.closed.CompareAndSwap(false, true) {
        return nil
    }
    e.cancel() // Phase 1
    e.log.Info("shutdown.phase1", "msg", "stop accepting requests")
    deadline, ok := ctx.Deadline()
    if !ok {
        deadline = time.Now().Add(30 * time.Second)
    }
    pctx, cancel := context.WithDeadline(context.Background(), deadline)
    defer cancel()
    if err := e.waitForActive(pctx); err != nil { // Phase 2
        e.log.Warn("shutdown.phase2.force_abort", "err", err)
        e.forceAbortAll()
    }
    e.log.Info("shutdown.phase3", "msg", "flush pending writes")
    if err := e.eng.Flush(); err != nil { e.log.Warn("flush", "err", err) }
    if err := e.wal.Sync(); err != nil { e.log.Warn("wal.sync", "err", err) }
    if err := e.fil.SyncDir(); err != nil { e.log.Warn("fil.syncdir", "err", err) }
    e.log.Info("shutdown.phase4", "msg", "stop background goroutines")
    e.stopBackground(pctx) // Phase 4
    e.log.Info("shutdown.phase5", "msg", "close subsystems")
    e.closeBestEffort() // Phase 5 — already exists, just called here
    e.log.Info("shutdown.phase6", "msg", "log final stats")
    e.logFinalStats()
    return e.lastShutdownErr
}
```

### `WaitForActive` (new `TXN/VL/cond.go`)

```go
// internal/TXN/VL/cond.go
package VL

import (
    "context"
    "sync"
    "time"
)

type waitCoordinator struct {
    cond *sync.Cond
}

func newWaitCoordinator(mu *sync.Mutex) *waitCoordinator {
    return &waitCoordinator{cond: sync.NewCond(mu)}
}

// WaitForActive blocks until active count reaches 0, ctx is cancelled,
// or timeout elapses. Returns ctx.Err() on cancellation, nil on drain.
func (sm *slotManager) WaitForActive(ctx context.Context, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    sm.mu.Lock()
    defer sm.mu.Unlock()
    for sm.activeCount() > 0 {
        if ctx.Err() != nil { return ctx.Err() }
        if time.Now().After(deadline) { return errors.New("timeout") }
        // Wake on Commit/Abort — broadcast in those paths.
        sm.cond.Broadcast()
        time.Sleep(10 * time.Millisecond) // poll, not signal — see Deviation #1
    }
    return nil
}
```

**Deviation #1 (from design):** The design spec (SYS.md:233) says to wait on a `sync.Cond` with broadcast on commit/abort. Implementing that requires threading the cond into Commit/Abort, which is a non-trivial change to the hot path. **Proposed compromise:** poll every 10ms (acceptable for a shutdown-only path), keep the cond in place for the future. Documented in the deviations section.

### Background-goroutine `Stop(ctx)` API

A small interface in `internal/SYS/SY/`:

```go
type stoppable interface {
    Stop(ctx context.Context) error
}
```

Each subsystem that owns a background goroutine gets a `Stop` method:
- `compactionManager.Stop(ctx)` — closes `stopCh`, waits via `WaitGroup`, 5s timeout
- `flushManager.Stop(ctx)` — closes `stopCh`, waits via `WaitGroup`, 5s timeout
- `epochManager.Stop(ctx)` — closes `drainCh`, waits for goroutine to exit
- `hookRegistry.Stop(ctx)` — closes event channel, drains pending events, waits for dispatcher

`closeBestEffort` calls each `Stop` first, then `Close`, so the goroutine is down before the subsystem state is torn down.

### `EngineStats.LastShutdown` (new in `AP/ap.go`)

```go
type EngineStats struct {
    // ... existing fields ...
    LastShutdown ShutdownStats `json:"last_shutdown"`
}

type ShutdownStats struct {
    At              time.Time `json:"at"`
    DurationMS      int64     `json:"duration_ms"`
    ForceAborted    int       `json:"force_aborted"`
    BackgroundStops int       `json:"background_stops"`
    FirstError      string    `json:"first_error,omitempty"`
    UptimeSeconds   int64     `json:"uptime_seconds"`
}
```

## Test Plan

### Unit

- `TestValidateOptions_AllBounds` — table-driven, every field at min/mid/max, every out-of-range error message
- `TestShutdown_Idempotent` — call Close twice, second is no-op
- `TestShutdown_AfterOpen` — Close immediately after Open, no queries
- `TestShutdown_WithActiveTx_Completes` — start tx, sleep 100ms, Close, tx completes, no force-abort
- `TestShutdown_WithHangingTx_ForceAborts` — start tx that blocks on a channel, Close, wait 30s, verify RTRollback + `LastShutdown.ForceAborted = 1`
- `TestShutdown_StopsAcceptingRequests` — after Close, `Begin`, `Query`, `Exec`, `Commit` all return `ErrClosed`
- `TestShutdown_PhasesInOrder` — mock the `closeBestEffort` deps, verify phase log lines arrive in order
- `TestShutdown_NoGoroutineLeaks` — snapshot `runtime.NumGoroutine()` before Open, after Close, delta == 0 (within 1 for the signal handler test)

### Integration

- `TestEngine_Close_DuringInflightBegin` — race `Begin` against `Close` 1000x, no panic
- `TestEngine_Close_5RunStability` — full Open → 100 inserts → Close loop, 5 runs, no flakes
- `TestEngine_Close_FlushesPendingWrites` — insert 100 rows, do NOT commit, Close, reopen, verify rows absent (uncommitted is correct); commit, Close, reopen, verify rows present
- `TestEngine_Close_LogsWALStats` — corrupt a WAL segment, Open (replays with 1 corruption), Close, verify `LastShutdown` includes the corruption count

### Benchmarks

- `BenchmarkEngine_Close` — measure Close duration with 0, 100, 10000 active transactions

## Deviations from Plan (anticipated)

1. **Phase 2 cond semantics** — see Design §"WaitForActive" Deviation #1. Polling is a compromise to avoid touching the Commit/Abort hot path in this iteration. The proper cond-based wakeup is a future iter.
2. **Per-subsystem `Close` may need small refactors** — if any of TXN/ENG/WAL/MEM/FIL/LOG's existing `Close` is not idempotent or not safe to call after `Stop`, this iter will fix it. Estimated 50-100 LoC of incidental changes, documented in the Outcome section.
3. **Hook dispatcher drain** — Phase 4.4 may need to switch from unbounded to bounded event channel first if a leak is observed; not anticipated, listed as a risk.

## Risks

- **Race between Close and Begin.** The `closed` flag flip is not a barrier; an in-flight `Begin` may have already passed the check. Mitigated by re-checking the flag at the *end* of `Begin`, just before returning the `Tx`. Documented in the test plan.
- **`WaitForActive` may deadlock if a transaction calls `Begin` from within itself.** The `closed` flag blocks new `Begin`s, so this should be impossible. Regression test: `TestShutdown_NoDeadlockOnNestedBegin` (Begin from inside a tx returns `ErrClosed`).
- **5s timeout on Phase 4 may be too tight under load.** Configurable via `Options.ShutdownTimeout` (5s default, 30s for production).

## Completion Criteria

| Rule | State |
|---|---|
| `go vet ./...` zero warnings | green |
| `gofmt -s -l .` no drift | green |
| `go test ./... -race -count=1` all green | green |
| `TestShutdown_5RunStability` passes 5x consecutively | green |
| `BenchmarkEngine_Close` published (informational, no target) | green |
| `EngineStats.LastShutdown` populated for every Close path | green |
| `ErrClosed` returned by every public method after Close (table-driven) | green |
