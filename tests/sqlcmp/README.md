# tests/sqlcmp

SQLite compatibility test suite for Razordata. Two complementary
tracks:

- **SLT** (`slt/`) — pure-Go driver for the SQLLogicTest format
  used by the upstream corpus at
  `https://www.sqlite.org/sqllogictest`. The corpus is mirrored
  as a git submodule under `corpus/`; the driver parses and
  runs `.test` files and diffs Razordata's output against the
  expected results.

## Running

Default test run (no corpus required):

```
go test ./...
```

Run the SLT driver without the corpus:

```
go test ./tests/sqlcmp/slt/... -v
```

To exercise the corpus, initialize the submodule and pass the
`slt_corpus` build tag:

```
git submodule update --init --depth 1 tests/sqlcmp/corpus
go test -tags slt_corpus ./tests/sqlcmp/slt/... \
    -run TestSQLLogicTest_CorpusSubset -v
```

Override the corpus location with `RAZOR_SLT_ROOT`:

```
RAZOR_SLT_ROOT=/path/to/sqllogictest go test -tags slt_corpus \
    ./tests/sqlcmp/slt/... -run TestSQLLogicTest_CorpusSubset
```

## Pass-Rate Threshold

`tests/sqlcmp/slt/subset.go` carries a hand-calibrated
`corpusSubsetThreshold` constant. The default of `0.30` reflects
the v0.25.0 baseline (most failures are deliberately-skipped
SQL features that ship in later iterations). Re-baseline after
each release.
