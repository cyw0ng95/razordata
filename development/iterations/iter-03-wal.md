# Iteration 3 — WAL (Write-Ahead Log)

**Subsystem:** `WAL`
**Status:** pending
**Est. LOC:** ~2,000

## Overview

Sequential durability path. Append-only segments (64 MB). fsync on commit. Replay on startup. Depends on FIL.

## Dependencies

- Required: `FIL`
- Consumed interfaces: `FileManager`, `BlockDevice`

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

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `RecordType` enum: RTData=0, RTCommit=1, RTRollback=2, RTCheckpoint=3, RTMerge=4 | pending |
| R02 | Log record encoding: `[length:varint][txnID:varint][type:uint8][payload:blob]` | pending |
| R03 | `Writer` interface: `Append(batch *WriteBatch) (lsn uint64, err error)`, `Sync`, `Close` | pending |
| R04 | Sequential append — no reads in hot path | pending |
| R05 | `lsnCounter`: atomic.Int64, `lsn = segmentNumber * SegSize + offset` | pending |
| R06 | `logSegment`: number, fd, path, writeOff, buf (pre-allocated 256 KB from MEM/SP) | pending |
| R07 | Segment rotation: close current when `writeOff >= 64 MB`, create new segment | pending |
| R08 | `Flusher` interface: `Sync/BatchSync/SyncDir` | pending |
| R09 | `fsync` on segment FD, update `synced` LSN | pending |
| R10 | `SyncDir` after fsync to ensure directory entries are durable | pending |
| R11 | `Replayer` interface: `Replay() error`, `LastCheckpoint() (*Checkpoint, error)` | pending |
| R12 | Segment scanning: iterate `wal.000`, `wal.001`, ... in numeric order | pending |
| R13 | Find last `RTCheckpoint`, rewind to its LSN | pending |
| R14 | `Checkpoint` struct: LSN, CatalogRootPtr, ManifestChecksum, ActiveTXNs ([]uint64) | pending |
| R15 | `replayer` struct: dir, segments, lastCheckpoint, onData/onCommit/onRollback callbacks | pending |
| R16 | Replay RTData: apply block image to buffer pool and update memtable in memory | pending |
| R17 | Replay RTCommit: mark transaction as committed | pending |
| R18 | Replay RTRollback: discard transaction's write set | pending |
| R19 | Truncate clean segments before checkpoint after replay | pending |
| R20 | Simulated crash test: write data, kill process, restart — committed data present | pending |
| R21 | `go vet ./internal/WAL/...` zero warnings | pending |
| R22 | `go test ./internal/WAL/... -race -count=1` all green | pending |
| R23 | Benchmark: sequential append throughput | pending |

## Implementation

### Phase 1: Writer (`WR/wr.go` + `encode.go`)

1. `encodeVarint`/`decodeVarint` helpers
2. `LogRecord` struct: `Type`, `Key`, `Value`, `BlockID`
3. `WriteBatch`: `TxnID`, `Recs []LogRecord`
4. `encodeRecord`: length + txnID + type + payload
5. Payload encoding: RTData = `[blockID:8][checksum:4][data:varint]`, RTCommit = `[commitTS:8]`, RTCheckpoint = `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNCount:varint][activeTXNs:varint...]`
6. `Writer.Append`: assign LSN, encode into pre-allocated buffer, pwrite, update writeOff
7. Segment rotation: when writeOff >= SegSize (64 MB), close segment, create new, update current segment

### Phase 2: Flusher (`FL/fl.go` + `lsn.go`)

1. `lsnCounter`: atomic.Int64 value, SegSize int64 constant
2. `CurrentLSN`: return latest LSN
3. `writeBuffer` struct: seg, records (pre-allocated 256 KB), lsn, mu, synced
4. `Sync`: flush write buffer to OS, fsync segment FD, update synced
5. `BatchSync`: wait for multiple transactions, single fsync
6. `SyncDir`: fsync on WAL directory

### Phase 3: Replayer (`RP/rp.go` + `checkpoint.go`)

1. `Replayer`: scan all `wal.*` files in numeric order
2. `LastCheckpoint`: scan backward from last segment, find `RTYPE == RTCheckpoint`
3. `Replay`: find last checkpoint, scan from checkpoint LSN, apply records in LSN order (not filename order)
4. `onData(blockID, data)`: populate buffer pool and memtable state
5. `onCommit(txnID, commitTS)`: mark transaction committed in TXN
6. `onRollback(txnID)`: discard write set
7. After replay: truncate clean segments before checkpoint. Do NOT update manifest — manifest is source of truth.

## Deferred to v2

- WAL compression (lz4)
- WAL corruption handling (skip partial record at end of segment)