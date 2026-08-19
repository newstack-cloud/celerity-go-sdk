#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/modules.sh"

VET_ONLY=""
for arg in "$@"; do
  case $arg in
    --vet-only) VET_ONLY=yes ;;
    -h|--help)
      cat <<USAGE
Runs go vet and staticcheck over every module.

Usage:
  bash scripts/lint.sh [--vet-only]

Reports are written to the repository root for SonarCloud to pick up:
  govet-report.out, staticcheck.out
USAGE
      exit 0
      ;;
  esac
done

cd "$ROOT"
: > govet-report.out
: > staticcheck.out

# Both reports are printed whatever the outcome. A failing lint run whose output
# is only in a file nobody opens is a failing run nobody can act on.
function report {
  echo ""
  echo "go vet:"
  cat govet-report.out
  echo ""
  echo "staticcheck:"
  cat staticcheck.out
}
trap report EXIT

status=0

for module in "${MODULES[@]}"; do
  echo "vetting $module"
  if ! (cd "$ROOT/$module" && go vet ./... 2>> "$ROOT/govet-report.out"); then
    status=1
  fi
done

if [ -n "$VET_ONLY" ]; then
  exit $status
fi

for module in "${MODULES[@]}"; do
  echo "checking $module"
  # Generated stubs are excluded: staticcheck has opinions about code no human
  # wrote and no human may edit.
  if ! (cd "$ROOT/$module" && staticcheck $(go list ./... | grep -v '/internal/ipcproto') \
      >> "$ROOT/staticcheck.out"); then
    status=1
  fi
done

exit $status
