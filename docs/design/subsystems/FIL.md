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
    Path string
    FD   int
    Refs atomic.Int64
    mu   sync.Mutex
}
```

- `FD` is the OS file descriptor.
- `Refs` is atomically incremented on `Open()` and decremented on `Close()`. When `Refs == 0`, the FD is closed.
- `mu` protects `FD` from concurrent access and prevents double-close. All public methods acquire `mu` before operating on `FD`.
- `Path` is the absolute path to the file, used for debugging and identification.

### FileManager

```go
type fileManager struct {
    dir     string
    handles sync.Map // map[string]*FileHandle, keyed by absolute path
    dirFDs  sync.Map // map[string]int, cached directory FDs for SyncDir
}
```

- `handles` provides concurrent-safe access to open file handles.
- `dirFDs` caches open directory FDs to avoid repeated `Open`/`Close` for `SyncDir`. Cached FDs are closed on `fileManager.Close()`.

### PathValidator (colocated in FS)

```go
type pathValidator struct {
    root string
}

func (v *pathValidator) Validate(path string) error
func (v *pathValidator) Resolve(path string) (string, error)
```

- Reject any path containing `..` or symlinks. All paths resolved against `root` before use.
- `Resolve` returns the absolute path under `root`; `Validate` returns an error if resolution would escape `root`.

### MetaReader (colocated in MF)

```go
type metaReader struct {
    handle *FileHandle
    path   string
}

func (r *metaReader) Read() (*MetaPage, error)
func (r *metaReader) Write(p *MetaPage) error
```

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
| `IO` | io_uring: Linux async I/O with poll-based completion queue, batched submissions, overlapped read/write for data files |

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
- `SyncDir` uses a cached directory FD from `dirFDs` map. On first call for a directory, opens the FD and caches it. On `fileManager.Close()`, all cached directory FDs are closed.
- Path validation (via embedded `pathValidator`): reject any path containing `..` or symlinks. All paths resolved against the database root before use.
- `Remove` unlinks a file; `List` uses `os.ReadDir` with glob pattern matching.

### IO — io_uring

**Responsibility:** Linux async I/O for data files using io_uring.

**Key behaviors:**
- `uring_linux.go` — registers an `io_uring` instance, submits batched `READ`/`WRITE`/`SYNC_FILE_RANGE` ops, polls CQEs for completion.
- `uring_other.go` — stubs for non-Linux platforms (falls back to pread/pwrite).
- Uses `syscall.IoringRegister` and `syscall.IoringSubmit` / `IoringWaitCqes` for low-latency AIO.
- Designed to overlay on `DF` for data file reads — the block device can optionally use the io_uring path when available, providing overlapped I/O for parallel SST reads during compaction.

## Implementation Plan

1. **`internal/FIL/FS/fs.go`** — path validation, directory creation, `SyncDir`, `Remove`, `List`.
2. **`internal/FIL/DF/df.go`** — `BlockDevice` implementation with `pread`/`pwrite` and `O_DIRECT` fallback.
3. **`internal/FIL/MF/mf.go`** — `MetaPage` struct, `MetaReader` implementation, magic/version validation.
4. **`internal/FIL/LF/lf.go`** — WAL segment file management, `CreateSegment`, `Truncate`, handle pool.
5. **Tests:** `fs_test.go` (path traversal blocking, directory creation), `df_test.go` (checksum, O_DIRECT), `mf_test.go` (round-trip read/write), `lf_test.go` (segment rotation).

## Open Issues

- Should we use `MADV_DONTNEED` or `madvise` for buffer eviction hints?
- How to handle disk full gracefully? Retry with backoff or propagate `ErrIO`?
- Should `FileManager` support file locking (flock) to prevent concurrent access from multiple processes? (multi-process access is out of scope for v1 — single-process embedding is the primary use case).