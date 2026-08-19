#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/modules.sh"

for module in "${MODULES[@]}"; do
  echo "scanning $module"
  (cd "$ROOT/$module" && govulncheck ./...)
done
