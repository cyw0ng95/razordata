# Iteration 3 — WAL (Write-Ahead Log)

**Subsystem:** `WAL`
**Status:** complete
**Est. LOC:** ~2,200 (5 critical clarifications added after design review)

## Overview

Sequential durability path. Append-only segments (64 MB). fsync on commit. Replay on startup. Depends on FIL and MEM.

## Dependencies

- Required: `FIL`, `MEM`
- Consumed (concrete types, no interfaces defined):
  - `*lf.SegmentManager` — segment file management (`FIL/LF/lf.go`)
  - `*df.BlockDevice` — block I/O with checksum (`FIL/DF/df.go`)
  - `sp.SyncPool` — pre-allocated write buffers (`MEM/SP/sp.go`); the `syncPool` implementation is unexported, so the interface is the only public handle
  - `lg.Logger` — structured logging (`LOG/LG/lg.go`)

## Design Alignment

Directory structure matches `design/subsystems/WAL.md`:
```
internal/WAL/
├── WR/               # Writer cluster
│   ├── wr.go         # Writer, segment rotation, LSN allocation
│   └── encode.go     # log record encoding, varint helpers
├── FL/               # Flusher cluster
│   ├── fl.go         # Flusher, fsync, batch sync
│   └── lsn.go        # LSN counter, encode/decode
└── RP/               # Replay cluster
    ├── rp.go         # Replayer, segment scanning, replay
    ├── rp_test.go
    └── checkpoint.go # checkpoint encoding/decoding
```

### Gap Analysis vs FIL + MEM

#### Resolved: LF cluster already exists

`FIL/LF/lf.go` already provides `*lf.SegmentManager` with `CreateSegment(n)` / `GetSegment(n)` / `Truncate(n, size)` / `Close()`. WAL does NOT reimplement segment file management. It composes `*lf.SegmentManager` directly.

#### Resolved: SegmentManager missing ListSegments

`lf.SegmentManager` has no method to enumerate segments for replay. WAL needs segments scanned in numeric order. **Fix:** add `ListSegments() ([]uint64, error)` to `FIL/LF/lf.go` — reads `root/wal/` directory, parses `wal.%03d` filenames, returns sorted uint64 slice.

#### Resolved: BufferPool missing Upsert

`bf.BufferPool` has `Get/Pin/Unpin/SetCapacity/Stats/Close/Warm`. WAL's `onData(blockID, data)` replay callback needs to inject a page into the cache without reading from disk. `Get` always hits disk, which doesn't support replay injection. **Fix:** add `Upsert(page *Page) error` to the `BufferPool` interface and implement it in `bp`.

#### Resolved: No interface wrappers needed

`design/subsystems/WAL.md` says "consumed interfaces: `FileManager`, `BlockDevice`". Neither interface type exists in the codebase. WAL uses concrete types directly: `*lf.SegmentManager`, `*df.BlockDevice`, `sp.SyncPool` (the unexported `*sp.syncPool` implementation is held via the interface). No interface wrappers needed for v1.

#### Resolved: SyncDir with subdirectory path

`FIL/FS.SyncDir("wal")` resolves relative to the database root → `root/wal` ✓. WAL calls `fm.SyncDir("wal")` after fsync to ensure segment directory entries are durable.

## Requirements

Requirements are grouped by implementation order: **Foundation** (types/interfaces/API) → **Upstream Fixes** (Phase 0 cross-subsystem changes) → **Core** (data structures + behaviors) → **Reliability** (concurrency/error/edge cases) → **Quality** (integration tests, CI, benchmarks).

| ID | Requirement | Status | Notes |
|---|---|---|---|
| **Foundation: types & interfaces** | | | |
| R01 | `RecordType` enum: RTData=0, RTCommit=1, RTRollback=2, RTCheckpoint=3, RTMerge=4 | done | |
| R02 | Log record encoding: `[length:varint][txnID:varint][type:uint8][payload:blob]` | done | |
| R14 | `Checkpoint` struct: LSN, CatalogRootPtr, ManifestChecksum, ActiveTXNs ([]uint64) | done | |
| R03 | `Writer` interface: `Append(batch *WriteBatch) (lsn uint64, err error)`, `Sync`, `Close` | done | |
| R08 | `Flusher` interface: `Sync/BatchSync/SyncDir` | done | |
| R11 | `Replayer` interface: `Replay() error`, `LastCheckpoint() (*Checkpoint, error)` | done | |
| R15 | `replayer` struct: dir, sm, cb (Callbacks), lastCheckpoint — internal state of RP | done | |
| R35 | `WR.New`, `FL.New`, `RP.New` constructor signatures (see Constructor Signatures section) | done | |
| R36 | `Replayer` constructed with `Callbacks` struct (`OnData`/`OnCommit`/`OnRollback`); zero-value hooks are no-ops | done | |
| **Upstream fixes (Phase 0)** | | | |
| R33 | `sp.WALBufSize = 256*1024` pool — third sync pool in `MEM/SP` for WAL write buffers | done | Phase 0C |
| R34 | `BufferPool.Upsert` semantics: reject non-BlockSize data, overwrite existing slot, count against capacity, no checksum, no pin | done | Phase 0B |
| **Core: data structures** | | | |
| R04 | `lsnCounter`: atomic.Uint64, `lsn = segmentNumber * SegSize + offset` (uint64 end-to-end) | done | |
| R05 | `logSegment`: number, fd, path, writeOff, buf (pre-allocated 256 KB from MEM/SP) | done | buf from sp |
| **Core: behaviors** | | | |
| R37 | Flush trigger policy: `Append` writes to 256 KB in-memory buffer; flush on overflow, `Sync`, or segment rotation | done | |
| R06 | Segment rotation: close current when `writeOff >= 64 MB`, create new segment | done | uses *lf.SegmentManager |
| R07 | Sequential append — no reads in hot path | done | |
| R09 | `fsync` on segment FD, update `synced` LSN | done | |
| R10 | `SyncDir` after fsync to ensure directory entries are durable | done | uses *fs.FileManager |
| R12 | Segment scanning: iterate `wal.000`, `wal.001`, ... in numeric order | done | uses LF.ListSegments (Phase 0A) |
| R13 | Find last `RTCheckpoint`, rewind to its LSN | done | |
| R16 | Replay RTData: apply block image to buffer pool via `Upsert` | done | stub in iter-03 |
| R17 | Replay RTCommit: mark transaction as committed | done | stub in iter-03 |
| R18 | Replay RTRollback: discard transaction's write set | done | stub in iter-03 |
| R19 | Truncate clean segments before checkpoint after replay | done | uses LF.Truncate |
| **Reliability** | | | |
| R21 | Goroutine-safe — all public API methods safe for concurrent use | done | |
| R22 | Idempotent close — Close/Sync safe to call multiple times | done | |
| R23 | fsync error propagation — failed fsync returned as error, not swallowed | done | |
| R24 | No allocations in hot path — pre-allocate 256 KB write buffer in WR | done | per AGENTS.md perf rules |
| R25 | Zero-fill on buffer reuse — pre-allocated buf cleared before each use | done | |
| R26 | Partial record recovery — truncated record at end of segment handled gracefully | done | |
| R27 | Replay in LSN order — replay order by LSN, not segment filename | done | |
| R28 | Clean shutdown — no WAL segments = replay is a no-op | done | |
| R29 | Segment truncation safe — truncate before checkpoint doesn't lose committed data | done | |
| **Quality** | | | |
| R20 | Simulated crash test: write data, kill process, restart — committed data present | done | TestCrashSimulated |
| R30 | `go vet ./internal/WAL/...` zero warnings | done | |
| R31 | `go test ./internal/WAL/... -race -count=1` all green | done | |
| R32 | Benchmark: sequential append throughput | done | wal_bench.go |

## Implementation

### Phase 0: Upstream Fixes

#### A. `LF.ListSegments` (`FIL/LF/lf.go`)

Add to `SegmentManager`:
```go
func (sm *SegmentManager) ListSegments() ([]uint64, error)
```
- Read `root/wal/` directory entries.
- Filter names matching `wal.%03d` (3-digit zero-padded numeric suffix).
- Parse numeric suffix as uint64.
- Return sorted ascending slice.
- Return nil/empty if no segments.

#### B. `BufferPool.Upsert` (`MEM/BF/bf.go`)

Add to `BufferPool` interface:
```go
Upsert(page *Page) error
```

Implement in `bp` with the following semantics (added after design review):
- `page.ID` is the blockID; `page.Data` is a borrowed buffer (not copied, not retained by BF).
- If `len(page.Data) != BlockSize` (DataLen is the *usable* bytes; the slot stores a full BlockSize buffer), reject with `bf.ErrInvalidBlockID` rather than padding — padding would mask caller bugs.
- If a slot for `page.ID` already exists, overwrite the `data` field and clear `loading`. Do not call `sp.Put` on the old data (the previous occupant owns it; this is a re-injection, not a release).
- Counts against `used`; if `used >= capacity`, evict one slot before inserting (same two-pass policy as `Get`).
- Does NOT verify checksum — caller (WAL replayer) has already verified the page image against its own CRC32.
- Does NOT pin. The recovered page is a one-shot injection; subsequent eviction is allowed.

#### C. WAL write-buffer pool (`MEM/SP/sp.go`)

The 256 KB WAL write buffer must be pooled — `sp.Get(256*1024)` currently falls through to `make([]byte, 256*1024)`, defeating the no-allocation goal.

Add to `SP/sp.go`:
```go
const WALBufSize = 256 * 1024
```

Add a third pool to `syncPool`:
- `walPool sync.Pool` with `New: func() any { return make([]byte, WALBufSize) }`
- Extend `Get(size)`: if `size <= WALBufSize`, check `walPool` first.
- Extend `Put(buf)`: if `cap(buf) == WALBufSize`, return to `walPool` with `buf[:WALBufSize]`.

#### D. `BlockID` semantics

`LogRecord.BlockID` and `Page.ID` are the same identifier: a block number in the SST/manifest block space (1, 2, 3, ...). It is **not** a segment number or a segment-internal offset.

### Phase 1: Writer (`WR/wr.go` + `encode.go`)

1. `RecordType` enum, `LogRecord`, `WriteBatch` types
2. `encodeVarint`/`decodeVarint` helpers
3. `encodeRecord`: length + txnID + type + payload
4. Payload encoding:
   - `RTData`: `[blockID:8][checksum:4][data:varint]`
   - `RTCommit`: `[commitTS:8]`
   - `RTRollback`: `[]` (empty)
   - `RTCheckpoint`: `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNCount:varint][activeTXNs:varint...]`
5. `lsnCounter`: atomic.Uint64, SegSize constant = 64 MB (LSN type is `uint64` end-to-end; see R04 below)
6. `logSegment`: wraps `*lf.SegmentManager` + pre-allocated 256 KB buffer from `*sp.syncPool` (via the new `WALBufSize` pool added in Phase 0C)
7. `Writer.Append`: assign LSN, encode into pre-allocated buffer, pwrite, update writeOff
8. Segment rotation: when writeOff >= SegSize, close segment, call `sm.CreateSegment(n+1)`, swap current segment

### Constructor Signatures (added after design review)

```go
// WR — Writer
func New(dir string, sm *lf.SegmentManager, sp sp.SyncPool, log lg.Logger) (Writer, error)

// FL — Flusher
func New(dir string, sm *lf.SegmentManager, fm *fs.FileManager, log lg.Logger) (Flusher, error)

// RP — Replayer
type Callbacks struct {
    OnData     func(blockID uint64, data []byte) error
    OnCommit   func(txnID uint64, commitTS uint64) error
    OnRollback func(txnID uint64) error
}

func New(dir string, sm *lf.SegmentManager, cb Callbacks, log lg.Logger) (Replayer, error)
```

`Replayer` is constructed with a `Callbacks` struct (not a closure or function pointer), so all three hooks can be provided at once and zero-value hooks are no-ops.

### Phase 2: Flusher (`FL/fl.go` + `lsn.go`)

1. `writeBuffer` struct: seg, records (pre-allocated 256 KB), lsn (uint64), mu, synced (uint64, atomic)
2. `Sync`: flush write buffer to OS, fsync segment FD, update synced
3. `BatchSync`: wait for multiple transactions, single fsync
4. `SyncDir`: call `fm.SyncDir("wal")` after fsync to ensure directory entries durable

**Flush trigger policy (added after design review):**
- `Writer.Append` writes to the in-memory 256 KB `records` buffer only — never `pwrite`s directly.
- The buffer is flushed (single `pwrite` of its full contents) when **any** of the following is true:
  1. `len(records) + encodedRecordLen > WALBufSize` (buffer would overflow)
  2. `Writer.Sync()` is called (commit barrier)
  3. A segment rotation is about to occur
- After flush, `records` is reset to `buf[:0]`. The 256 KB backing array is retained.
- `Sync` always flushes, then `fsync` the segment FD, then `SyncDir` the WAL directory.

### Phase 3: Replayer (`RP/rp.go` + `checkpoint.go`)

1. `Replayer`: scan all `wal.*` files via `sm.ListSegments()`
2. `LastCheckpoint`: scan backward from last segment, find `RTYPE == RTCheckpoint`
3. `Replay`: find last checkpoint, scan from checkpoint LSN, apply records in LSN order (not filename order)
4. `onData(blockID, data)`: call `bp.Upsert(page)` to inject into buffer pool — **in iter-03 this is a logged no-op (logs the blockID + data length)**. Real wiring to the memtable is deferred to iter-04/05 when the memtable cluster exists. The callback signature is final; only the body changes.
5. `onCommit(txnID, commitTS)`: stub — log only
6. `onRollback(txnID)`: stub — log only
7. After replay: truncate clean segments before checkpoint via `sm.Truncate`. Do NOT update manifest — manifest is source of truth.

**WAL corruption handling (resolves WAL.md Open Issue #1):**
- If a segment's varint `length` would read past the current EOF: stop silently (treat as end of segment, not corruption). This handles torn writes from a crash mid-record.
- If a segment's CRC32 (envelope) does not match: log a warning and skip to the next segment. Do not abort replay. Recovered state is best-effort.
- Per-record envelope CRC32 is **deferred to v2** (see Deferred). v1 relies on `length` varint + EOF detection.

### Phase 4: Tests

- `wr_test.go` — record encoding round-trip, LSN counter, segment rotation, Append correctness
- `fl_test.go` — fsync, SyncDir, batch sync coordination
- `rp_test.go` — segment scanning, checkpoint find/skip, replay in LSN order, crash recovery simulation

### Phase 5: Benchmarks

- `wal_bench.go` — sequential append throughput, fsync latency under race detector

### Phase 6: Reliability

Each reliability requirement maps to specific implementation decisions:

| Req | Implementation |
|---|---|
| R21 | `sync.Mutex` on `writeBuffer`, `sync.RWMutex` on replayer state; atomic LSN counter |
| R22 | `sync.Once` on `Close()`; `Close()` returns cached error; `Sync()` after `Close()` is a no-op |
| R23 | `Sync()` returns `unix.Fsync()` error directly; flusher logs error before returning |
| R24 | `sp.Get(WALBufSize)` once per segment; `append` into pre-allocated slice — no `make` in hot path |
| R25 | On segment rotation, clear `buf[:n]` before reuse with `for i := range buf { buf[i] = 0 }` |
| R26 | `decodeRecord`: if `length` varint reads past EOF, stop silently (end of segment); no panic |
| R27 | `Replay`: sort segments by number, then read LSN from each record header to determine order |
| R28 | `ListSegments()` returns nil → `Replay()` returns nil immediately; no error |
| R29 | `Truncate(n, checkpoint.LSN % SegSize)` called after replay but before manifest update |

## Deferred to v2

- WAL compression (lz4)
- Per-record envelope CRC32 (v1 relies on varint `length` + EOF detection; full CRC32 on the envelope is v2)
- RTMerge support (compaction output bypasses WAL) — `RTMerge=4` is reserved in the enum but never written in v1
