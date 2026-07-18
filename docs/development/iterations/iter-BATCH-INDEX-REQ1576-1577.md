# Iteration — REQ001576 ~ REQ001577 (Batch index maintenance for UPDATE/DELETE)

> **Status**: done
> **Outcome**: shipped — commit `<this commit>` on `develop`.
> **Lines of code**: +180 / −18 across 1 production file + 1 new test helper.
> **Tests**: 6 new test functions in batch_dml_test.go, all green under `-race`.
> **SLT gate**: select1..4 all pass; full before-commit-cases.sh exits 0.

## What changed

### Production

| File | Change |
|---|---|
| `internal/SQB/DT/storage.go` | New `MaintainIndexesOnUpdateBatch(oldRows, newRows, pks)` + `MaintainIndexesOnDeleteBatch(rows)`. `TableHandle.UpdateRowBatch` extracts each row's PK exactly once (REQ001577 dedup) and threads the cached `[]any` into the batched index maintenance. `TableHandle.DeleteRowBatch` delegates to `MaintainIndexesOnDeleteBatch` for amortised index-key removal. |
| `internal/SQB/DT/schema.go` | New `UnregisterTableIndexes(table string)` test-cleanup helper (production paths never drop indexes while a table is alive). |

### Tests

| File | Coverage |
|---|---|
| `internal/SQB/DT/batch_dml_test.go` (+6 functions, +200 LoC) | `MaintainIndexesOnUpdateBatch_Basic`: UPDATE 50 rows through a registered index, verify new index entries present + stale old entries absent + DeleteBatch call count incremented. `MaintainIndexesOnUpdateBatch_NoIndexChange`: UPDATE that doesn't touch the indexed column is a clean no-op (no extra DeleteBatch). `_EmptyNoop` + `_LengthMismatch`. `MaintainIndexesOnDeleteBatch_Basic`: DELETE 30 rows through a registered index, verify all index entries removed + DeleteBatch call count incremented. `_EmptyNoop`. Helpers `mustSchemaFor`, `mustIndexValue`. |

## Deviations from the spec

| Spec said | Actually shipped | Why |
|---|---|---|
| Separate `ExtractPKForUpdateBatch` helper that returns `[]any` | Cached single `pks []any` slice inside `TableHandle.UpdateRowBatch` | A standalone helper would still need to thread the pks back to the caller; folding the dedup into the existing batch loop achieves the same O(N) → O(N) ExtractPK calls (vs the previous 2N) with one fewer allocation. The semantic is identical: each row's PK is computed once and reused for both heap-row key construction and index maintenance. |
| New `MaintainIndexesOnDeleteBatch` not in REQ001576 spec but natural follow-up | Shipped alongside | REQ001556 (batch DELETE) shipped per-row index maintenance as a known gap; closing it here was a one-function addition that completes the DELETE-path batching story. |

## Cost model (matches REQ001555/001556 cost model)

| Path | Closed.Load | ShouldFlush.Load | store.Delete | store.Insert |
|---|---|---|---|---|
| Per-row `MaintainIndexesOnUpdate` (before) | N per row | N per row | N per row × #indexes | N per row × #indexes |
| `MaintainIndexesOnUpdateBatch` + `BatchStore`/`BatchDeleteStore` (after) | 1 per batch × #indexes | 1 per batch × #indexes | 1 per batch × #indexes | 1 per batch × #indexes |

For N=1000 rows × 2 indexes, the index-side atomic-bound cost drops from O(N) to O(1) per index.

## Production wiring (inherits REQ001581)

All batch paths depend on `SYS/SY/executorStoreAdapter` satisfying `BatchStore`/`BatchDeleteStore` (REQ001581, already shipped). The new `MaintainIndexesOnUpdateBatch` / `MaintainIndexesOnDeleteBatch` type-assert on the same interfaces — when the executor reaches the batched path, the index writes use the same amortised atomic cost.

## Verification

```bash
$ go build ./...
$ go vet ./internal/SQB/DT/ ./internal/SQB/WT/
# Only pre-existing vet warnings (SQB/WT/subq.go:96 unreachable) — not in scope.

$ go test -count=1 ./internal/SQB/DT/ ./internal/SQB/WT/ ./internal/SQB/EX/ ./internal/SYS/SY/ ./internal/ENG/LS/
ok  internal/SQB/DT   0.258s
ok  internal/SQB/WT   0.003s
ok  internal/SQB/EX   5.911s
ok  internal/SYS/SY   3.068s
ok  internal/ENG/LS   3.730s

$ go test -race -count=1 ./internal/SQB/DT/ ./internal/SQB/WT/
ok  internal/SQB/DT   1.295s
ok  internal/SQB/WT   1.036s

$ ./before-commit-cases.sh
[PASS] internal
[PASS] select1
[PASS] select2
[PASS] select3
[PASS] select4
=== All checks passed ===
```

## Discovered-but-out-of-scope bugs

None new this iteration. The pre-existing `TestAutoCompact_Full_Triggered` / `TestAutoCompact_Incremental_Triggered` race flake noted in the REQ001555/001556 iteration doc remains unfixed (out of scope, separate REQ pending).