#!/usr/bin/env bash
set -euo pipefail

# Sets each module's requirement of another module in this repository to the
# version that module is released at.
#
# release-please writes the versions into .release-please-manifest.json and the
# tags from it, but a go.mod's require of a sibling is an ordinary dependency as
# far as it is concerned and is left alone. Those requirements would then name
# whatever version was current when the import was added, and a consumer would
# get that one rather than the release it is installing.
#
# The replace directives mean the version is not what builds this repository, so
# nothing here fails when it drifts, which is exactly why it is checked. The
# first thing it breaks is somebody else's build, once the modules are
# published.
#
# Usage:
#   bash scripts/sync-module-versions.sh            # write
#   bash scripts/sync-module-versions.sh --check    # report drift, change nothing

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/modules.sh"

MANIFEST="$ROOT/.release-please-manifest.json"
MODULE_PATH="github.com/newstack-cloud/celerity-go-sdk"

CHECK=""
[ "${1:-}" = "--check" ] && CHECK=yes

cd "$ROOT"

# version_of prints the released version of a module, by its manifest key.
version_of() {
  local key="$1"
  local found
  found=$(sed -n "s|^[[:space:]]*\"${key//\//\\/}\": \"\([^\"]*\)\".*|\1|p" "$MANIFEST")
  if [ -z "$found" ]; then
    echo "no version in $(basename "$MANIFEST") for \"$key\"" >&2
    exit 1
  fi
  echo "v$found"
}

# key_for maps an import path back to the manifest key it is released under.
key_for() {
  local import="$1"
  if [ "$import" = "$MODULE_PATH" ]; then
    echo "."
  else
    echo "${import#"$MODULE_PATH"/}"
  fi
}

# requirements prints each requirement of a module in this repository, as the
# import path and the version.
#
# Read from the require lines rather than from `go mod edit -json`, whose Path
# and Version keys appear in the module, require and replace sections alike. A
# line-wise read of that pairs them across sections and answers nonsense, which
# is how this was wrong the first time.
requirements() {
  sed -nE "s|^[[:space:]]+($MODULE_PATH(/[^[:space:]]+)?) (v[^[:space:]]+).*|\\1 \\3|p;\
s|^require ($MODULE_PATH(/[^[:space:]]+)?) (v[^[:space:]]+).*|\\1 \\3|p" "$1"
}

drifted=0

for module in "${MODULES[@]}"; do
  modfile="$module/go.mod"

  # Every requirement of another module in this repository, with its version.
  while read -r import current; do
    [ -z "$import" ] && continue

    want="$(version_of "$(key_for "$import")")"
    [ "$current" = "$want" ] && continue

    drifted=$((drifted + 1))
    if [ -n "$CHECK" ]; then
      echo "$modfile: requires $import $current, released at $want"
      continue
    fi

    echo "$modfile: $import $current -> $want"
    (cd "$module" && GOWORK=off go mod edit -require="$import@$want")
  done < <(requirements "$modfile")

done

if [ -n "$CHECK" ] && [ "$drifted" -gt 0 ]; then
  echo ""
  echo "$drifted requirement(s) name a version the module is not released at."
  echo "Run: bash scripts/sync-module-versions.sh"
  exit 1
fi

if [ -z "$CHECK" ] && [ "$drifted" -eq 0 ]; then
  echo "every requirement already names the released version"
fi
