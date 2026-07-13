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

run_step "internal" "go test ./internal/..." \
  go test ./internal/... -count=1 -timeout 180s "$@"

cd "$ROOT/tests/sqlcmp"

run_step "select1" "SLT select1" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/select1' -count=1 -timeout 60s ./slt/

run_step "select2" "SLT select2" \
  go test -tags slt_corpus -run 'TestSLT_PerFile/select2' -count=1 -timeout 60s ./slt/

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
