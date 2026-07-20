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

# Fast: SLT evidence files (~500ms each)
FAST_SLT_CASES=(
  "in1:evidence/in1"
  "in2:evidence/in2"
  "aggfunc:evidence/slt_lang_aggfunc"
  "createtrigger:evidence/slt_lang_createtrigger"
  "createview:evidence/slt_lang_createview"
  "dropindex:evidence/slt_lang_dropindex"
  "droptable:evidence/slt_lang_droptable"
  "droptrigger:evidence/slt_lang_droptrigger"
  "dropview:evidence/slt_lang_dropview"
  "reindex:evidence/slt_lang_reindex"
  "replace:evidence/slt_lang_replace"
  "update:evidence/slt_lang_update"
  "idx1000_2:index/random/1000/slt_good_2"
  "idx1000_3:index/random/1000/slt_good_3"
  "idx1000_4:index/random/1000/slt_good_4"
  "rsel125:random/select/slt_good_125"
  "rsel126:random/select/slt_good_126"
)

for case_def in "${FAST_SLT_CASES[@]}"; do
  label="${case_def%%:*}"
  path="${case_def##*:}"
  run_step "$label" "SLT $path" \
    go test -tags slt_corpus -run "TestSLT_PerFile/$path" -count=1 -timeout 30s ./slt/
  quick_fail
done

# Medium: select1/2 (~60s each)
SELECT_FAST_CASES=(
  "select1:select1"
  "select2:select2"
)

for case_def in "${SELECT_FAST_CASES[@]}"; do
  label="${case_def%%:*}"
  path="${case_def##*:}"
  run_step "$label" "SLT $path" \
    go test -tags slt_corpus -run "TestSLT_PerFile/$path" -count=1 -timeout 60s ./slt/
  quick_fail
done

# Slow: select3/4 (~300s / ~600s)
run_step "select3" "SLT select3" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/select3' -count=1 -timeout 300s ./slt/

run_step "select4" "SLT select4" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/select4' -count=1 -timeout 600s ./slt/

if [ "$FAILED" -eq 0 ]; then
  echo "=== All checks passed ==="
else
  echo "=== Some checks FAILED ==="
  exit 1
fi