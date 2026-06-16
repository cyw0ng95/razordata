# WAL — Write-Ahead Log

## Overview

The sole write path for durability. All mutations are serialized to WAL before the storage engine writes data. The WAL is append-only, divided into 64 MB segments. The flusher batches `fsync` calls. On startup, the replay cluster reads the last checkpoint, rewinds to that LSN, and replays subsequent records to reconstruct the memtable and LSM state. Depends on `FIL`.

## Dependencies

- Required: `FIL`
- Consumed interfaces: `FileManager`, `BlockDevice`

## Exposed Interfaces

```go
// Writer appends records to the WAL
type Writer interface {
    Append(batch *WriteBatch) (lsn uint64, err error)
    Sync() error
    Close() error
}

// WriteBatch is a batch of log records for one transaction
type WriteBatch struct {
    TxnID uint64
    Recs  []LogRecord
}

// LogRecord is a single WAL record
type LogRecord struct {
    Type    RecordType
    Key     []byte
    Value   []byte
    BlockID uint64 // which block this record modifies
}

// Replayer replays WAL on startup
type Replayer interface {
    Replay() error
    LastCheckpoint() (*Checkpoint, error)
}

// RecordType identifies the type of a WAL record
type RecordType uint8

const (
    RTData       RecordType = 0
    RTCommit     RecordType = 1
    RTRollback   RecordType = 2
    RTCheckpoint RecordType = 3
    RTMerge      RecordType = 4 // compaction output: ENG writes SST directly, no WAL involvement
)
```

## Data Structures

### LogSegment

```go
type logSegment struct {
    number   int
    fd       int
    path     string
    writeOff int64       // current write offset
    buf      []byte      // pre-allocated write buffer (from SP)
}
```

- Named `wal.000`, `wal.001`, ... (3-digit zero-padded numeric suffix for sorting).
- `writeOff` tracks the current file offset. When `writeOff >= SegmentSize (64 MB)`, the segment is closed and a new one is created.
- `buf` is pre-allocated (from `MEM/SP`) to avoid per-record `make` calls.

### LogRecord Encoding

```
┌──────────────┬──────────┬─────────────┬──────────────────┐
│ length:varint│ txnID:varint│ type:uint8 │ payload:blob     │
└──────────────┴──────────┴─────────────┴──────────────────┘
```

- `length` = total bytes of `txnID + type + payload` (not including the length field itself). Stored as a varint so the reader can skip unknown record types.
- `payload` for `RTData`: `[blockID:8][checksum:4][data:varint]`
- `payload` for `RTCommit`: `[commitTS:8]`
- `payload` for `RTRollback`: `[]` (empty)
- `payload` for `RTCheckpoint`: `[checkpointLSN:8][catalogRootPtr:8][manifestChecksum:4][activeTXNCount:varint][activeTXNs:varint...]`
- `payload` for `RTMerge`: `[newVersion:8][deletedFileCount:varint][deletedFiles:varint...][addedFileCount:varint][addedFiles:varint...]`

### LSN Counter

```go
type lsnCounter struct {
    value atomic.Int64
    segSize int64
}
```

- A global `atomic.Int64` counter. Each log record receives a monotonically increasing LSN before encoding.
- LSN encodes segment number and offset: `lsn = segmentNumber * SegSize + offset`.
- `CurrentLSN() int64` returns the latest LSN. Readers use this to detect stale reads.

### WriteBuffer

```go
type writeBuffer struct {
    seg     *logSegment
    records []byte // pre-allocated 256 KB from MEM/SP
    lsn     uint64
    mu      sync.Mutex
    synced  int64  // lsn that has been fsynced
}
```

- `records` is pre-allocated once per active segment at 256 KB. Small batches are copied into the buffer without allocation; if the buffer is full, a new segment is created (the 64 MB segment size handles the large case, the 256 KB buffer handles per-record overhead).
- Records are appended to `records` via `binary.LittleEndian` — no intermediate allocations.
- `Sync()` calls `fsync` on the segment FD, then updates `synced`.
- Batch commit: multiple transactions can be grouped into one `fsync` call via a `sync.WaitGroup` and a single write barrier.

**Implementation gap (REQ000176, REQ000184):** The current `WAL/FL/fl.go` has stub implementations of `Sync()` and `BatchSync()` that return `nil` without doing anything. The `writeBuffer` struct is also missing. This is a known gap that should be fixed in a future iteration.

### Checkpoint

```go
type Checkpoint struct {
    LSN             uint64
    CatalogRootPtr  uint64
    ManifestChecksum uint32
    ActiveTXNs      []uint64
}
```

- Written every N transactions or every N seconds (configurable via `Options`).
- A `RTCheckpoint` record in the WAL marks this point.
- On startup, `Replayer` finds the last `RTCheckpoint` and rewinds to its `LSN`.

### Recovery

```go
type replayer struct {
    dir     string
    segments []string
    lastCheckpoint *Checkpoint
    onData func(blockID uint64, data []byte) error
    onCommit func(txnID uint64, commitTS uint64) error
    onRollback func(txnID uint64) error
}
```

- Scans all WAL segments from the checkpoint LSN to the end.
- Replay order is determined by LSN (which encodes segment + offset), not segment filename order.
- **Replayer scope:** `onData` callbacks only populate the in-memory memtable state. The replayer does NOT write SST files or update the manifest — those are derived from the manifest on startup, not from WAL replay.
- For `RTData`: apply the block image to the buffer pool and update the memtable in memory.
- For `RTCommit`: mark the transaction as committed in `TXN/SN`.
- For `RTRollback`: discard the transaction's write set.
- After replay, truncate segments before the checkpoint.

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `WR` | Writer: sequential append, log record encoding, segment rotation, LSN allocation, columnar WAL encoding (REQ000314), LZ4 compression (REQ000315), async write with callback |
| `FL` | Flusher: fsync on commit, batch flush, write barrier coordination, LSN counter |
| `RP` | Replay: WAL replay on startup, checkpoint detection, segment truncation, parallel replay (REQ000316) |

## Clusters

### WR — Writer

**Responsibility:** Sequential append, log record encoding, segment rotation, LSN allocation, columnar WAL encoding, LZ4 compression.

**Key behaviors:**
- `Append(batch *WriteBatch)`: assign LSN, encode each record into `buf`, update `writeOff`.
- When `writeOff >= SegmentSize`, close current segment, create new segment, update `seg`.
- `Close()`: final `fsync`, close FD, clean up.
- **Columnar WAL (REQ000314):** `columnar.go` / `columnar_decode.go` encode WAL records in a column-major format for faster replay of key-only scans.
- **LZ4 compression (REQ000315):** `lz4/lz4.go` provides fast hardware-accelerated compression for WAL records. `header.go` stores a version + compression type byte in the segment header.
- **Async write (REQ000317):** `wr.go` supports `AppendAsync()` with a completion callback for non-blocking writer callers.

### FL — Flusher

**Responsibility:** `fsync` on commit, batch flush, write barrier coordination, LSN counter.

**Key behaviors:**
- `Sync()`: flush write buffer to OS page cache, call `fsync` on segment FD, update `synced`.
- Batch commit: `BatchSync()` waits for multiple transactions, then does a single `fsync`.
- `SyncDir()`: after `fsync` on WAL, call `fsync` on the WAL directory to ensure directory entries are durable.

### RP — Replay

**Responsibility:** WAL replay on startup, checkpoint detection, segment truncation, parallel replay.

**Key behaviors:**
- `Replay()`: find last checkpoint, scan from checkpoint LSN, apply records in LSN order.
- After replay: truncate clean segments before the checkpoint. Do not update the manifest — manifest state comes from the manifest file on startup.
- If no checkpoint found (new database), initialize empty state.
- **Parallel replay (REQ000316):** `parallel.go` splits WAL segments into key-range partitions and replays them in parallel via a worker pool. The replay result is merged into the in-memory state atomically.

## Implementation Plan

1. **`internal/WAL/WR/wr.go`** — `Writer` implementation: append, segment rotation, `Close`.
2. **`internal/WAL/WR/encode.go`** — log record encoding: `encodeVarint`, `encodeRecord`, `encodeBatch`.
3. **`internal/WAL/FL/fl.go`** — `Flusher`: `fsync`, batch sync, `SyncDir`.
4. **`internal/WAL/FL/lsn.go`** — `LSN` counter: atomic increment, LSN encode/decode.
5. **`internal/WAL/RP/rp.go`** — `Replayer`: segment scanning, checkpoint detection, record replay.
6. **`internal/WAL/RP/checkpoint.go`** — checkpoint encoding/decoding, manifest update.
7. **Integration test:** simulate crash — write some data, kill process, restart. Verify all committed data is present, uncommitted data is rolled back.

## Shipped Requirements

The following requirements have been implemented and shipped; they are now part of the design baseline.

| ID | Requirement | Iteration |
|---|---|---|
| REQ000027 | Sequential append with LSN allocation | iter-03 |
| REQ000028 | 64 MB segment rotation | iter-03 |
| REQ000029 | `fsync` on commit | iter-03 |
| REQ000030 | Batch flush / write barrier | iter-03 |
| REQ000031 | WAL replay on startup | iter-03 |
| REQ000032 | Checkpoint detection and segment truncation | iter-03 |
| REQ000033 | `RTCheckpoint` record with catalog root | iter-03 |
| REQ000034 | WAL compression (lz4) — pure-Go LZ4 block codec (`WAL/WR/lz4/`); segment header gains `FlagCompressionLZ4` (bit 0 of flags byte); writer optionally lz4-compresses record bodies before CRC; replayer reads flag and decompresses; CRC verified against on-disk (compressed) body to prevent decompression bombs; mixed compressed/uncompressed segments supported | iter-27 |
| REQ000035 | Corruption recovery policy: detect torn write, skip vs. fail | iter-13 |
| REQ000160 | Batch commit with sync.WaitGroup and write barrier | iter-03 |
| REQ000170 | RTMerge record encoding implementation | iter-17 |
| REQ000176 | Batch commit with sync.WaitGroup and write barrier | iter-17 |
| REQ000184 | 256 KB pre-allocated writeBuffer for batched WAL writes | iter-17 |
| REQ000191 | WAL/RP coverage lift: WAL/RP is at 75.2% (multi-segment truncate + ErrUnknownRecord added; shortfall now in resync-window edges) | iter-16 |
| REQ000200 | Per-segment locks (replace global write mutex) | iter-03 |
| REQ000272 | Checksum verification on WAL replay (detect corruption) | iter-23 |
| REQ000299 | WAL columnar batch encoding (column-major, single envelope CRC) | iter-27 |
| REQ000301 | Async fsync (buffered channel with `AsyncSyncResult`, `inflightFsyncs` WaitGroup, `Close` blocks on in-flight fsyncs) | iter-27 (Phase 6) |
| REQ000317 | Parallel WAL replay by key-range partition | iter-27 |

## Open Issues

- Should we support WAL compression (lz4) to reduce I/O, at the cost of CPU?
- (Resolved in iter-13) How to handle WAL corruption (partial record at end of segment)? Answered: tail-of-last-segment is tolerated (torn write, expected after a crash); mid-segment corruption fails loud with `ErrCorrupt`. Wire format bumped to carry a 12-byte segment header and a 4-byte envelope CRC. See `docs/development/iterations/iter-legacy.md`.