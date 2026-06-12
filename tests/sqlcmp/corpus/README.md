# SLT Corpus Mirror

The `tests/sqlcmp/corpus/` directory is intended to be populated with
the SQLite SQLLogicTest corpus, mirrored as a git submodule from
`https://github.com/MarvBeer/sqlite-test-suite` (community mirror of
the upstream fossil repository at `https://www.sqlite.org/sqllogictest`).

The submodule is gated by the `slt_corpus` build tag in Go test files.
Default `go test ./...` does **not** require the submodule.

To initialize the submodule locally:

```
git submodule update --init --depth 1 tests/sqlcmp/corpus
```

To run the full corpus (nightly):

```
go test ./tests/sqlcmp/slt/... -tags 'slt_corpus slt_corpus_full' -v
```

To run the curated PR subset (~200 files, ~60s):

```
go test ./tests/sqlcmp/slt/... -tags slt_corpus -run TestSQLLogicTest_CorpusSubset -v
```

The curated subset list is in `tests/sqlcmp/slt/subset.go`. The
pass-rate threshold is calibrated per release.
