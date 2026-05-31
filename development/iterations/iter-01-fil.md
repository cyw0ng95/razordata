# Iteration 1 — FIL (File I/O)

**Subsystem:** `FIL`
**Status:** pending
**Est. LOC:** ~2,500

## Overview

Block-level disk I/O abstraction. Only depends on LOG. Provides file management, block read/write, meta page, and WAL segment handles.

## Dependencies

- Required: `LOG`
- Consumed interfaces: `Logger`

## Design Alignment

Directory structure matches `design/subsystems/FIL.md`:
```
internal/FIL/
├── FS/               # FileSystem cluster
│   └── fs.go         # FileManager, path validation, SyncDir, Remove, List
├── DF/               # DataFile cluster
│   └── df.go         # BlockDevice, pread/pwrite, O_DIRECT, CRC32 checksum
├── MF/               # MetaFile cluster
│   └── mf.go         # MetaPage, magic validation, meta.razor read/write
└── LF/               # LogFile cluster
    └── lf.go         # WAL segment handle pool, ftruncate, segment rotation
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `FileManager` interface: `Open/Create/Remove/List/MkdirAll/SyncDir` | pending |
| R02 | Path validation: reject `..` and symlinks, resolve against database root | pending |
| R03 | `SyncDir`: cached directory FD map (`dirFDs sync.Map`), closed on `Close` | pending |
| R04 | `BlockDevice` interface: `ReadBlock/WriteBlock/Sync/Close/Size` | pending |
| R05 | `pread`/`pwrite` per block (offset = blockID * BlockSize) | pending |
| R06 | CRC32 checksum in last 4 bytes of each block (verify on read) | pending |
| R07 | `O_DIRECT` on Linux with `EINVAL`/`ENOTSUP` fallback to buffered I/O | pending |
| R08 | `MetaPage` struct: Magic `0x5241524F`, Version, BlockSize, CatalogRootPtr, ManifestChecksum | pending |
| R09 | `MetaPage` read on startup: validate magic, validate version <= current | pending |
| R10 | `MetaPage` write on CREATE DATABASE or CHECKPOINT only | pending |
| R11 | WAL segment handle pool: numeric suffix (`wal.000`, `wal.001`), refcounting | pending |
| R12 | `Truncate(n)` calls `ftruncate` to shrink segment after replay | pending |
| R13 | `go vet ./internal/FIL/...` zero warnings | pending |
| R14 | `go test ./internal/FIL/... -race -count=1` all green | pending |
| R15 | Benchmark: block read/write throughput | pending |

## Implementation

### Phase 1: FileSystem (`FS/fs.go`)

1. `pathValidator`: reject `..` and symlinks, `Resolve` returns absolute path under root
2. `FileManager` struct: `handles sync.Map` (map[string]*FileHandle), `dirFDs sync.Map`
3. `FileHandle`: `Path`, `FD` (int), `Refs atomic.Int64`, `mu sync.Mutex`
4. `Open`: lookup in handles, increment Refs, return or create new
5. `Create`: `Open` with `O_CREATE|O_EXCL`, refcount = 1
6. `Remove`: `Unlink`, remove from handles map if cached
7. `SyncDir`: use cached dirFD from `dirFDs`, open + cache on first call, close on `fileManager.Close`

### Phase 2: DataFile (`DF/df.go`)

1. `BlockDevice` interface: `ReadBlock/WriteBlock/Sync/Close/Size`
2. `ReadBlock`: `pread(fd, buf, offset)` where offset = blockID * BlockSize
3. `WriteBlock`: `pwrite(fd, buf, offset)`, append CRC32 to last 4 bytes before write
4. `Sync`: `fsync(fd)`, `Close`: close FD if Refs == 0
5. Checksum verification: read last 4 bytes, compute CRC32, mismatch → `ErrCorrupt`
6. `O_DIRECT`: attempt `open(name, O_RDWR|O_DIRECT)`, fall back to regular open on failure

### Phase 3: MetaFile (`MF/mf.go`)

1. `MetaPage` struct matching design: Magic (0x5241524F), Version, BlockSize, CatalogRootPtr, ManifestChecksum
2. `MetaReader`: open `meta.razor`, read block 0, validate magic, validate version
3. `MetaWriter`: write block 0 only on CREATE or CHECKPOINT
4. Version check: if version > current version, return `ErrUpgradeRequired`

### Phase 4: LogFile (`LF/lf.go`)

1. Segment naming: `wal.000`, `wal.001`, ... (3-digit zero-padded numeric suffix for sorting)
2. `CreateSegment(n)`: open `wal.<N>` for append-only
3. Handle pool: reused `FileHandle` per active segment
4. `Truncate(n)`: `ftruncate` to shrink segment `<N>` after replay

## Deferred to v2

- `madvise` for buffer eviction hints
- Multi-process file locking (`flock`)
- Disk-full retry with backoff