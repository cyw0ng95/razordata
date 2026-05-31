# Iteration 5 — ENG/SST (SST Writer + Reader + Manifest)

**Subsystem:** `ENG`
**Status:** pending
**Est. LOC:** ~3,500

## Overview

SST file format. Writer produces SST from memtable flush. Reader serves block lookups. Manifest tracks all live SST files. Depends on LOG.

## Requirements

| ID | Requirement | Status |
|---|---|---|
| R01 | SST file format: `[DataBlock_0]...[DataBlock_N][IndexBlock][BloomFilter][Footer]` | pending |
| R02 | DataBlock format: `[KV pairs][restart array][restart count:4][checksum:4]` | pending |
| R03 | Restart points every 16 K-V pairs for block-level binary search | pending |
| R04 | IndexBlock: one entry per data block `[largestKey:varint][blockOffset:varint][blockSize:varint]` | pending |
| R05 | Bloom filter: 10 bits/key, double-hash (two independent hash functions), set on `Insert` | pending |
| R06 | Footer (28 bytes): `[indexOffset:8][indexSize:4][bloomOffset:8][bloomSize:4][magic:4]` | pending |
| R07 | SST writer: encode all blocks, write bloom filter, write footer | pending |
| R08 | SST reader: binary search IndexBlock to find target block | pending |
| R09 | SST reader: bloom check before block read (skip file if bloom says "probably not present") | pending |
| R10 | SST reader: decode block, find key via restart point binary search | pending |
| R11 | `SSTFileMeta`: FileID, Level, MinKey, MaxKey, Size, BloomBits | pending |
| R12 | `Version` struct: immutable, num, levels ([][]SSTFileMeta), created time | pending |
| R13 | `Manifest` interface: `Current/Apply/Checkpoint` | pending |
| R14 | Manifest atomic update: write temp file → fsync → rename → fsync dir | pending |
| R15 | Manifest stores all live SST files across levels | pending |
| R16 | SST round-trip: write → flush → read (verify data integrity) | pending |
| R17 | `go vet ./internal/ENG/...` zero warnings | pending |
| R18 | `go test ./internal/ENG/... -race -count=1` all green | pending |
| R19 | Benchmark: SST write/read throughput | pending |

## Implementation

```
internal/ENG/
├── sst_writer.go  # SST file writer, block encoding, bloom, footer
├── sst_reader.go  # SST file reader, index binary search, bloom check
├── sst_block.go   # block encode/decode, restart points
├── manifest.go    # Manifest, Version, atomic update
└── sst_meta.go    # SSTFileMeta, file naming (L<level>_<minKey>_<maxKey>_<fileID>.sst)
```

## Deferred

- Compaction (leveled merge sort)
- Secondary indexes
- Prefix bloom filters
- Statistics (row count per SST)