# FIL — File I/O Layer

## Overview

The lowest layer. Abstracts disk I/O for all other subsystems. Provides block read/write, file handle management, meta page access, WAL segment files, and path validation. Depends only on `LOG`.

## Dependencies

- Required: `LOG`
- Consumed interfaces: `Logger`

## Exposed Interfaces

```go
// BlockDevice provides block-level I/O
type BlockDevice interface {
    ReadBlock(ctx context.Context, blockID uint64, buf []byte) error
    WriteBlock(ctx context.Context, blockID uint64, buf []byte) error
    Sync() error
    Close() error
    Size() (int64, error)
}

// FileManager manages files in the database directory
type FileManager interface {
    Open(name string) (*FileHandle, error)
    Create(name string) (*FileHandle, error)
    Remove(name string) error
    List(pattern string) ([]string, error)
    MkdirAll(name string) error
    SyncDir(path string) error
}

// FileHandle is an open file with reference counting
type FileHandle struct {
    Path string
    FD   int
    Refs atomic.Int64
}

// PathValidator validates paths against the database root
type PathValidator interface {
    Validate(path string) error
    Resolve(path string) (string, error)
}

// MetaReader reads the meta page
type MetaReader interface {
    Read() (*MetaPage, error)
    Write(p *MetaPage) error
}
```

## Data Structures

### MetaPage

```go
type MetaPage struct {
    Magic            uint32  // 0x5241524F "RAZO"
    Version          uint32  // semantic version, bumped on format changes
    BlockSize        uint32  // block size in bytes (power of 2, default 4096)
    CatalogRootPtr   uint64  // root pointer of the system catalog LSM tree
    ManifestChecksum uint32  // checksum of the current manifest
}
```

- Always occupies block 0 of `meta.razor`.
- Read on startup to validate magic bytes, load version, and get the catalog root.
- Written only on `CREATE DATABASE` and `CHECKPOINT`.
- `CatalogRootPtr` references the root node of the system catalog (a special LSM tree).

### FileHandle

```go
type FileHandle struct {
    path string
    fd   int
    refs atomic.Int64
    mu   sync.Mutex
}
```

- Reference-counted. `Open()` increments `refs`; `Close()` decrements and closes when `refs == 0`.
- All operations protected by `sync.Mutex` to prevent double-close and concurrent access to the same FD.

### FileManager

```go
type fileManager struct {
    dir    string
    root   string
    handles sync.Map // map[string]*FileHandle
    mu     sync.RWMutex
}
```

- All paths are validated against `root` (the database directory).
- `handles` is a `sync.Map` keyed by absolute path — provides concurrent-safe access to open file handles.

### File Path Conventions

All files follow these conventions under the database root `<name>.razor/`:

| Path | Description |
|---|---|
| `<name>.razor/meta.razor` | Meta page (block 0) |
| `<name>.razor/wal/wal.<N>` | WAL segment N (64 MB each) |
| `<name>.razor/sst/L<N>/<fileID>.sst` | SST file at level N |
| `<name>.razor/manifest` | Current LSM manifest |
| `<name>.razor/hint` | Buffer pool warm hint file |

### O_DIRECT Fallback

- On Linux, `Open` attempts `O_DIRECT` for data files to bypass the OS page cache.
- If `EINVAL` or `ENOTSUP` is returned, fall back to regular `Open` (buffered I/O).
- WAL segments always use buffered I/O (fsync handles durability).

## Function Clusters

| Cluster | Responsibility |
|---|---|
| `DF` | DataFile: block read/write via pread/pwrite, O_DIRECT support, checksum |
| `MF` | MetaFile: meta.razor read/write, magic validation |
| `LF` | LogFile: WAL segment creation, rotation, handle pool |
| `FS` | FileSystem: directory management, path validation, SyncDir |

## Clusters

### DF — Data File

**Responsibility:** Block read/write via `pread`/`pwrite`, `O_DIRECT` support, checksum verification.

**Key behaviors:**
- `ReadBlock`: `pread(fd, buf, offset)` where `offset = blockID * BlockSize`.
- Checksum verification on every read — CRC32 (IEEE polynomial) stored in the last 4 bytes of each block. On mismatch, return `ErrCorrupt`.
- `WriteBlock`: `pwrite(fd, buf, offset)`. Checksum is computed and appended before write.
- `O_DIRECT`: alignment requirements — `buf` must be aligned to `BlockSize`, `offset` must be aligned to `BlockSize`. Use `syscall.Mmap` or `unix.RawSyscall` to meet alignment.

### MF — Meta File

**Responsibility:** `meta.razor` read/write, magic bytes, version, catalog root pointer.

**Key behaviors:**
- Open `meta.razor` on startup. If it does not exist, create it with default values.
- Validate `Magic == 0x5241524F`. If mismatch, the directory is not a valid razordata database.
- Validate `Version <= CurrentVersion`. If `Version > CurrentVersion`, return `ErrUpgradeRequired` (this version of razordata is too old).
- `Write` is only called from `CHECKPOINT` or `CREATE DATABASE`.

### LF — Log File

**Responsibility:** WAL segment file creation, handle management, `ftruncate`.

**Key behaviors:**
- Segments named `wal.0`, `wal.1`, ... (numeric suffix, zero-padded to 3 digits for sorting).
- `CreateSegment(n)` opens `wal.<N>` for append-only access.
- `Truncate(n)` calls `ftruncate` to shrink segment `<N>` after replay.
- Handle pool: reused `FileHandle` for each active segment.

### FS — File System

**Responsibility:** Directory management, `fsync` on directories, path validation.

**Key behaviors:**
- `MkdirAll` creates directories with permission `0700`.
- `SyncDir` calls `fsync` on the directory FD to ensure directory entry changes are durable.
- Path validation: reject any path containing `..` or symlinks. All paths resolved against the database root before use.
- `Remove` unlinks a file; `List` uses `os.ReadDir` with glob pattern matching.

## Implementation Plan

1. **`internal/FIL/FS/fs.go`** — path validation, directory creation, `SyncDir`, `Remove`, `List`.
2. **`internal/FIL/DF/df.go`** — `BlockDevice` implementation with `pread`/`pwrite` and `O_DIRECT` fallback.
3. **`internal/FIL/MF/mf.go`** — `MetaPage` struct, `MetaReader` implementation, magic/version validation.
4. **`internal/FIL/LF/lf.go`** — WAL segment file management, `CreateSegment`, `Truncate`, handle pool.
5. **Tests:** `fs_test.go` (path traversal blocking, directory creation), `df_test.go` (checksum, O_DIRECT), `mf_test.go` (round-trip read/write), `lf_test.go` (segment rotation).

## Open Issues

- Should we use `MADV_DONTNEED` or `madvise` for buffer eviction hints?
- How to handle disk full gracefully? Retry with backoff or propagate `ErrIO`?
- Should `FileManager` support file locking (flock) to prevent concurrent access from multiple processes?