# Iteration — REQ001297 ~ REQ001299 (SQLite-style WAL 骨架)

> **Status**: partial — skeleton shipped; full REQ acceptance blocked on
> the architectural swap (see Deviations §1) AND on the missing SLT
> corpus (see Audit §6).
> **Outcome**: shipped skeleton to `main` as commit pending.
> **Lines of code**: +1014 LoC across 6 new files
>   (`internal/WAL/WR/frame.go`, `internal/WAL/WR/frame_test.go`,
>   `internal/WAL/WR/shm.go`, `internal/WAL/WR/shm_test.go`,
>   `internal/WAL/RP/frame.go`, `internal/WAL/RP/frame_test.go`).
> **Verification**: `go test ./internal/WAL/... -race -count=1` PASS;
>   `go vet ./internal/WAL/WR/... ./internal/WAL/RP/...` clean (no new
>   warnings introduced; pre-existing `unsafe.Pointer` warnings in
>   `FIL/IO/uring_linux.go` are unrelated and unchanged).

## Scope

Three REQs that introduce SQLite-style WAL-as-journal mode (24-byte
WAL header, 24-byte frame header, big-endian two-checkum integrity,
wal-index hash table). The current `docs/design/subsystems/WAL.md`
design retains an LSM-recovery WAL (64 MB append-only segments,
per-record header). The two layouts are **architecturally conflicting**:
replacing the existing pipeline would touch Engine.Open, TXN commit
points, the existing per-segment replay, the LZ4 envelope, and the
header version protocol — well outside this iteration's budget.

The user explicitly scoped this delivery to a **skeleton**:

- 24-byte WAL header + frame header encoding/decoding (REQ001297)
- frame-level two-checksum integrity + torn-write detection
  (REQ001298)
- wal-index hash slot addressing in memory only — **no -shm file,
  no reader/writer lock protocol** (REQ001299)

The skeleton is independent from the existing WAL pipeline and does
not touch `Engine.Open`, `WAL/FL`, TXN, or FIL. It is the smallest
piece that proves the byte layout and the checksum algorithm are
correct, and that a future iteration can wire it onto a real
`-wal`/`-shm` file pair with confidence.

## Delivered

| REQ       | Action                                                                     | Test coverage |
|-----------|----------------------------------------------------------------------------|---------------|
| REQ001297 | 24-byte WAL header (`Magic/Version/PageSize/CheckpointSeq/Salt1/Salt2`, big-endian); 24-byte frame header (`PageNo/DbSizeAfterCommit/Salt1/Salt2/Checksum1/Checksum2`, big-endian); `EncodeWalHdr`/`DecodeWalHdr`/`EncodeFrameHdr`/`DecodeFrameHdr`/`AppendFrame`; `WalPageSize=4096`; **`TestWalFrameHeaderSize`, `TestWalMagicBigEndian`, `TestWalHdrRoundTrip`, `TestWalHdrTruncatedInput`, `TestFrameHdrRoundTrip`, `TestFrameHeaderBigEndian`, `TestAppendFrame`** | frame_test.go |
| REQ001298 | init-then-fold big-endian two-checkum (mirrors SQLite `walChecksumBytes`); `FrameChecksum`/`VerifyFrame`; commit marker = `salt1\|frameCount\|c1\|c2` (16 bytes BE) with `CommitMarkerChecksum`/`EncodeCommitMarker`/`VerifyCommitMarker`; **`TestFrameChecksumDeterministic`, `TestWalFrameChecksum_Torn`, `TestCommitMarker`**; replayer-side `InspectFrame` + `VerifyFrameBytes` + `VerifyCommitMarkerBytes` with `ErrFrameShortBuffer` and `ErrPageBodyWrongSize`; replayer tests `TestInspectFrameParsesHeader`, `TestVerifyFrameBytesAcceptsValidFrame`, `TestVerifyFrameBytesRejectsSaltMismatch`, `TestVerifyFrameBytesRejectsBitFlip`, `TestVerifyFrameBytesRejectsTruncatedFrame`, `TestVerifyCommitMarkerAcceptsEncoding` | frame_test.go (wr + rp) |
| REQ001299 | `WalHashTableNslot=4096`, `WalHashTableR=2`; `WalNReadLock=5`, `WalLockWriteLock`, `WalLockCkptLock`, `WalLockTotal`; `WalIndexSlotFor(pageNo, salt1, salt2)` mirroring `walHash`; `WalIndexSize()`, `WalLockByteSize()`; `WalIndexSnapshot` (in-memory slice, **no -shm MAP_SHARED yet**); `Slot`/`Put`/`Size`; **`TestWalIndexSize`, `TestWalLockByteSize`, `TestWalHashDeterministic`, `TestWalHashCollisionsAreDocumented`, `TestWalIndexSnapshotRoundTrip`, `TestWalIndexSnapshotSizeReturnsCanonicalCount`** | shm_test.go |

## Deviations

1. **No -wal/-shm file plumbing.** Real REQ acceptance for REQ001297
   item (4) ("Open both files at Engine.Open; create -shm with
   N×HASH_TABLE_R\*HASHTABLE_NSLOT bytes") and REQ001299 items (2)-(5)
   (`walAcquireReadLock`, `walBeginWrite`, `walEndWrite`,
   `MAP_SHARED`, atomic seq_cst barriers) is intentionally **not**
   shipped — user chose the skeleton scope. The path from
   skeleton→Engine.Open integration is documented in
   `docs/development/REQUIREMENTS.md` REQ row under partial progress,
   so a follow-up iteration can pick it up without context loss.

2. **Checksum algorithm: 4-byte aligned big-endian fold over
   `[0..16) | [24..end)`.** The REQ text suggests checksum-1 sums
   `[0..8) and [24..end)` and checksum-2 sums `[8..16) and
   [16..24)`. Direct two-window summation has a fixed-point problem
   when the algorithm folds checksum bytes (which it must, because
   the replayer reads back what the writer wrote). The skeleton
   implements the SQLite-native version: a single init-then-fold
   `(c1, c2)` accumulators over the 16-byte header prefix and the
   full page body, **excluding the two checksum words themselves**.
   This is the contract `walChecksumBytes` enforces in the SQLite
   source. Both Encoded-then-Decoded-then-Verified frames satisfy
   the same invariant; torn writes (truncated body or overwritten
   checksum slot) are rejected with `ErrFrameChecksumMismatch`.
   `TestWalFrameChecksum_Torn` covers both torn scenarios.

3. **Commit marker length is 16 bytes, not 8.** The REQ text
   describes the "final 8 bytes of the commit marker" carrying a
   checksum. For the skeleton, the marker is `[salt1 BE][frameCount
   BE][c1 BE][c2 BE]` — 16 bytes — using the same
   init-then-fold algorithm as the frame checksum. This keeps the
   algorithm single-shape across both frame- and commit-level
   integrity, and ensures a torn-commit on either field is rejected.

4. **Magic constant corrected.** The first implementation wrote
   `WalMagic = 0x574F4C47`, which serializes to ASCII bytes
   `[W, O, L, G]` — `WOLG`, not `WLOG`. Corrected to `0x574C4F47`
   so the on-disk magic matches the SQLite convention
   (`TestWalMagicBigEndian` enforces ASCII equality).

5. **WalIndexSnapshot is in-memory.** The REQ's `MAP_SHARED` and
   `walAcquireReadLock` semantics are deferred. The skeleton retains
   the same addressing function and slot count so the cross-process
   implementation can swap the underlying storage for an mmap region
   without changing the API surface.

6. **Audit (post-skeleton): SLT acceptance is unreachable in this
   repo's current state.** `git ls-tree -r HEAD --name-only` on the
   submodule `jzombie/sqlite-sqllogictest-corpus` shows only
   `select1..select5.test` + `evidence/`, `index/`, `random/` — no
   `wal.test`, `wal3.test`, `wal4.test`. The SLT drivers
   (`tests/sqlcmp/slt/driver.go`, `razor_driver.go`) have **zero**
   matches for `journal_mode` / `wal_mode`. Therefore REQ001297 item
   (6) ("Verify SLT `wal.test` passes") cannot be executed against
   the existing corpus, and REQ001299's SLT concurrency checks have
   no path to run. A new REQ has been added to REQUIREMENTS.md
   (Bug-To-Requirement Rule) tracking the missing corpus and PRAGMA
   dispatch as an acceptance-blocker for REQ001297~1299.

## Open follow-ups

- **`tests/sqlcmp/corpus/test/wal_mode.test` + SLT driver
  PRAGMA-wal-mode wiring** — required for any end-to-end WAL-mode
  acceptance. Tracked as new REQ (Bug-To-Requirement).
- `Engine.Open` plumbing: create `<db>.razor-wal` and
  `<db>.razor-shm`; route `WAL/WR/AppendFrame` writes through the
  new file; replayer reads back through the new RP-side parsers.
  Touches: `internal/ENG` (Open path), `internal/FIL/LF`,
  `internal/FIL/IO/shm_linux.go` (new).
- Multi-reader/writer concurrency: read-lock array, write lock,
  ckpt lock, MAP_SHARED, atomic seq_cst barriers. Test plan:
  `TestWalConcurrency_TwoReadersOneWriter`,
  `TestWalConcurrency_WriterBlocksReaders`.
- SLT `wal.test` / `wal3.test` / `wal4.test` pass after the
  Engine.Open integration lands AND after the missing-corpus REQ
  ships; auto-checkpoint (REQ001300) and checkpoint modes
  (REQ001303) depend on this.

## Out of scope (explicitly)

- Any change to `internal/WAL/FL`, the existing segment format, or
  the LZ4 envelope — preserved unchanged so the existing crash-recovery
  path is untouched.
- `PRAGMA journal_mode = WAL` semantics — `planner.go:692` keeps the
  no-op behavior. SLT `wal.test` will pass only after both the
  Engine.Open integration AND the missing-corpus REQ land.
- `-shm` mmap on Windows/macOS — Linux-first per the design baseline;
  non-Linux shm requires an OsMmapCompat shim.
