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

# REQ002103: detect under-resourced environments (CI runners,
# small dev VMs) and skip the SLT corpus pass — it requires 4 GB+
# of RAM and 3+ cores to complete within the wall-clock budget on
# its slowest single file (select4 takes ~40s on 2 cores). CPU is
# read from `nproc` (fallback to /proc/cpuinfo). Memory is read
# from /proc/meminfo MemTotal (Linux); on macOS fall back to
# sysctl hw.memsize.
detect_cpu_count() {
  if command -v nproc >/dev/null 2>&1; then
    nproc
  elif [ -r /proc/cpuinfo ]; then
    grep -c ^processor /proc/cpuinfo
  else
    echo 1
  fi
}

detect_mem_gb() {
  if [ -r /proc/meminfo ]; then
    awk '/^MemTotal:/ { printf "%d\n", $2 / 1024 / 1024 }' /proc/meminfo
  else
    if command -v sysctl >/dev/null 2>&1; then
      sysctl -n hw.memsize 2>/dev/null | awk '{ printf "%d\n", $1 / 1024 / 1024 / 1024 }'
    else
      echo 0
    fi
  fi
}

CPU_COUNT=$(detect_cpu_count)
MEM_GB=$(detect_mem_gb)
SKIP_SLT=0

if [ "$CPU_COUNT" -le 2 ] || [ "$MEM_GB" -le 4 ]; then
  SKIP_SLT=1
fi

echo "=== Environment: CPUs=$CPU_COUNT MemTotal=${MEM_GB}GB SLT_SKIP=$SKIP_SLT ==="
echo ""

# Fast: core engine tests
run_step "internal" "go test ./internal/..." \
  go test ./internal/... -count=1 -timeout 180s "$@"
quick_fail

# REQ002140: shadow validation — compare pipeline vs legacy results
run_step "px_validate" "go test -tags px_validate ./internal/..." \
  go test -tags px_validate ./internal/... -count=1 -timeout 180s "$@"
quick_fail

# Fast: SQL parser/rewriter tests (no SLT corpus needed)
run_step "sqlcmp" "go test ./tests/sqlcmp/ -run 'Test(DDL|DML|Select|Lexer|Rewriter)'" \
  go test ./tests/sqlcmp/ -count=1 -timeout 30s -run 'Test(DDL|DML|Select|Lexer|Rewriter)' "$@"
quick_fail

cd "$ROOT/tests/sqlcmp"

# SLT included cases — all 385 cases in a SINGLE go-test invocation
# via TestSLT_InList. Per-case results shown as subtests with -v;
# quick-fail on first error. No overall timeout — per-case limits
# are set in includedCases (select4 gets 30 min).
if [ "$SKIP_SLT" -eq 1 ]; then
  echo "=== SKIP slt-inlist: under-resourced environment (CPUs=$CPU_COUNT <= 2 or MemTotal=${MEM_GB}GB <= 4) ==="
  echo ""
else
  run_step "slt-inlist" "SLT included cases (385 files, 1 invocation)" \
    go test -v -tags slt_corpus -run TestSLT_InList -count=1 ./slt/
  quick_fail
fi

if [ "$FAILED" -eq 0 ]; then
  echo "=== All checks passed ==="
else
  echo "=== Some checks FAILED ==="
  exit 1
fi