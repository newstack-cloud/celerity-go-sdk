#!/usr/bin/env bash
set -euo pipefail

# Regenerates the Go stubs from the vendored contract, with buf.
#
# buf is the only tool the protocol needs, and it is the same one the runtime
# repository lints the contract and checks it for compatibility with, so a
# developer's machine and CI cannot disagree about which compiler produced the
# checked-in stubs. The plugins are remote and pinned in proto/buf.gen.yaml, so
# nothing has to be installed beyond buf itself.
#
# The stubs are committed rather than produced at build time, following the
# runtime's reasoning about its own Rust stubs: a build that needs a code
# generator needs it in every matrix leg of every release workflow, and a leg
# that misses it fails at release rather than in CI.
#
# Changing proto/ without running this leaves the two out of step, which
# scripts/check-proto.sh catches.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

OUT_DIR="${1:-$ROOT/internal/ipcproto/celerityv1}"

if ! command -v buf > /dev/null; then
  cat <<'MISSING'
buf is not on PATH.

  brew install bufbuild/buf/buf

or see https://buf.build/docs/installation
MISSING
  exit 1
fi

staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

(cd "$ROOT/proto" && buf generate --template buf.gen.yaml --output "$staging" .)

# paths=source_relative mirrors the proto's own directory structure, which is
# flattened here: the stubs are one Go package, not three nested directories.
mkdir -p "$OUT_DIR"
rm -f "$OUT_DIR"/*.pb.go
mv "$staging"/gen/celerity/runtime/v1/*.go "$OUT_DIR/"

echo "generated stubs in $OUT_DIR"
