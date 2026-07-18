# Iteration — REQ001557 (Pre-allocate RETURNING row buffers)

> **Status**: done
> **Outcome**: shipped — commit `<this commit>` on `develop`.
> **Lines of code**: +108 / −10 across 1 production file + 2 test/chore files.
> **Tests**: `go test ./internal/... -count=1` all pass; benchmark runs clean.
> **SLT gate**: select1 passes.

## What changed

### Production

| File | Change |
|---|---|
| `internal/SQB/WT/writers_dml.go` | Added `rColsBuf`/`rTypesBuf`/`rDataBuf` flat backing arrays to `Insert`, `Update`, `Delete` structs. Initialized in `NewInsertWithStore`/`NewUpdateWithStore`/`NewDeleteWithStore` with 256-row capacity. Replaced per-row `make` in `evalReturning` with flat-buffer allocation via `growSlice` generic helper. Updated all 10 call sites to pass buffer pointers. |

### Tests

| File | Coverage |
|---|---|
| `internal/SYS/SY/returning_bench_test.go` (new) | `BenchmarkReturningUpdateNoAlloc` — 500-row `UPDATE t SET v = v + 1` with RETURNING, measures allocations. |

## Deviations from the spec

None.

## Verification

```bash
$ go build ./...
$ go test ./internal/... -count=1 -timeout 300s
# 30 packages all pass
$ go test -tags slt_corpus -run 'TestSLT_PerFile/select1' -count=1 ./tests/sqlcmp/slt/
ok
```

## Notes

- The `growSlice[T any]` generic helper (`writers_dml.go:18–28`) is shared with any future flat-buffer allocation REQs (e.g. REQ001558–001562, 001567, 001572–001573). It grows a slice to length `n` with doubling capacity, avoiding per-call `make` overhead in the steady state.
- The flat buffers use three-index slice expressions (`buf[flatOff:newLen:newLen]`) so each result row's header has a capacity bound to exactly `nCols`, preventing accidental `append` from writing into adjacent rows' backing storage.
