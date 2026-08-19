#!/usr/bin/env bash
set -euo pipefail

# Validates the vendored contract and the stubs generated from it.
#
# Three questions, each of which can go wrong silently:
#
#   1. Is the contract well formed?              buf lint
#   2. Is our copy of it the runtime's copy?     export the upstream and diff
#   3. Do the committed stubs match ours?        regenerate and diff
#
# The contract is owned by the Celerity monorepo, which is public, so the second
# check needs nothing arranged: buf reads the upstream contract straight out of
# the repository. That makes it the authoritative one. Byte-identical is a
# stricter requirement than merely compatible, and it is the right one, because
# this repository is not where the contract is decided.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Any buf input: the public monorepo by default, or a path to a local checkout
# through CELERITY_RUNTIME_PROTO, which is faster and works offline.
UPSTREAM="${CELERITY_RUNTIME_PROTO:-https://github.com/newstack-cloud/celerity.git#branch=main,subdir=libs/runtime/proto}"

if ! command -v buf > /dev/null; then
  echo "buf is not on PATH, see https://buf.build/docs/installation" >&2
  exit 1
fi

staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

echo "linting the contract"
(cd "$ROOT/proto" && buf lint)

echo "checking the vendored contract against the runtime's"
buf export "$UPSTREAM" -o "$staging/upstream"

if ! diff -ru "$staging/upstream/celerity" "$ROOT/proto/celerity"; then
  echo ""
  echo "The vendored contract is not the runtime's."
  echo ""
  # Whether the drift is breaking is the thing worth knowing next: an additive
  # change is a sync, and a breaking one needs a v2 package served alongside v1
  # through a deprecation window.
  echo "Classifying the difference:"
  if (cd "$ROOT" && buf breaking proto --against "$UPSTREAM"); then
    echo "  the difference is backwards compatible."
  else
    echo "  the difference is BREAKING, per the report above."
  fi
  echo ""
  echo "Copy the runtime's contract over proto/, run scripts/gen-proto.sh, and"
  echo "commit the .proto and the regenerated stubs together."
  exit 1
fi

echo "checking the generated stubs are in step with the contract"
bash "$SCRIPT_DIR/gen-proto.sh" "$staging/stubs" > /dev/null

if ! diff -ru "$ROOT/internal/ipcproto/celerityv1" "$staging/stubs" > "$staging/stubs.diff"; then
  echo "the generated stubs are out of step with proto/:"
  cat "$staging/stubs.diff"
  echo ""
  echo "run: bash scripts/gen-proto.sh"
  exit 1
fi

echo "the contract matches the runtime's and the stubs are in step with it"
