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

# ── Fast: core engine tests ──
run_step "internal" "go test ./internal/..." \
  go test ./internal/... -count=1 -timeout 180s "$@"
quick_fail

cd "$ROOT/tests/sqlcmp"

# ── Fast: evidence SLT files (~500ms each) ──
run_step "evidence/in1" "SLT evidence/in1" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/in1' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/in2" "SLT evidence/in2" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/in2' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/aggfunc" "SLT evidence/slt_lang_aggfunc" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_aggfunc' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/createtrigger" "SLT evidence/slt_lang_createtrigger" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_createtrigger' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/createview" "SLT evidence/slt_lang_createview" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_createview' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/dropindex" "SLT evidence/slt_lang_dropindex" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_dropindex' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/droptable" "SLT evidence/slt_lang_droptable" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_droptable' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/droptrigger" "SLT evidence/slt_lang_droptrigger" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_droptrigger' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/dropview" "SLT evidence/slt_lang_dropview" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_dropview' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/reindex" "SLT evidence/slt_lang_reindex" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_reindex' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/replace" "SLT evidence/slt_lang_replace" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_replace' -count=1 -timeout 30s ./slt/
quick_fail

run_step "evidence/update" "SLT evidence/slt_lang_update" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/evidence/slt_lang_update' -count=1 -timeout 30s ./slt/
quick_fail

# ── Medium: select1/2 (~60s each) ──
run_step "select1" "SLT select1" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/select1' -count=1 -timeout 60s ./slt/
quick_fail

run_step "select2" "SLT select2" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/select2' -count=1 -timeout 60s ./slt/
quick_fail

# ── Slow: select3/4 (~300s / ~600s) ──
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