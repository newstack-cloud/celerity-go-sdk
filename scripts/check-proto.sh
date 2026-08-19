#!/usr/bin/env bash
set -euo pipefail

# Fails when the committed stubs do not match what the vendored contract
# generates, and when the vendored contract has fallen behind the runtime's.
#
# Both are committed files that can drift silently, and drift in either is a
# protocol mismatch that would first show up as a handshake failure against a
# real runtime.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

bash "$SCRIPT_DIR/gen-proto.sh" "$staging" > /dev/null

if ! diff -ru "$ROOT/internal/ipcproto/celerityv1" "$staging" \
    --exclude="*_test.go" > "$staging.diff"; then
  echo "the generated stubs are out of step with proto/:"
  cat "$staging.diff"
  echo ""
  echo "run: bash scripts/gen-proto.sh"
  echo "and commit the .proto and the regenerated stubs together."
  exit 1
fi

echo "generated stubs are in step with the contract"

# The upstream copy is only checked when it is available: a contributor without
# the runtime repository checked out should not be blocked by its absence, and
# CI supplies it explicitly.
UPSTREAM="${CELERITY_RUNTIME_PROTO:-}"
if [ -z "$UPSTREAM" ] || [ ! -f "$UPSTREAM" ]; then
  echo "skipping the upstream contract check, set CELERITY_RUNTIME_PROTO to enable it"
  exit 0
fi

if ! diff -u "$UPSTREAM" "$ROOT/proto/celerity/runtime/v1/runtime.proto"; then
  echo ""
  echo "the vendored contract has fallen behind the runtime's."
  echo "copy the runtime's proto over proto/, run scripts/gen-proto.sh, and"
  echo "review the generated diff: it is the clearest signal of whether the"
  echo "change was additive or breaking."
  exit 1
fi

echo "the vendored contract matches the runtime's"
