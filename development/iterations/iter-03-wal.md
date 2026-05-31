# Iteration 3 — WAL (Write-Ahead Log)

**Subsystem:** `WAL`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

Sequential durability path. Append-only segments (64 MB). fsync on commit. Replay on startup. Depends on FIL and MEM.

## Dependencies

- Required: `FIL`, `MEM`
- Consumed (concrete types, no interfaces defined):
  - `*lf.SegmentManager` — segment file management (`FIL/LF/lf.go`)
  - `*df.BlockDevice` — block I/O with checksum (`FIL/DF/df.go`)
  - `*sp.syncPool` — pre-allocated write buffers (`MEM/SP/sp.go`)
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

`design/subsystems/WAL.md` says "consumed interfaces: `FileManager`, `BlockDevice`". Neither interface type exists in the codebase. WAL uses concrete types directly: `*lf.SegmentManager`, `*df.BlockDevice`, `*sp.syncPool`. No interface wrappers needed for v1.

#### Resolved: SyncDir with subdirectory path

`FIL/FS.SyncDir("wal")` resolves relative to the database root → `root/wal` ✓. WAL calls `fm.SyncDir("wal")` after fsync to ensure segment directory entries are durable.

## Requirements

| ID | Requirement | Status | Notes |
|---|---|---|---|
| R01 | `RecordType` enum: RTData=0, RTCommit=1, RTRollback=2, RTCheckpoint=3, RTMerge=4 | pending | |
| R02 | Log record encoding: `[length:varint][txnID:varint][type:uint8][payload:blob]` | pending | |
| R03 | `Writer` interface: `Append(batch *WriteBatch) (lsn uint64, err error)`, `Sync`, `Close` | pending | |
| R04 | `lsnCounter`: atomic.Int64, `lsn = segmentNumber * SegSize + offset` | pending | |
| R05 | `logSegment`: number, fd, path, writeOff, buf (pre-allocated 256 KB from MEM/SP) | pending | buf from sp |
| R06 | Segment rotation: close current when `writeOff >= 64 MB`, create new segment | pending | uses *lf.SegmentManager |
| R07 | Sequential append — no reads in hot path | pending | |
| R08 | `Flusher` interface: `Sync/BatchSync/SyncDir` | pending | |
| R09 | `fsync` on segment FD, update `synced` LSN | pending | |
| R10 | `SyncDir` after fsync to ensure directory entries are durable | pending | uses *fs.FileManager |
| R11 | `Replayer` interface: `Replay() error`, `LastCheckpoint() (*Checkpoint, error)` | pending | |
| R12 | Segment scanning: iterate `wal.000`, `wal.001`, ... in numeric order | pending | uses LF.ListSegments |
| R13 | Find last `RTCheckpoint`, rewind to its LSN | pending | |
| R14 | `Checkpoint` struct: LSN, CatalogRootPtr, ManifestChecksum, ActiveTXNs ([]uint64) | pending | |
| R15 | `replayer` struct: dir, segments, lastCheckpoint, onData/onCommit/onRollback callbacks | pending | |
| R16 | Replay RTData: apply block image to buffer pool via `Upsert` | pending | |
| R17 | Replay RTCommit: mark transaction as committed | pending | |
| R18 | Replay RTRollback: discard transaction's write set | pending | |
| R19 | Truncate clean segments before checkpoint after replay | pending | uses LF.Truncate |
| R20 | Simulated crash test: write data, kill process, restart — committed data present | pending | |
| R21 | Goroutine-safe — all public API methods safe for concurrent use | pending | |
| R22 | Idempotent close — Close/Sync safe to call multiple times | pending | |
| R23 | fsync error propagation — failed fsync returned as error, not swallowed | pending | |
| R24 | No allocations in hot path — pre-allocate 256 KB write buffer in WR | pending | per AGENTS.md perf rules |
| R25 | Zero-fill on buffer reuse — pre-allocated buf cleared before each use | pending | |
| R26 | Partial record recovery — truncated record at end of segment handled gracefully | pending | |
| R27 | Replay in LSN order — replay order by LSN, not segment filename | pending | |
| R28 | Clean shutdown — no WAL segments = replay is a no-op | pending | |
| R29 | Segment truncation safe — truncate before checkpoint doesn't lose committed data | pending | |
| R30 | `go vet ./internal/WAL/...` zero warnings | pending | |
| R31 | `go test ./internal/WAL/... -race -count=1` all green | pending | |
| R32 | Benchmark: sequential append throughput | pending | |

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

Implement in `bp`:
- Insert page directly into hash table without disk I/O.
- Used by WAL replayer to inject pages during replay.

### Phase 1: Writer (`WR/wr.go` + `encode.go`)

1. `RecordType` enum, `LogRecord`, `WriteBatch` types
2. `encodeVarint`/`decodeVarint` helpers
3. `encodeRecord`: length + txnID + type + payload
4. Payload encoding:
   - `RTData`: `[blockID:8][checksum:4][data:varint]`
   - `RTCommit`: `[commitTS:8]`
   - `RTRollback`: `[]` (empty)
   - `RTCheckpoint`: `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNCount:varint][activeTXNs:varint...]`
5. `lsnCounter`: atomic.Int64, SegSize constant = 64 MB
6. `logSegment`: wraps `*lf.SegmentManager` + pre-allocated 256 KB buffer from `*sp.syncPool`
7. `Writer.Append`: assign LSN, encode into pre-allocated buffer, pwrite, update writeOff
8. Segment rotation: when writeOff >= SegSize, close segment, call `sm.CreateSegment(n+1)`, swap current segment

### Phase 2: Flusher (`FL/fl.go` + `lsn.go`)

1. `writeBuffer` struct: seg, records (pre-allocated 256 KB), lsn, mu, synced
2. `Sync`: flush write buffer to OS, fsync segment FD, update synced
3. `BatchSync`: wait for multiple transactions, single fsync
4. `SyncDir`: call `fm.SyncDir("wal")` after fsync to ensure directory entries durable

### Phase 3: Replayer (`RP/rp.go` + `checkpoint.go`)

1. `Replayer`: scan all `wal.*` files via `sm.ListSegments()`
2. `LastCheckpoint`: scan backward from last segment, find `RTYPE == RTCheckpoint`
3. `Replay`: find last checkpoint, scan from checkpoint LSN, apply records in LSN order (not filename order)
4. `onData(blockID, data)`: call `bp.Upsert(page)` to inject into buffer pool
5. `onCommit(txnID, commitTS)`: mark transaction committed in TXN (deferred — stubs for now)
6. `onRollback(txnID)`: discard write set (deferred — stubs for now)
7. After replay: truncate clean segments before checkpoint via `sm.Truncate`. Do NOT update manifest — manifest is source of truth.

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
| R24 | `sp.Get(256*1024)` once per segment; `append` into pre-allocated slice — no `make` in hot path |
| R25 | On segment rotation, clear `buf[:n]` before reuse with `for i := range buf { buf[i] = 0 }` |
| R26 | `decodeRecord`: if `length` varint reads past EOF, stop silently (end of segment); no panic |
| R27 | `Replay`: sort segments by number, then read LSN from each record header to determine order |
| R28 | `ListSegments()` returns nil → `Replay()` returns nil immediately; no error |
| R29 | `Truncate(n, checkpoint.LSN % SegSize)` called after replay but before manifest update |

## Deferred to v2

- WAL compression (lz4)
- WAL corruption handling (skip partial record at end of segment)
- RTMerge support (compaction output bypasses WAL)
