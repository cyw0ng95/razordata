#!/bin/bash
set -uo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
FAILED=0

run_step() {
  local label="$1" desc="$2"
  shift 2
  echo "=== $desc ==="
  if "$@"; then
    echo "[PASS] $label"
  else
    echo "[FAIL] $label"
    FAILED=1
  fi
  echo ""
}

quick_fail() {
  if [ "$FAILED" -eq 1 ]; then
    echo "=== Quick fail: aborting ==="
    exit 1
  fi
}

# Fast: core engine tests
run_step "internal" "go test ./internal/..." \
  go test ./internal/... -count=1 -timeout 180s "$@"
quick_fail

# Fast: SQL parser/rewriter tests (no SLT corpus needed)
run_step "sqlcmp" "go test ./tests/sqlcmp/ -run 'Test(DDL|DML|Select|Lexer|Rewriter)'" \
  go test ./tests/sqlcmp/ -count=1 -timeout 30s -run 'Test(DDL|DML|Select|Lexer|Rewriter)' "$@"
quick_fail

cd "$ROOT/tests/sqlcmp"

# SLT included cases — all 248 cases in a SINGLE go-test invocation
# via TestSLT_InList. Per-case results shown as subtests with -v;
# quick-fail on first error. No overall timeout — per-case limits
# are set in includedCases (select4 gets 30 min).
run_step "slt-inlist" "SLT included cases (248 files, 1 invocation)" \
  go test -v -tags slt_corpus -run TestSLT_InList -count=1 ./slt/
quick_fail

if [ "$FAILED" -eq 0 ]; then
  echo "=== All checks passed ==="
else
  echo "=== Some checks FAILED ==="
  exit 1
fi