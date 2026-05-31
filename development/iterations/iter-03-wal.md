# Iteration 3 — WAL (Write-Ahead Log)

**Subsystem:** `WAL`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

Sequential durability path. Append-only segments (64 MB). fsync on commit. Replay on startup. Depends on FIL.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | Log record encoding: `[length:varint][txnID:varint][type:uint8][payload]` | pending |
| R02 | `RecordType`: RTData, RTCommit, RTRollback, RTCheckpoint, RTMerge | pending |
| R03 | `Writer` interface: `Append/Close` | pending |
| R04 | Sequential append — no reads in hot path | pending |
| R05 | LSN allocation: atomic counter, `lsn = segmentNumber * SegSize + offset` | pending |
| R06 | Segment rotation: close current when `writeOff >= 64 MB`, create new segment | pending |
| R07 | Pre-allocated 256 KB write buffer per segment (no per-record `make`) | pending |
| R08 | `Flusher` interface: `Sync/BatchSync/SyncDir` | pending |
| R09 | `fsync` on segment FD, update `synced` LSN | pending |
| R10 | `SyncDir` after fsync to ensure directory entries are durable | pending |
| R11 | `Replayer` interface: `Replay/LastCheckpoint` | pending |
| R12 | Segment scanning: iterate `wal.000`, `wal.001`, ... in numeric order | pending |
| R13 | Find last `RTCheckpoint`, rewind to its LSN | pending |
| R14 | Replay `RTData`/`RTCommit`/`RTRollback` via callbacks | pending |
| R15 | Truncate clean segments before checkpoint after replay | pending |
| R16 | Checkpoint encoding: `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNCount:varint]` | pending |
| R17 | Simulated crash test: write data, kill process, restart — committed data present | pending |
| R18 | `go vet ./internal/WAL/...` zero warnings | pending |
| R19 | `go test ./internal/WAL/... -race -count=1` all green | pending |
| R20 | Benchmark: sequential append throughput | pending |

## Implementation

```
internal/WAL/
├── encode.go      # log record encoding, varint helpers
├── writer.go      # Writer, segment rotation, LSN
├── flusher.go     # Flusher, fsync, SyncDir
├── replayer.go    # Replayer, segment scanning
└── checkpoint.go  # checkpoint encoding/decoding
```

## Deferred

- WAL compression (lz4)
- WAL corruption handling (skip partial record at end of segment)