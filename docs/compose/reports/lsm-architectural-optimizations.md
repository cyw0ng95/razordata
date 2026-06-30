# Architectural Optimizations for High-Performance Embedded LSM-Tree Databases

> Research report — June 2026
> Focus: Embedded / LSM-tree databases with a performance goal, full-stack survey
> Practical relevance for Go-based embedded database design

---

## Table of Contents

1. [Storage Engine Design](#1-storage-engine-design)
2. [Compaction Strategy](#2-compaction-strategy)
3. [Write Path (WAL & Memtable)](#3-write-path-wal--memtable)
4. [Read Path (Index, Cache, Filters)](#4-read-path-index-cache-filters)
5. [Concurrency & Lock-Free Structures](#5-concurrency--lock-free-structures)
6. [Hardware-Informed Optimizations](#6-hardware-informed-optimizations)
7. [Query Execution & Scan](#7-query-execution--scan)
8. [Case Studies](#8-case-studies)
9. [Recommendations for a Go-based Embedded DB](#9-recommendations-for-a-go-based-embedded-db)
10. [References](#10-references)

---

## 1. Storage Engine Design

### 1.1 LSM-Tree Fundamentals

An LSM-tree (Log-Structured Merge-Tree) works by buffering writes in a **memtable** (in-memory sorted structure), flushing it to an immutable **SSTable** (Sorted String Table) on disk, and periodically merging SSTables through **compaction**. The key design dimensions are:

- **Compaction strategy**: leveled vs. tiered vs. hybrid
- **SSTable format**: block-based, key-value separation, fence-pointer indexing
- **Memory layout**: memtable representation (skip-list, B-tree, ART, hash)

### 1.2 Key-Value Separation (WiscKey Pattern)

**Canonical paper**: WiscKey (FAST '16) separates keys from values — the LSM-tree stores only keys + value pointers, while values reside in a separate log (vLog). Benefits:

- **Dramatically reduced write amplification**: compaction touches only small keys, not large values
- **Better cache utilization**: more keys fit in the block cache
- **Faster compaction**: less data rewritten per compaction cycle

**Trade-offs**:
- Value reads require an extra vLog lookup (sequential vLog reads are fast on SSDs)
- vLog garbage collection is needed
- Best for workloads with large values (>1 KB)

**RocksDB implementation**: Integrated BlobDB — values above `min_blob_size` are stored in blob files, while SSTs hold keys, metadata, filters, and blob references. As of RocksDB 11.0+, FIFO compaction with KV-ratio compaction (`CompactionOptionsFIFO::use_kv_ratio_compaction`) intelligently sizes SST files based on the observed SST/blob byte ratio, preventing runaway intra-L0 compaction.

**Practical takeaway for Go**: Key-value separation is essential for any embedded DB targeting mixed workloads. Go's lack of CGo overhead makes it easier to implement a clean separation layer.

### 1.3 Hybrid B-Tree + LSM Designs

Recent research explores combining B-tree point-lookup performance with LSM-tree write throughput:

- **bLSM** (SOSP '11) is the foundational work that introduced "springs and gears" scheduling to balance read vs. write cost
- **LSM-trie** (NSDI '14) uses a trie-structured LSM for flash, reducing write amplification
- **F1**: Spanner's storage layer uses a hybrid approach — an LSM-tree for writes with B-tree organized read-optimized tiers

**Practical takeaway**: Pure LSM with leveled compaction is the best baseline for an embedded DB targeting general workloads. Key-value separation and intelligent compaction scheduling provide most of the hybrid benefit without architectural complexity.

---

## 2. Compaction Strategy

### 2.1 Leveled Compaction (RocksDB Default)

Levels grow exponentially (default factor ×10). L0 contains files from memtable flushes (overlapping ranges). L1+ files are non-overlapping within a level.

**Canonical optimization**: Dynamic level sizing (`level_compaction_dynamic_level_bytes`). Adjusts level sizes based on data volume rather than fixed multipliers. RocksDB doc reports this reduces write amplification by ~30-50% for databases that don't perfectly fill all levels.

**Write amplification**: ~10-30× for leveled, tunable with level count and size ratio.

### 2.2 Tiered (Universal) Compaction

All files in L0 go through size-tiered merging. Lower write amplification (~1-5×) but higher space amplification and slower reads.

**RocksDB universal compaction improvements** (blog post 2026): The incremental universal compaction call for contribution aims to break large merges into smaller sub-tasks, reducing compaction debt.

### 2.3 FIFO Compaction

Zero write amplification — files are simply dropped when old or when space budget is exceeded. For time-series and ephemeral data.

**RocksDB 11.0 KV-ratio optimization**: When BlobDB is in use, FIFO's intra-L0 compaction uses a KV-ratio-aware target size. This prevents unbounded L0 file growth while keeping write amplification near FIFO's original design goal (see blog: "FIFO KV-Ratio Compaction for BlobDB-Backed TTL Workloads").

### 2.4 Novel Approaches

- **TRIAD** (VLDB '17): NVMe-aware LSM with three components — a small in-memory table for recent writes, a persistent log for crash recovery, and delayed compaction policies. Reduces write amplification at the tail.
- **NoFTL-KV** (ATC '20): LSM-tree designed specifically to exploit FTL (Flash Translation Layer) behavior, reducing internal flash write amplification.

### 2.5 Compaction Scheduling

**Key insight**: Compaction is the main source of write amplification and the biggest determinant of tail latency.

**Worth adopting**:
- **TTL and deletion-triggered compaction** (RocksDB `NewCompactOnDeletionCollectorFactory`): an SST file containing a high tombstone density triggers compaction sooner, freeing space faster.
- **Subcompaction** (RocksDB): splits a large compaction job across multiple threads for a single level range, using SST file boundaries as split points.
- **Manual compaction** for bulk operations: bulkload via SST ingestion avoids compaction overhead entirely.

---

## 3. Write Path (WAL & Memtable)

### 3.1 Write-Ahead Log

The WAL is the durability backbone. Every write goes to the WAL before the memtable.

**Optimizations**:

| Technique | Description | Relevance for Go |
|-----------|-------------|------------------|
| **Group commit** | Batch multiple WAL writes into one fsync | Essential — Go's `sync` package makes this straightforward |
| **Pipelined write** | Overlap WAL write with memtable insertion | RocksDB `pipelined_write` option; important for multi-core |
| **Direct I/O** (O_DIRECT) | Bypass OS page cache for WAL, avoiding double buffering | Important — needs aligned buffers in Go |
| **WAL compression** | Compress WAL records to reduce I/O | RocksDB 5.17+; trade-off is CPU vs I/O savings |
| **FlushWAL** | Control when to fsync with `FlushWAL` API | RocksDB has this; useful for periodic fsync |
| **Recovery** | 3 recovery modes: kTolerateCorruptedTail, kAbsoluteConsistency, kPointInTimeRecovery | Critical for correctness/speed tradeoff |

**Practical insight from RocksDB blog** ("FlushWAL; less fwrite, faster writes", 2017): A major performance win came from reducing unnecessary `fwrite` calls by tracking dirty pages and only flushing when needed, producing ~30% throughput improvement on some workloads.

### 3.2 Memtable Design

The memtable is the first landing point for writes.

**Options**:

- **Skip-list** (RocksDB default): O(log n) insert/lookup; concurrent writers via CAS on next pointers. Write-heavy workloads benefit from relaxed insertion (non-concurrent memtables).
- **ART (Adaptive Radix Tree)**: O(1) lookups on many workloads; more CPU-efficient for point queries. Memory overhead can be higher.
- **Hash-linked-list**: Cuckoo-style or hash-skiplist for fast lookups when range scans are not critical.
- **Unsorted (vector memtable)**: Write-optimized, sorted on flush. Best for pure write-heavy workloads.

**Benchmark results** (RocksDB internal): Skip-list is the best general-purpose choice. ART can be faster for read-heavy workloads with small keys. Vector memtable wins for write throughput at the cost of read performance.

**Pebble implementation**: Arena-backed concurrent skip-list with CAS-based insertion. This proved to be a good balance for CockroachDB's workload.

### 3.3 Batch Writes

RocksDB `WriteBatch` groups multiple operations atomically. Pebble's indexed batch supports all operations including range deletions, giving a powerful atomicity primitive.

**Practical tip for Go**: Use `sync.Pool` for WriteBatch recycling to avoid allocation overhead on the hot write path.

---

## 4. Read Path (Index, Cache, Filters)

### 4.1 SSTable Index Formats

The SSTable index maps keys to data block locations.

| Format | Lookup | Overhead | Notes |
|--------|--------|----------|-------|
| Binary search index | O(log n) probes | Low per-SST | RocksDB default, two-level index |
| Hash index | O(1) expected | Higher memory | RocksDB Data Block Hash Index (2018), useful for point lookups |
| Interpolation search | O(log log n) on uniform keys | Negligible | **RocksDB 11.0** — +9.2% throughput on uniform distributions (2026) |
| Fence-pointer | O(log(num_fences)) | Compact | B-tree-like index at SST boundaries |

**RocksDB interpolation search** (2026 blog): Uses the coefficient of variation (CV) of key gaps to automatically choose between binary and interpolation search per index block (`kAuto` mode). The `is_uniform` bit in the block footer costs only ~0.08% CPU during SST construction.

### 4.2 Bloom Filters

Filters avoid unnecessary SST reads for point lookups.

| Filter type | FPR | Memory | Notes |
|-------------|-----|--------|-------|
| Standard Bloom | configurable | 1.44×log₂(1/FPR) bits per key | Classic, well-understood |
| Block-based Bloom | per block granularity | Lower | Good tradeoff |
| Ribbon filter (RocksDB 6.25+) | same FPR, 30% less memory | 30% smaller | **Use this** — mathematically superior to classical Bloom |
| Prefix Bloom | configurable | Per prefix-set | For prefix-based point lookups |

**RocksDB Ribbon Filter** (2021 blog): A "new and improved" Bloom filter that uses practical PFR (Perfect Hashed Filter with Reconfigurable), reducing memory by ~30% at the same false positive rate, or lower FPR for the same memory.

**Partitioned Index/Filters** (RocksDB 2017): Splits the index/filter block into fixed-size partitions, avoiding O(SST size) memory allocation for filters. Essential for large SST files.

### 4.3 Block Cache

| Cache | Eviction | Features |
|-------|----------|----------|
| LRUCache | LRU | RocksDB default, sharded per-CPU |
| HyperClockCache | CLOCK-based eviction | **Lock-free** (RocksDB 2022+), much better multi-core scaling |
| SecondaryCache | N/A | Tiered: DRAM + NVM/SSD cache |

**HyperClockCache** (RocksDB 8.1+): Uses the BitFields API for lock-free metadata. Refactored in 2025 with the new BitFields API to simplify the acquire/release counter logic. It is the recommended cache for workloads with high concurrency.

**Pebble's approach**: Block cache in pure Go, with better concurrency than RocksDB because it avoids the CGo cross-boundary overhead. The CockroachDB blog reports Pebble matches or exceeds RocksDB on all YCSB workloads, with particular wins on YCSB-C (reads) due to better cache concurrency.

### 4.4 Zero-Copy Reads

**PinnableSlice** (RocksDB 2017): Returns a pointer to cached data instead of copying. The slice pins the cache entry, preventing eviction. Reduces memory allocation and memcpy in point lookups.

**Practical takeaway for Go**: Zero-copy reads in Go are more challenging due to GC, but `[]byte` returned from a cache can use RCU-like patterns (reference counting) or `unsafe` with careful lifetime management. Pebble's approach of reading directly from mmap'd files is a practical alternative.

---

## 5. Concurrency & Lock-Free Structures

### 5.1 Latch-Free Memtable

RocksDB's concurrent skip-list uses CAS (compare-and-swap) on next-pointers rather than mutex locks. Proved essential for write throughput on multi-core machines.

**Pebble**: Arena-backed concurrent skip-list, same approach.

**Key insight**: The skip-list insertion depends on correctness of `std::atomic` operations. RocksDB's BitFields API (2025 blog) provides type-safe bit packing for lock-free data structures, used in HyperClockCache.

### 5.2 Read-Only Path

RocksDB reads hold no mutex at all after acquiring the table handle — they use snapshots (sequence numbers) for MVCC. The write path is serialized through one `Writer` thread, but reads proceed lock-free.

**Optimizable for Go**: Avoid allocating `Iterator` objects on the heap. Pebble uses iterator pooling, but Go escape analysis can force heap allocation.

### 5.3 MVCC and Snapshots

Both RocksDB and Pebble implement MVCC through sequence numbers. Each write operation gets a strictly increasing sequence number. Reads at a specific snapshot see all writes ≤ that sequence number.

**Snapshot lifecycle**: Lightweight — a snapshot is just a sequence number cached in the MANIFEST. Old data is not physically removed until no snapshot references it.

**Performance implication**: Long-lived snapshots prevent compaction from dropping tombstone-shadowed data. RocksDB's range tombstone conversion (2026) mitigates the read impact of accumulated tombstones.

### 5.4 Latch-Free Cache

HyperClockCache's lock-free metadata operations use CAS on a packed 64-bit word. The acquire/release counters and state bits are updated atomically without a central lock.

**For Go's sync/atomic**: Similar patterns are possible with `atomic.AddUint64` and `atomic.CompareAndSwapUint64`. Sharded caches reduce contention further.

### 5.5 Core-Local Statistics

RocksDB `core-local-stats` (2017 blog): Statistics counters are per-core to avoid contention on `++` operations. Periodically aggregated.

**For Go**: `atomic.AddInt64` is cheap, but per-core sharding (using `runtime.GOMAXPROCS`) can avoid false sharing issues entirely.

---

## 6. Hardware-Informed Optimizations

### 6.1 Direct I/O (O_DIRECT)

Bypasses the OS page cache, avoiding double buffering (once in page cache, once in block cache). RocksDB supports optional `use_direct_reads` and `use_direct_io_for_flush_and_compaction`.

**Alignment requirements**: Buffers must be sector-aligned (typically 4096 bytes).

**For Go**: Can be done with `golang.org/x/sys/unix` and aligned `[]byte` allocation. Requires careful buffer management.

### 6.2 Asynchronous I/O

RocksDB added async I/O for compactions and reads in 2022 (see "Asynchronous IO in RocksDB" blog). Uses `AIO` (Linux) or `io_uring` to overlap I/O with computation.

**io_uring** is the modern path: submission and completion queues share memory between kernel and userspace, avoiding system call overhead per I/O.

**For Go**: Go 1.23+ `os.File` with `Fadvise` and `pread` can be combined with worker pools. Pure `io_uring` integration is possible but requires CGo (for `liburing`) or raw `syscall.RawSyscall`.

### 6.3 NVMe Optimization

**Polling mode**: Instead of waiting for hardware interrupts, actively poll the completion queue. Reduces latency at high I/O rates.

**Queue depth**: Modern NVMe drives support deep queues (up to 64K). Parallel I/O can saturate the device.

**Write combining**: Group small writes into larger ones to maximize NVMe bandwidth (allocation unit alignment).

### 6.4 Persistent Memory (PMem/NVDIMM)

PMem offers byte-addressable, persistent storage that is ~10× faster than NAND flash but slower than DRAM.

**For LSM databases**:
- WAL on PMem: No fsync needed (persistent store), dramatically reduces commit latency
- Memtable on PMem: Large active memtable without DRAM cost
- PMDK (Persistent Memory Development Kit) provides C library support; Go bindings (`pmem` packages) exist but are experimental

### 6.5 CPU Optimizations

- **Prefetching**: RocksDB uses software prefetch (`__builtin_prefetch`) in skip-list traversal and SST index search
- **Cache-line alignment**: Critical data structures (cache entries, block handles) aligned to 64 bytes to avoid false sharing
- **Branch prediction**: Likely/unlikely annotations for error paths
- **Huge pages** (2 MB / 1 GB): RocksDB can allocate index/filter blocks on huge pages, reducing TLB misses (see "Allocating Some Indexes and Bloom Filters using Huge Page TLB")

---

## 7. Query Execution & Scan

### 7.1 Scan Optimizations

**Range Tombstone Conversion** (RocksDB 11.3+, 2026 blog): During a scan, contiguous point tombstones are converted into a single range tombstone. Results:

| Workload | Without conversion | With conversion | Speedup |
|----------|-------------------|-----------------|---------|
| Forward scan, tombstones | 2,685 ops/s | 266,733 ops/s | **~99×** |
| Reverse scan, tombstones | 519 ops/s | 191,119 ops/s | **~368×** |
| Forward scan, no tombstones | 310,052 ops/s | 311,185 ops/s | ~0% (no overhead) |

This is a **breakthrough optimization** — it solves the tombstone accumulation problem at read time without relying on reactive compaction or long-lived snapshot awareness.

**Compaction alignment** (RocksDB 2022): Aligning compaction output file boundaries reduces overlapping across levels, improving scan performance.

### 7.2 Point Lookup Acceleration

- **Bloom filters** first: Check if key might exist in an SST before reading its index
- **Data block hash index**: O(1) probe within a data block (for point lookups with many keys per block)
- **Two-level index**: The data index block is itself indexed, allowing large SSTs without O(index) memory concern

### 7.3 Merge Operation

RocksDB's `Merge` operator allows composable, incremental updates (e.g., counters, JSON patches) without a read-before-write. The merge is applied during compaction (for base data) or during read (for recent data).

**For Go**: Merge operators can dramatically reduce write amplification for aggregation workloads (counters, sums, top-K).

### 7.4 Expression Execution

For an embedded SQL database over an LSM engine:

- **Interpreted bytecode**: Simple, debuggable, but slower per-row. Suitable for simple predicate push-down.
- **Vectorized execution**: Process column batches (tens of rows) in tight loops. DuckDB and ClickHouse show 10-100× speedups over interpreted per-row execution. Not as relevant for key-value LSM engines (row-oriented), but applicable if the SQL layer operates on column groups.
- **Code generation**: LLVM JIT (HyPer, Umbra) or expression compilation (PostgreSQL JIT). Overkill for embedded databases — the overhead of compilation doesn't amortize over short queries.

**Most practical for embedded DB**: Interpreted bytecode with minimal allocations (reuse scratch buffers, avoid boxing). Prefer `Value` structs (3 words: type tag + payload) over `interface{}` for expression evaluation.

---

## 8. Case Studies

### 8.1 RocksDB

| Aspect | Detail |
|--------|--------|
| Language | C++ |
| Lines of code | ~350K+ (grew from LevelDB's 30K) |
| Strength | Battle-tested, enormous feature set, continuous innovation |
| Weakness | Large codebase, C++ complexity, CGo boundary issues |
| Key optimizations | Level compaction, BlobDB, Ribbon filter, HyperClockCache, range tombstone conversion, interpolation search, BitFields API, parallel compression, subcompaction |
| Latest features (2025-2026) | Range tombstone conversion, FIFO KV-ratio compaction, resumable remote compaction, interpolation search, BitFields API |

### 8.2 Pebble (CockroachDB)

| Aspect | Detail |
|--------|--------|
| Language | Go |
| Lines of code | ~45K (core) + ~45K (tests) |
| Strength | Pure Go (no CGo!), cleaner concurrency, simple, purpose-built for CockroachDB |
| Weakness | Smaller community, fewer configuration options, only implements subset of RocksDB features |
| Started | 2020 (public introduction) |
| Why built | CGo overhead, debugging C++ was painful, needed ownership of storage engine optimizations |
| Key wins | 2-3× performance on read-heavy YCSB workloads versus RocksDB (from CGo elimination + better cache concurrency) |

**Pebble architecture**: 
- Arena-backed concurrent skip-list memtable
- Level-based compaction with range-deletion integration
- Block-based SSTables with bloom filters
- Metamorphic + crash testing against RocksDB for correctness
- File system interface (`vfs.VFS`) for testability and crash simulation

**Pebble's deletion-triggered compaction**: Unlike RocksDB, Pebble directly incorporates range deletion information into compaction decisions. This allowed CockroachDB to eliminate its `Compactor` workaround — the workaround was a separate service that forced compaction on recently-deleted ranges to recover disk space faster.

### 8.3 LevelDB

| Aspect | Detail |
|--------|--------|
| Language | C++ |
| Origin | Google (2011) — Sanjay Ghemawat, Jeff Dean |
| Architecture | Classic LSM, level compaction, binary log format |
| Lines | ~30K |
| Limits | Single writer, no concurrent compaction, no bloom filters on data blocks |
| What it inspired | Every LSM variant (RocksDB, Pebble, Levigo, etc.) |

### 8.4 BadgerDB (DGraph)

| Aspect | Detail |
|--------|--------|
| Language | Go |
| Architecture | LSM-tree with **key-value separation** (WiscKey-inspired) |
| Key innovation | Values stored in a log (vLog), only keys in LSM. Reduces write amplification significantly for workloads with large values |
| Memory | Uses `mmap` for both SST and vLog access |
| Strengths | Simpler than RocksDB, good for large-value workloads, pure Go |
| Weaknesses | Higher tail latency, vLog GC is non-trivial, less battle-tested in OLTP |
| Best for | Workloads with large values where write amplification is the bottleneck |

### 8.5 Academic Systems

| System | Year | Key Idea | Relevance |
|--------|------|----------|-----------|
| **bLSM** | SOSP '11 | "Springs and gears" — rate-limits compaction to avoid write stalls | Core idea used in RocksDB's rate-limiter |
| **WiscKey** | FAST '16 | Key-value separation for LSM-trees | Inspired BadgerDB, RocksDB BlobDB |
| **SlimDB** | SOSP '17 | Compressed, hierarchical filters for flash | Novel filter design |
| **NoFTL-KV** | ATC '20 | FTL-aware LSM-tree | Theoretical value |
| **TRIAD** | VLDB '17 | Three-component LSM for NVMe | Practical NVMe guidance |
| **REMIX** | TOS '22 | Merging granularity studies for compaction | Formal analysis of compaction cost |

---

## 9. Recommendations for a Go-based Embedded DB

Based on the research above, here is a prioritized list of architectural decisions for a new Go-based embedded LSM database:

### Tier 1: Foundational (Implement First)

1. **Leveled compaction with dynamic level sizing** — Best general-purpose compaction strategy. Implement with configurable level size ratios.

2. **Arena-backed concurrent skip-list memtable** — Proven in both RocksDB and Pebble. Avoid per-insert lock contention.

3. **Block-based SSTable format** — Two-level index (data index + block index). Variable-size blocks (default 4 KB or 16 KB).

4. **Group commit WAL with batch writes** — Essential for write throughput. `sync.Pool` for WriteBatch. Direct I/O option for the WAL.

5. **Bloom filters (Ribbon variant)** — 30% memory savings vs. standard Bloom. Partitioned to avoid O(SST) memory.

6. **Sharded block cache** — Per-core sharding to avoid contention. LRU or CLOCK eviction.

### Tier 2: High-Impact Optimizations

7. **Key-value separation (BlobDB/WiscKey pattern)** — For workloads with values > 512 bytes. Dramatically reduces write amplification.

8. **Range tombstone conversion** — Implement the RocksDB 11.3 approach: during scan, detect contiguous point tombstones and synthesize a range tombstone. Critical for workloads with frequent deletes.

9. **Interpolation search for uniform key distributions** — ~9% throughput win at negligible cost. Auto-detect uniformity via coefficient of variation.

10. **Zone-aware compaction** — Track compaction debt per level. Prioritize levels that are falling behind. Rate-limit to avoid write stalls.

### Tier 3: Advanced

11. **Lock-free cache (HyperClockCache style)** — When multi-core contention on the block cache becomes a bottleneck.

12. **Asynchronous I/O (io_uring)** — For workloads where I/O is the bottleneck. Only on Linux 5.1+.

13. **Per-CPU statistics and memory accounting** — Avoid contention on hot stat counters. Track memory usage for predictable behavior.

14. **Zero-copy read path** — Return references to cached data rather than copies. Use reference counting or arena-style lifetime management.

### Tier 4: Future/Nice-to-Have

15. **Persistent memory (PMem) WAL** — Byte-addressable persistent memory creates new write optimization opportunities. Infrastructure still maturing.

16. **Vectorized scan for column-oriented workloads** — If the SQL layer supports column projections, vectorized scans over column groups in the SSTable can yield 10×+ improvement.

17. **Automatic tuning** — Learn from workload patterns: cache size, compaction trigger thresholds, bloom filter FPR. RocksDB's Tuning Advisor and auto-tuned rate limiter (2017) show the way.

### Architectural Principles

- **Zero CGo**: The entire storage engine must be pure Go. Pebble's experience confirms that CGo is a net negative — debugging, profiling, and maintaining the boundary are all harder. Go's compiler produces competitive code for the workloads that matter.

- **Filesystem abstraction**: Implement a `vfs` interface (like Pebble's) for testability, crash simulation, and custom backends (e.g., remote storage, in-memory).

- **Metamorphic testing**: Essential for a storage engine. Generate random operation sequences, run against multiple configurations, compare outputs. This is Pebble's most important testing innovation.

- **Crash testing**: The same metamorphic test framework with a "restart" operation that discards unsynced data. Use the VFS abstraction to implement this.

- **Allocation discipline**: `sync.Pool` for iterators, write batches, and per-read buffers. Hot paths should allocate zero-heap memory.

---

## 10. References

### Papers

1. O'Neil et al., "The Log-Structured Merge-Tree (LSM-Tree)," Acta Informatica 1996.
2. Sears & Ramakrishnan, "bLSM: A General Purpose Log Structured Merge Tree," SOSP 2011.
3. Lu et al., "WiscKey: Separating Keys from Values in SSD-Conscious Storage," FAST 2016.
4. Ren et al., "SlimDB: A Space-Efficient KV Store," SOSP 2017.
5. Balmau et al., "TRIAD: Creating Synergies Between Memory, Disk and Log in Log-Structured Key-Value Stores," VLDB 2017.
6. Wu et al., "LSM-trie: An LSM-Tree-Based Key-Value Store," NSDI 2014.
7. Dong et al., "NoFTL-KV: Tackling Write Amplification on LSM-Trees with FTL Awareness," ATC 2020.

### Blogs & Technical Reports

8. Facebook, "RocksDB Official Blog" (rocksdb.org/blog): Range tombstone conversion, FIFO KV-ratio compaction, resumable remote compaction, interpolation search, BitFields API, Ribbon filter, HyperClockCache, asynchronous I/O, direct I/O, partitioned index/filters, FlushWAL, PinnableSlice, core-local stats, and ~60+ more posts (2014-2026).
9. Peter Mattis, "Introducing Pebble: A RocksDB-inspired key-value store written in Go," Cockroach Labs Blog, Sep 2020.
10. Cockroach Labs, "Pebble Docs — RocksDB Compatibility Differences," github.com/cockroachdb/pebble.

### Source Code

11. facebook/rocksdb (github.com/facebook/rocksdb)
12. cockroachdb/pebble (github.com/cockroachdb/pebble)
13. dgraph-io/badger (github.com/dgraph-io/badger)
14. google/leveldb (github.com/google/leveldb)

---

*Report compiled from multiple sources including rocksdb.org/blog, Cockroach Labs engineering blog, and published peer-reviewed systems research papers. The deep-research workflow was unavailable, so sources were fetched individually and synthesized manually.*
