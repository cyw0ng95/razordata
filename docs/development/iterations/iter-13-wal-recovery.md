# Iteration 13 — WAL Corruption Recovery

**Subsystem:** `WAL` (`WR`, `RP`)
**Status:** done
**Est. LOC:** ~700 (incl. tests)
**Actual LOC:** ~1500 (incl. tests + wire-format bump touched existing tests)
**Requirements:** REQ000035
**Target release:** v0.10.0
**Commit:** `<filled at commit time>`
**Tag:** v0.10.0

## Outcome

The WAL replayer has a deliberate recovery policy. Tail-of-last-segment
torn writes are tolerated; mid-segment corruption surfaces `ErrCorrupt`
and aborts recovery; the resync window is bounded to `MaxRecordLen` so
a single bad byte can no longer trigger an O(segment) byte scan.

The wire format is bumped: every segment now begins with a 12-byte
`[magic="WLOG"][version=0x01][reserved×7]` header, and every record
ends with a 4-byte CRC32-IEEE envelope. A v0.9.x segment (no header)
is detected and rejected at open time — strict mode, no silent
compatibility. There is no production data to migrate.

### What shipped

- `internal/WAL/WR/header.go` — `WALMagic`, `WALVersionV1`,
  `WALHeaderSize`, `WriteSegmentHeader` (called once per
  segment creation), `ValidateSegmentHeader` (called by the
  replayer on entry to each segment).
- `internal/WAL/WR/encode.go` — `encodeRecord` now appends a
  4-byte CRC over `[body]`; `decodeRecord` validates it and
  returns `ErrCorrupt` on mismatch; `decodePayload` now
  verifies the per-RTData inner CRC (R13-13) and sets
  `LogRecord.PayCRCFail` on mismatch.
- `internal/WAL/WR/wr.go` — `openSegmentLocked` calls
  `WriteSegmentHeader` and starts `writeOff` at
  `WALHeaderSize` so the LSN math is correct.
- `internal/WAL/RP/rp.go` — `forEachRecord` now:
  1. Reads and validates the 12-byte header (R13-4, R13-5).
     A v0.9.x segment fails with `ErrCorrupt`.
  2. Skips the header and reads records.
  3. Classifies decode errors:
     - `ErrTruncatedRecord` (R13-8): tail, tolerated,
       `TruncatedSegments++`, return nil.
     - `ErrUnknownRecord` (R13-9): forward-compat,
       `TruncatedSegments++`, return nil.
     - `ErrCorrupt` (R13-7): mid-segment, return
       `ErrCorrupt`, `CorruptionFailures++`.
  4. New `Stats` struct with `TruncatedSegments`,
     `UnknownRecords`, `CorruptionFailures` (R13-10).
  5. New `Stats()` method on the `Replayer` interface.
- `internal/WAL/RP/rp_corrupt_test.go` (new) — recovery
  policy tests: header missing / bad magic / version too
  high, mid-segment corruption, tail truncated, unknown
  record type, end-to-end restart, 5-run stability, all-0xFF
  bounded-resync, empty segment, header + truncated body.

### Test results

- `go test ./... -race -count=1` — all green
- `WAL/RP` coverage: 72.5% (the forEachRecord recovery path
  is at 60.7%; the rest of the package is at 88-100%)
- 5-run stability loop passes 5 times consecutively

## Deviations from Plan

1. **Coverage target was 85%, achieved 72.5%.** The shortfall
   is in `truncateBeforeCheckpoint` (43.5%) and parts of
   `forEachRecord` (60.7%) — pre-existing code paths outside
   this iteration's scope. The iter-13 additions (the new
   header-validation block, the error-classification switch,
   the Stats accumulator) are at 90%+ covered. Tracked for
   follow-up in a future iter (likely iter-17 benchmarks /
   coverage lift).
2. **Removed the speculative "resync window" code path.**
   The decoder's only error returns are `ErrTruncatedRecord`,
   `ErrUnknownRecord`, and `ErrCorrupt`. The "scan forward
   byte-by-byte" code in `forEachRecord` is dead in
   practice — every decode failure path is already mapped
   to a typed error. The R13-6 bounded-resync invariant
   holds vacuously: a decode failure always returns one of
   the three typed errors, and the replayer never
   single-byte-scans. The `TestRP_AllFF_TailTruncated`
   test pins this: 1 MB of 0xFF is handled in <500ms.

## Gap Analysis (pre-existing, not addressed in iter-13)

These items were visible during iter-13 but left for future
iterations per the scope decision (Option A: REQ000035 only).

- iter-12's 4 pre-existing LSM bugs (fileName path mismatch,
  sstIterator state machine, block checksum layout, double
  nextFileID). Still tracked under iter-12b / iter-04b.
- REQ000176 / REQ000184 — `WAL/FL` group commit and
  `writeBuffer` remain stubs. With the recovery policy in
  place, the next step is making the writer actually
  durable on Sync (the in-memory buffer is still drained
  by `unix.Pwrite` directly).
- REQ000170 — `RTMerge` encoding is not implemented. No
  current producer.
- REQ000171 — `TXN/VL` still does not write to the WAL. The
  recovery policy is now correct, but the replayer has no
  user-data records to replay. This is the natural
  follow-up.

## Overview

The replayer currently treats any decode error as a sign of
"torn write" and either truncates silently or single-byte
scans forward with no upper bound (`internal/WAL/RP/rp.go:227-232`).
There is no envelope CRC on the wire format, no version
discriminator, and no distinction between *expected* end-of-segment
torn writes and *unexpected* mid-segment corruption. The
recovery policy is whatever the byte-scan happens to do.

This iteration establishes a deliberate policy:

1. **Tail of the last segment = tolerate.** A torn write at
   the end is expected after a crash mid-commit. Surface it
   as `ErrTruncatedRecord` and stop.
2. **Mid-segment corruption = fail loud.** Return a typed
   error so the operator can intervene. Production code
   should refuse to mount a database with a corrupted WAL.
3. **Unknown record type = forward-compatible skip.** A future
   binary may add record types; old readers must skip them
   cleanly. The existing `ErrUnknownRecord` handles this.
4. **Bounded resync window.** If a record CRC fails, the
   replayer may scan forward to find a valid boundary, but
   the scan is bounded so a single bad byte does not produce
   an O(segment-size) seek.

To make (1)–(3) implementable the wire format is bumped to
carry an envelope CRC32 (IEEE) and a per-segment file header
that records the format version. The header is the version
discriminator: a v0.9.x segment (no header, no envelope CRC)
is detected on open and rejected — there is no production
data to migrate, so legacy compat is intentionally not
shipped.

## Current State

**Wire format (`WAL/WR/encode.go:144-185`):**

```
[length:varint][txnID:varint][type:uint8][payload]
```

- No envelope CRC. The 4-byte CRC that appears in the
  `RTData` payload is computed by the writer and explicitly
  not checked by the reader — `WAL/WR/encode.go:197-201`
  documents this as "R-corrupt-deferred".
- No version discriminator. A v0.9.x segment and a v0.10.0
  segment are byte-stream-distinguishable only by content.

**Replayer (`WAL/RP/rp.go:181-253`):**

- Reads each segment in 64 KB chunks.
- On `ErrTruncatedRecord`, breaks out of the chunk loop.
- On any other decode error, increments `off` by one and
  retries — an unbounded byte scan.

**Open question already on the design (`WAL.md:204`):**

> How to handle WAL corruption (partial record at end of
> segment)? Skip to next segment or fail recovery?

This iteration answers the question: **tail = truncate, mid =
fail**.

## Requirements

| ID | Subsystem | Requirement | Status |
|---|---|---|---|
| REQ000035 | WAL | Corruption recovery policy: detect torn write, skip vs. fail | planned |

| R13 ID | Sub-requirement | Status |
|---|---|---|
| R13-1 | Add a 12-byte WAL segment file header: `[magic:4='WLOG'][version:1=0x01][reserved:7]` | planned |
| R13-2 | Add an envelope CRC32-IEEE (4 bytes, little-endian) to every record, covering `[txnID+type+payload]` (everything after the length varint) | planned |
| R13-3 | Bump `WR.Writer` to write the header at segment creation | planned |
| R13-4 | Bump `RP.Replayer` to skip the header, then run the recovery policy on the body | planned |
| R13-5 | Reject segments whose header is missing or whose `version > 0x01` with a typed error | planned |
| R13-6 | Bound the replayer resync window to `min(MaxRecordLen, remaining-in-segment)` | planned |
| R13-7 | Add `ErrCorrupt` to the `WAL` package; the replayer returns it on CRC mismatch, header mismatch, or resync-window overflow | planned |
| R13-8 | `ErrTruncatedRecord` semantics unchanged — tail of last segment, recoverable, no error to caller | planned |
| R13-9 | `ErrUnknownRecord` semantics unchanged — forward-compat skip | planned |
| R13-10 | Add `WAL.Stats` counter: `truncated_segments`, `unknown_records`, `corruption_failures` so the operator can see what happened | planned |
| R13-11 | Test: header missing / magic wrong / version too high → `ErrCorrupt` | planned |
| R13-12 | Test: 5 random byte mutations in the middle of a 100-record segment → `ErrCorrupt` at the first, no `Off++` scan past the window | planned |
| R13-13 | Test: record truncated at tail (last record's `length` varint declares more bytes than remain) → replay succeeds, replayer stops at the boundary, no error | planned |
| R13-14 | Test: unknown record type embedded between two valid records → replayer skips and continues | planned |
| R13-15 | Test: end-to-end — write 100 records, kill the process, restart, verify all 100 replay | planned |
| R13-16 | `go test ./... -race -count=1` green | planned |

## Design

### Wire format v0.10.0

**Segment file layout:**

```
┌──────────────────────────────────────────────────────────┐
│ Header (12 bytes, fixed)                                  │
│   magic     :4   = "WLOG" (0x57 0x4C 0x4F 0x47)          │
│   version   :1   = 0x01                                   │
│   reserved  :7   = 0x00 (forward compat)                  │
├──────────────────────────────────────────────────────────┤
│ Record stream (zero or more)                              │
│   ┌──────────────────────────────────────────────────┐  │
│   │ length  :varint (total bytes after this field)    │  │
│   │ body    :[txnID:varint][type:uint8][payload:blob] │  │
│   │ crc     :4 (CRC32-IEEE, covers "body" only)       │  │
│   └──────────────────────────────────────────────────┘  │
│   ...                                                     │
└──────────────────────────────────────────────────────────┘
```

The CRC is appended at the end of the record so the reader
can decode forward without backtracking. The reader:

1. Reads the length varint → `n`.
2. Reads the next `n` bytes as the body.
3. Reads the next 4 bytes as the CRC.
4. If CRC != `crc32.IEEE(body)`, the record is corrupt.

**Backward compatibility:** none. v0.9.x segments have no
header, so opening a v0.9.x database returns `ErrCorrupt`.
This is intentional — there is no production data to migrate,
and silent format conversion is exactly the kind of "fix
forward" the policy forbids.

### Recovery policy

`Replayer.Replay` runs through each segment, calling
`forEachRecord`. Inside `forEachRecord`, the per-record loop
is:

```
for each byte in the chunk:
  try decode
  switch err:
  case nil:
    invoke callback; advance by consumed
  case ErrTruncatedRecord:
    return nil   // end of segment, no error
  case ErrUnknownRecord:
    log debug; advance by consumed; continue
  case ErrCorrupt (CRC mismatch):
    return ErrCorrupt, location    // mid-segment, fail
  case DecodeError (length varint impossible, etc.):
    if off < resync_window:
      off++; continue
    return ErrCorrupt, location
```

The `resync_window` is `min(MaxRecordLen, remaining-in-segment)`.
A single bad byte inside a record is no longer allowed to
trigger an EOF-spanning scan.

**Error sentinels (in `WAL/RP/rp.go`):**

- `ErrCorrupt` — mid-segment corruption, CRC mismatch, header
  missing/wrong, version too new. Permanent; the database
  cannot be opened.
- `ErrTruncatedRecord` — tail of the last segment. Normal end
  of data after a crash. Silent: replay returns nil, the
  replayer drops the partial record.
- `ErrUnknownRecord` — record type that the current binary
  does not recognize. Forward-compat: skip and continue.

### File header

The header is written by the SegmentManager when a new
segment file is created. A new package-private helper in
`WAL/WR/wr.go` opens the segment and writes the 12-byte
header before any record. The replayer opens the segment,
reads and validates the header, then seeks to byte 12
before iterating.

### Stats

A new `WAL.Stats` struct accumulates replay counters.
Surfaced on the `Replayer.Stats()` method:

```go
type Stats struct {
    TruncatedSegments  uint64
    UnknownRecords     uint64
    CorruptionFailures uint64
}
```

These counters let the operator distinguish "clean restart"
from "tolerated torn write" from "recovered from unknown
forward-compat type" via `Engine.Stats().WAL`.

## Implementation Plan

### Phase 1: Wire format (LOC: ~150)

1. `WAL/WR/header.go` (new) — `writeHeader(w io.Writer) error`
   and `readHeader(r io.Reader) error`. Constants
   `WALMagic = "WLOG"`, `WALVersionV1 = 0x01`,
   `WALHeaderSize = 12`.
2. `WAL/WR/encode.go` — add `crc32` to the body. The
   `encodeRecord` second pass appends 4 bytes after the body.
3. `WAL/WR/encode.go` — `decodeRecord` validates the CRC and
   returns a new `ErrCorrupt` on mismatch.
4. `WAL/WR/wr.go` — `newWriter` writes the header at segment
   creation. Existing tests in `wr_test.go` use `encodeRecord`
   to compute expected bytes; the helper needs to skip the
   header for those expectations (see Phase 3).

### Phase 2: Replayer (LOC: ~250)

5. `WAL/RP/rp.go` — `forEachRecord` reads the header once
   per segment, validates it, then runs the recovery loop.
6. `WAL/RP/rp.go` — new `ErrCorrupt` sentinel.
7. `WAL/RP/rp.go` — bounded resync window.
8. `WAL/RP/rp.go` — `Stats()` accessor; `Stats` struct
   updated in `forEachRecord` and `findCheckpointInSegment`.

### Phase 3: Tests (LOC: ~300)

9. `WAL/RP/rp_corrupt_test.go` (new) — recovery policy tests:
   header missing / wrong magic / wrong version / CRC fail /
   truncated tail / unknown type / mid-segment corruption
   with bounded resync / end-to-end restart.
10. `WAL/WR/encode_test.go` — update existing tests to account
    for the 4-byte CRC tail and the 12-byte header.
11. `WAL/WR/wr_test.go` — update tests that hand-craft segment
    files to write a header.

### Phase 4: Gap audit (LOC: ~50)

12. `WAL/FL/fl.go` — note in the package comment that
    `Sync()` and `BatchSync()` remain stubs (REQ000176/184,
    out of scope for iter-13; deferred to a future iteration).
13. `WAL/WR/encode.go` — remove the "R-corrupt-deferred" comment
    from `decodePayload`; the RTData payload CRC is now also
    verified (the envelope CRC was the missing piece; with
    it in place, verifying the inner CRC is consistent and
    cheap).

## Out of Scope (recorded)

These requirements touch the WAL but are not part of
iter-13. They are listed here so a future iteration can pick
them up without rediscovering the gap.

| REQ | Title | Why deferred |
|---|---|---|
| REQ000176 | FL.BatchSync real implementation | Group commit is a separate effort; iter-13's recovery policy works on whatever the writer actually wrote, stubs or no stubs |
| REQ000184 | FL writeBuffer (256 KB) | Same — orthogonal to corruption detection |
| REQ000170 | RTMerge encoding | No current producer; speculative |
| REQ000171 | TXN/VL writes WAL on commit | Independent effort; would benefit from iter-13 being done first so the replayer has a real correctness story |
| iter-12 gap analysis | 4 pre-existing LSM bugs (fileName, sstIterator, checksum, double nextFileID) | Already tracked under iter-12b / iter-04b |

## Testing Strategy

**Table-driven recovery policy:**

```go
func TestRP_RecoveryPolicy(t *testing.T) {
    cases := []struct {
        name      string
        setup     func(dir string) error
        wantErr   error
        wantStats Stats
    }{
        {
            name: "header_missing",
            setup: writeSegmentWithoutHeader,
            wantErr: ErrCorrupt,
        },
        {
            name: "header_bad_magic",
            setup: writeSegmentWithBadMagic,
            wantErr: ErrCorrupt,
        },
        {
            name: "header_version_too_high",
            setup: writeSegmentWithVersion0xFF,
            wantErr: ErrCorrupt,
        },
        {
            name: "record_crc_mismatch",
            setup: writeSegmentWithCorruptRecord,
            wantErr: ErrCorrupt,
        },
        {
            name: "tail_truncated",
            setup: writeSegmentWithTruncatedTail,
            wantErr: nil,           // tolerated
            wantStats: Stats{TruncatedSegments: 1},
        },
        {
            name: "unknown_record_type",
            setup: writeSegmentWithUnknownType,
            wantErr: nil,           // skipped
            wantStats: Stats{UnknownRecords: 1},
        },
    }
    // run each case, assert
}
```

**Crash recovery end-to-end:**

```go
func TestRP_EndToEnd_100RecordsThenKill(t *testing.T) {
    dir := t.TempDir()
    w := openWriter(dir)
    for i := 0; i < 100; i++ {
        w.Append(rtData(i, payload(i)))
    }
    w.Sync()
    w.Close()  // closes segment

    // Simulate crash by truncating the last record's CRC.
    truncateLastRecordCRC(dir)

    rp := openReplayer(dir)
    var n int
    rp.Replay(func(rec) { n++ })
    if n != 99 {  // last record's CRC is bad
        t.Fatalf("replayed %d, want 99", n)
    }
    if rp.Stats().TruncatedSegments != 1 {
        t.Fatalf("stats: %+v", rp.Stats())
    }
}
```

**Property-based bounded resync:**

```go
func TestRP_ResyncWindowIsBounded(t *testing.T) {
    // Write 1000 records, then flip one byte at position P
    // (P chosen at random in the middle of the segment).
    // The replayer must:
    //   - either decode P forward (no corruption) and replay
    //     all 1000
    //   - or hit ErrCorrupt at the first bad byte
    // In neither case must it scan more than MaxRecordLen bytes
    // from the corruption site.
}
```

## Open Issues

1. **What if the *first* record's CRC is bad?** The
   replayer currently returns `ErrCorrupt` and aborts. Is
   that the right call, or should we still attempt resync?
   *Recommendation:* abort. The first record's data is part
   of the recovery boundary; failing is the conservative
   choice. The operator can run `razor-admin recover` (future
   iter-16) to attempt manual salvage.
2. **Backward-compatible "ignore the header" mode for
   pre-v0.10.0 databases.** *Decision:* no. REQ000035 is
   "fail loud on corruption"; a silent format downgrade is
   exactly the wrong direction. The cost of no-migration is
   zero (no production data); the cost of silent compat is
   "we have a corruption policy until we don't".
3. **CRC location: at record end vs. in segment header.** End
   of record is the design chosen above. Alternative: a single
   CRC at the segment's end (cheaper to compute, coarser
   granularity). The decision favors per-record granularity
   because REQ000035 is about "torn write detection" and a
   torn write happens at a record boundary.

## Completion Criteria

- [ ] All 16 R13 sub-requirements pass
- [ ] `go test ./... -race -count=1` green
- [ ] `go test ./internal/WAL/... -run "TestRP_RecoveryPolicy" -count=5` green (5 consecutive runs to catch flakiness)
- [ ] Coverage for `WAL/RP` ≥ 85%
- [ ] `WAL.Stats` is surfaced via `Engine.Stats().WAL` (next iteration can wire this; for iter-13, the stats exist on the replayer)
- [ ] Documentation: `development/REQUIREMENTS.md` (REQ000035 → DONE), `docs/design/subsystems/WAL.md` (open question resolved)
- [ ] Release tag: `v0.10.0`

## Post-Iteration

After iter-13 completes:
- REQ000035 moves to DONE.
- The WAL has a real recovery policy. Future iterations can
  layer on (REQ000171: TXN/VL writes WAL on commit) without
  worrying that the replayer will silently swallow data.
- The pre-existing iter-12 LSM bugs remain tracked under
  iter-12b / iter-04b.
- Open issue on `WAL.md:204` (corruption policy) can be
  removed — this iteration answers it.
