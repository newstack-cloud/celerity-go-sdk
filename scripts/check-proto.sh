#!/usr/bin/env bash
set -euo pipefail

# Validates the vendored contract and the stubs generated from it.
#
# Three questions, each of which can go wrong silently:
#
#   1. Is the contract well formed?          buf lint
#   2. Has our copy of it changed in a way
#      that would break a peer?              buf breaking, against main
#   3. Do the committed stubs match it?      generate and diff
#
# and a fourth when the runtime repository is available: has our copy fallen
# behind theirs at all, which is stricter than compatible.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

if ! command -v buf > /dev/null; then
  echo "buf is not on PATH, see https://buf.build/docs/installation" >&2
  exit 1
fi

echo "linting the contract"
(cd "$ROOT/proto" && buf lint)

# Against main rather than against the runtime's copy, so this still says
# something in a repository that has no runtime checkout: it catches an edit to
# the vendored contract that would break a peer already speaking it.
if git -C "$ROOT" rev-parse --verify --quiet origin/main > /dev/null; then
  echo "checking the contract for breaking changes against origin/main"
  (cd "$ROOT" && buf breaking proto --against ".git#ref=origin/main,subdir=proto") || {
    echo ""
    echo "The contract changed in a way that breaks a peer already speaking it."
    echo "Renaming or renumbering a field, changing its type or removing it needs"
    echo "a v2 package served alongside v1 through a deprecation window."
    exit 1
  }
else
  echo "skipping the breaking-change check, origin/main is not available"
fi

echo "checking the generated stubs are in step with the contract"
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

bash "$SCRIPT_DIR/gen-proto.sh" "$staging" > /dev/null

if ! diff -ru "$ROOT/internal/ipcproto/celerityv1" "$staging" > "$staging.diff"; then
  echo "the generated stubs are out of step with proto/:"
  cat "$staging.diff"
  echo ""
  echo "run: bash scripts/gen-proto.sh"
  echo "and commit the .proto and the regenerated stubs together."
  exit 1
fi

# Only checked when the runtime repository is to hand: a contributor without it
# should not be blocked by its absence, and CI supplies it explicitly.
UPSTREAM="${CELERITY_RUNTIME_PROTO_DIR:-}"
if [ -z "$UPSTREAM" ] || [ ! -d "$UPSTREAM" ]; then
  echo "skipping the upstream check, set CELERITY_RUNTIME_PROTO_DIR to enable it"
  exit 0
fi

echo "checking the vendored contract matches the runtime's"
if ! diff -ru "$UPSTREAM/celerity" "$ROOT/proto/celerity"; then
  echo ""
  echo "the vendored contract has fallen behind the runtime's."
  echo "copy it over proto/, run scripts/gen-proto.sh, and review the generated"
  echo "diff: it is the clearest signal of whether the change was additive."
  exit 1
fi

echo "the vendored contract matches the runtime's"
