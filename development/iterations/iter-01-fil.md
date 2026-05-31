# Iteration 1 — FIL (File I/O)

**Subsystem:** `FIL`
**Status:** pending
**Est. LOC:** ~2,500

## Overview

Block-level disk I/O abstraction. Only depends on LOG. Provides file management, block read/write, meta page, and WAL segment handles.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `FileManager` interface: `Open/Create/Remove/List/MkdirAll/SyncDir` | pending |
| R02 | Path validation: reject `..` and symlinks, resolve against database root | pending |
| R03 | `SyncDir`: cached directory FD map, closed on `Close` | pending |
| R04 | `BlockDevice` interface: `ReadBlock/WriteBlock/Sync/Close/Size` | pending |
| R05 | `pread`/`pwrite` per block (blockID * BlockSize offset) | pending |
| R06 | CRC32 checksum in last 4 bytes of each block (verify on read) | pending |
| R07 | `O_DIRECT` on Linux with `EINVAL`/`ENOTSUP` fallback to buffered I/O | pending |
| R08 | `MetaPage` struct: magic `0x5241524F`, version, blockSize, catalogRootPtr, manifestChecksum | pending |
| R09 | `MetaPage` read on startup: validate magic, validate version <= current | pending |
| R10 | `MetaPage` write on CREATE DATABASE or CHECKPOINT only | pending |
| R11 | WAL segment handle pool: numeric suffix (wal.000, wal.001...), refcounting, `ftruncate` | pending |
| R12 | `go vet ./internal/FIL/...` zero warnings | pending |
| R13 | `go test ./internal/FIL/... -race -count=1` all green | pending |
| R14 | Benchmark: block read/write throughput | pending |

## Implementation

```
internal/FIL/
├── fs.go          # FileManager, path validation, SyncDir
├── df.go          # BlockDevice, pread/pwrite, O_DIRECT, checksum
├── mf.go          # MetaPage, magic validation
└── lf.go          # WAL segment handle pool
```

## Deferred

- `madvise` for buffer eviction hints
- Multi-process file locking (`flock`)
- Disk-full retry with backoff