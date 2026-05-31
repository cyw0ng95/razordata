# Iteration 1 — FIL (File I/O)

**Subsystem:** `FIL`
**Status:** done
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
│   └── fs.go         # FileManager, pathValidator, SyncDir, Remove, List, MkdirAll
├── DF/               # DataFile cluster
│   └── df.go         # BlockDevice, pread/pwrite, O_DIRECT, CRC32 checksum
├── MF/               # MetaFile cluster
│   └── mf.go         # MetaPage, MetaReader, MetaWriter, magic/version validation
└── LF/               # LogFile cluster
    └── lf.go         # WAL segment handle pool, ftruncate, segment rotation
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | `FileManager` interface: `Open/Create/Remove/List/MkdirAll/SyncDir` | done |
| R02 | Path validation: reject `..` and symlinks, resolve against database root | done |
| R03 | `SyncDir`: cached directory FD map (`dirFDs sync.Map`), closed on `Close` | done |
| R04 | `MkdirAll`: creates directories with permission `0700` | done |
| R05 | `List`: uses `os.ReadDir` with glob pattern matching | done |
| R06 | `FileHandle` struct: `Path`, `FD`, `Refs`, `mu sync.Mutex` | done |
| R07 | `FileHandle.Close()`: closes FD immediately, guarded by `mu`, idempotent | done |
| R08 | `BlockDevice` interface: `ReadBlock/WriteBlock/Sync/Close/Size` | done |
| R09 | `pread`/`pwrite` per block (offset = blockID * BlockSize) | done |
| R10 | CRC32 checksum in last 4 bytes of each block (verify on read) | done |
| R11 | `O_DIRECT` on Linux with `EINVAL`/`ENOTSUP` fallback; `buf` aligned to `BlockSize` | done |
| R12 | `BlockSize` power of 2, default 4096 | done |
| R13 | `MetaPage` struct: Magic `0x5241524F`, Version, BlockSize, CatalogRootPtr, ManifestChecksum | done |
| R14 | `meta.razor` created with defaults if absent on startup | done |
| R15 | `MetaPage` read: validate magic == `0x5241524F`, version <= current | done |
| R16 | `MetaPage` write: block 0 only, on CREATE DATABASE or CHECKPOINT | done |
| R17 | WAL segments under `wal/` subdir: `wal/wal.000`, `wal/wal.001`, ..., 3-digit zero-padded | done |
| R18 | WAL segments always use buffered I/O (no O_DIRECT) | done |
| R19 | WAL handle pool: refcounting, `FileHandle` reused per active segment | done |
| R20 | `CreateSegment(n)`: opens `wal/wal.<N>` append-only; `Truncate(n)`: `ftruncate` after replay | done |
| R21 | `go vet ./internal/FIL/...` zero warnings | done |
| R22 | `go test ./internal/FIL/... -race -count=1` all green | done |
| R23 | Benchmark: block read/write throughput | done |

## Implementation

### Phase 1: FileSystem (`FS/fs.go`)

1. `pathValidator`: reject `..` and symlinks, `Resolve` returns absolute path under root
2. `FileManager` struct: `handles sync.Map` (map[string]*FileHandle), `dirFDs sync.Map`
3. `FileHandle`: `Path`, `FD` (int), `Refs atomic.Int64`, `mu sync.Mutex`
4. `Open`: lookup in handles, increment Refs, return or create new; acquire `mu` before operating on `FD`
5. `Create`: `Open` with `O_CREATE|O_EXCL`, refcount = 1
6. `Remove`: `Unlink`, remove from handles map if cached
7. `MkdirAll`: creates directories with permission `0700`
8. `List`: uses `os.ReadDir` with glob pattern matching
9. `SyncDir`: use cached dirFD from `dirFDs`, open + cache on first call, close on `fileManager.Close`
10. `FileHandle.Close()`: closes FD when `Refs == 0`, guarded by `mu`, prevents double-close

### Phase 2: DataFile (`DF/df.go`)

1. `BlockDevice` interface: `ReadBlock/WriteBlock/Sync/Close/Size`
2. `BlockSize`: power of 2, default 4096
3. `ReadBlock`: `pread(fd, buf, offset)` where offset = blockID * BlockSize
4. `WriteBlock`: `pwrite(fd, buf, offset)`, append CRC32 to last 4 bytes before write
5. Checksum verification: read last 4 bytes, compute CRC32, mismatch → `ErrCorrupt`
6. `O_DIRECT`: attempt `open(name, O_RDWR|O_DIRECT)`, fall back to regular open on `EINVAL`/`ENOTSUP`
7. `Sync`: `fsync(fd)`, `Close`: close FD if Refs == 0

### Phase 3: MetaFile (`MF/mf.go`)

1. `MetaPage` struct: Magic (`0x5241524F`), Version, BlockSize, CatalogRootPtr, ManifestChecksum
2. `MetaReader`: if `meta.razor` absent, create with default values; open, read block 0, validate magic, validate version <= current
3. `MetaWriter`: write block 0 only on CREATE DATABASE or CHECKPOINT
4. Version check: if version > current version, return `ErrUpgradeRequired`

### Phase 4: LogFile (`LF/lf.go`)

1. Segment path: `wal/wal.000`, `wal/wal.001`, ... (3-digit zero-padded, under `wal/` subdir)
2. WAL segments always use buffered I/O (no O_DIRECT)
3. `CreateSegment(n)`: `MkdirAll("wal")` then open `wal/wal.<N>` append-only
4. Handle pool: reused `FileHandle` per active segment, refcounted
5. `Truncate(n)`: `ftruncate` to shrink segment after WAL replay

## Deferred to v2

- `madvise` for buffer eviction hints
- Multi-process file locking (`flock`)
- Disk-full retry with backoff