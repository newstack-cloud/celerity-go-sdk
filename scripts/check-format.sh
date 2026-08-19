#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Generated stubs are formatted by their generator and are not ours to reformat.
unformatted=$(cd "$ROOT" && gofmt -l . | grep -v '^internal/ipcproto/' || true)

if [ -n "$unformatted" ]; then
  echo "these files are not gofmt-formatted:"
  echo "$unformatted"
  echo ""
  echo "run: gofmt -w <file>"
  exit 1
fi

echo "all files are formatted"
