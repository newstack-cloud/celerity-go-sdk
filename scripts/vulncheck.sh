#!/usr/bin/env bash
set -uo pipefail

# Deliberately not -e. govulncheck exits non-zero when it finds something, and
# stopping there would leave every later module unscanned while the run still
# reported the findings it did have. Every module is scanned, and the exit
# status is the worst of them.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/modules.sh"

status=0
affected=()

for module in "${MODULES[@]}"; do
  echo "scanning $module"
  if ! (cd "$ROOT/$module" && govulncheck ./...); then
    status=1
    affected+=("$module")
  fi
done

if [ "$status" -ne 0 ]; then
  echo
  echo "vulnerabilities found in: ${affected[*]}"
fi

exit "$status"
