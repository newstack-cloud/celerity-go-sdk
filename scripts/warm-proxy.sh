#!/usr/bin/env bash
set -uo pipefail

# Asks the Go module proxy for each module version this release published.
#
# Nothing publishes a Go module. A semver tag is the publication, and
# proxy.golang.org fetches from the repository the first time anybody asks for
# that version. The ask also records the version in index.golang.org, which is
# where pkg.go.dev learns it exists, so without one the documentation appears
# whenever the first consumer happens to fetch rather than when the release is
# announced.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MANIFEST="$ROOT/.release-please-manifest.json"
MODULE_PATH="github.com/newstack-cloud/celerity-go-sdk"

if [ -z "${PATHS:-}" ] || [ "$PATHS" = "[]" ]; then
  echo "no module was released, so there is nothing to ask for"
  exit 0
fi

# The directories released, one per line, out of the JSON array.
released=$(printf '%s' "$PATHS" | tr -d '[]"' | tr ',' '\n' | sed 's/^ *//;s/ *$//')

asking=$(mktemp -d)
trap 'rm -rf "$asking"' EXIT
(cd "$asking" && go mod init warm-proxy >/dev/null 2>&1)

# Off, so a workspace inherited from the environment cannot answer either.
export GOWORK=off

failed=0

while read -r dir; do
  [ -z "$dir" ] && continue

  version=$(sed -n "s|^[[:space:]]*\"${dir//\//\\/}\": \"\([^\"]*\)\".*|\1|p" "$MANIFEST")
  if [ -z "$version" ]; then
    echo "no version in the manifest for \"$dir\", so it was not asked for" >&2
    failed=$((failed + 1))
    continue
  fi

  if [ "$dir" = "." ]; then
    module="$MODULE_PATH"
  else
    module="$MODULE_PATH/$dir"
  fi

  echo "asking for $module@v$version"
  if ! (cd "$asking" && go list -m "$module@v$version"); then
    echo "  the proxy did not serve it, which the next consumer to fetch will fix" >&2
    failed=$((failed + 1))
  fi
done <<< "$released"

if [ "$failed" -gt 0 ]; then
  echo ""
  echo "$failed module(s) were not served. The tags are what publish them, so the"
  echo "release stands; pkg.go.dev will catch up on the first fetch."
fi
