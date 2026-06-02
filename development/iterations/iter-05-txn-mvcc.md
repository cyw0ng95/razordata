# Iteration 5 — ENG/SST (SST Writer + Reader + Manifest)

**Subsystem:** `ENG`
**Status:** pending
**Est. LOC:** ~3,500

## Overview

SST file format. Writer produces SST from memtable flush. Reader serves block lookups. Manifest tracks all live SST files. Depends on LOG.

## Dependencies

- Required: `LOG`
- Consumed interfaces: `Logger`

## Design Alignment

Directory structure matches `design/subsystems/ENG.md`:
```
internal/ENG/
└── LS/               # LSM tree cluster (part 2: SST + manifest)
    ├── sst_writer.go # SST file writer, block encoding, bloom filter, footer
    ├── sst_writer_test.go
    ├── sst_reader.go # SST file reader, index binary search, bloom check
    ├── sst_reader_test.go
    ├── sst_block.go  # block encode/decode, restart points
    ├── sst_block_test.go
    ├── manifest.go   # Manifest, Version, atomic update
    ├── manifest_test.go
    └── sst_meta.go   # SSTFileMeta, file naming
```

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | SST file layout: `[DataBlock_0]...[DataBlock_N][IndexBlock][BloomFilter][Footer: 28 bytes]` | pending |
| R02 | DataBlock format: `[KV pairs][restart array][restart count:4][checksum:4]` | pending |
| R03 | Restart every 16 K-V pairs for block-level binary search | pending |
| R04 | IndexBlock: one entry per data block `[largestKey:varint][blockOffset:varint][blockSize:varint]` | pending |
| R05 | Bloom filter: 10 bits/key, double-hash (two independent hash functions), set on `Insert` | pending |
| R06 | Footer (28 bytes): `[indexOffset:8][indexSize:4][bloomOffset:8][bloomSize:4][magic:4]` | pending |
| R07 | SST writer: encode all data blocks, write index block, write bloom filter, write footer | pending |
| R08 | SST reader: binary search IndexBlock to locate target data block | pending |
| R09 | SST reader: bloom check before block read (skip if bloom says "probably not present") | pending |
| R10 | SST reader: decode block, binary search restart points for exact key | pending |
| R11 | `SSTFileMeta`: FileID, Level, MinKey, MaxKey, Size, BloomBits | pending |
| R12 | `Version` struct: immutable, num (int64), levels ([][]SSTFileMeta), created time | pending |
| R13 | `Manifest` interface: `Current() Version`, `Apply(v Version) error`, `Checkpoint() (*ManifestCheckpoint, error)` | pending |
| R14 | Manifest atomic update: write temp file → fsync → rename → fsync dir | pending |
| R15 | Manifest stores all live SST files across levels | pending |
| R16 | SST round-trip: write → flush → read (verify data integrity) | pending |
| R17 | File naming: `L<level>_<minKeyHex>_<maxKeyHex>_<fileID>.sst` | pending |
| R18 | `go vet ./internal/ENG/...` zero warnings | pending |
| R19 | `go test ./internal/ENG/... -race -count=1` all green | pending |
| R20 | Benchmark: SST write/read throughput | pending |

## Implementation

### Phase 1: SST Block Encoding (`LS/sst_block.go`)

1. `encodeKV(key, value []byte) []byte`: delta-encode key (store only delta from previous key)
2. `encodeBlock(kvs []Pair, restartInterval int) []byte`: collect all KVs, add restart array, append checksum
3. `decodeBlock(data []byte) ([]Pair, []int restartPositions, error)`: parse KVs, parse restart array, return pairs + positions
4. Restart array: every `restartInterval` keys, store absolute offset. Binary search within block.

### Phase 2: SST Writer (`LS/sst_writer.go`)

1. `SSTWriter`: open temp file, collect data blocks (4 KB each), write index block
2. Bloom filter: on `Insert(key)`, hash key with two independent hash functions, set bits in bitset
3. Write order: data blocks → index block → bloom filter → footer
4. Footer: write last (position fixed at end of file minus 28 bytes)

### Phase 3: SST Reader (`LS/sst_reader.go`)

1. `SSTReader`: open SST file, read footer (last 28 bytes), read index block
2. `Get(key)`: binary search index block for largestKey <= key, bloom check, read block, binary search within block
3. `Iterator(prefix)`: find starting block via index binary search, iterate through blocks

### Phase 4: Manifest (`LS/manifest.go`)

1. `Manifest` struct: version (atomic.Int64), current (atomic.Value of Version), dir, changes channel
2. `Version`: immutable struct with num, levels, created time
3. `Apply(v Version)`: create new version, write to temp file, fsync, rename, fsync dir
4. `Current()`: return current version
5. `Checkpoint()`: return current manifest state for WAL checkpoint

## Deferred to v2

- Compaction (leveled merge sort)
- Secondary indexes
- Prefix bloom filters
- Statistics (row count per SST)